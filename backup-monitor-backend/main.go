package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/ssh"
)

// ===== CẤU HÌNH MÁY ẢO: Đọc từ biến môi trường =====

// ========================
// AUTH - JWT
// ========================
func getJWTSecret() []byte {
	return []byte(getEnv("JWT_SECRET", "fallback_secret_change_this"))
}

func generateToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(8 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getJWTSecret())
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || len(authHeader) < 8 || authHeader[:7] != "Bearer " {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Chưa đăng nhập"})
			return
		}
		tokenStr := authHeader[7:]
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return getJWTSecret(), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token không hợp lệ hoặc đã hết hạn"})
			return
		}
		c.Next()
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// ===== CACHE NẰM TRONG RAM CHO CÁC API ĐỌC (Giảm tải SSH & Chống nghẽn) =====
type cacheEntry struct {
	value     []byte
	expiresAt time.Time
}

type flightCall struct {
	done chan struct{}
	val  []byte
	err  error
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	flightMu sync.Mutex
	inFlight = map[string]*flightCall{}
)

func cachedGet(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	cacheMu.Lock()
	entry, hasEntry := cache[key]
	isFresh := hasEntry && time.Now().Before(entry.expiresAt)
	cacheMu.Unlock()

	if isFresh {
		return entry.value, nil
	}

	flightMu.Lock()
	call, isRunning := inFlight[key]
	if isRunning {
		flightMu.Unlock()
		if hasEntry && len(entry.value) > 0 {
			return entry.value, nil
		}
		<-call.done
		return call.val, call.err
	}

	call = &flightCall{done: make(chan struct{})}
	inFlight[key] = call
	flightMu.Unlock()

	doWork := func() ([]byte, error) {
		b, err := producer()
		if err == nil {
			cacheMu.Lock()
			cache[key] = cacheEntry{value: b, expiresAt: time.Now().Add(ttl)}
			cacheMu.Unlock()
		}

		call.val = b
		call.err = err

		flightMu.Lock()
		delete(inFlight, key)
		close(call.done)
		flightMu.Unlock()

		return b, err
	}

	// Nếu đã có cache cũ, trả về luôn và làm mới ngầm (Stale-While-Revalidate)
	if hasEntry && len(entry.value) > 0 {
		go doWork()
		return entry.value, nil
	}

	b, err := doWork()
	if err != nil && hasEntry && len(entry.value) > 0 {
		return entry.value, nil
	}
	return b, err
}

func invalidate(keys ...string) {
	cacheMu.Lock()
	for _, k := range keys {
		delete(cache, k)
	}
	cacheMu.Unlock()
}

// ===== SSH CONNECTION POOL =====
// Tái sử dụng kết nối SSH thay vì tạo mới mỗi lần gọi (tiết kiệm ~300ms/request)
var (
	sshClient   *ssh.Client
	sshClientMu sync.Mutex
)

func dialSSH() (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: getEnv("SSH_USER", "root"),
		Auth: []ssh.AuthMethod{
			ssh.Password(getEnv("SSH_PASSWORD", "")),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	client, err := ssh.Dial("tcp", getEnv("SSH_HOST", "192.168.37.130:22"), config)
	if err != nil {
		return nil, err
	}

	// Gửi keepalive mỗi 30 giây để server không đóng kết nối idle
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				// Kết nối đã chết, xóa khỏi pool
				sshClientMu.Lock()
				if sshClient == client {
					sshClient = nil
				}
				sshClientMu.Unlock()
				client.Close()
				return
			}
		}
	}()

	return client, nil
}

func getSSHClient() (*ssh.Client, error) {
	sshClientMu.Lock()
	defer sshClientMu.Unlock()

	// Kiểm tra kết nối hiện tại còn sống không
	if sshClient != nil {
		sess, err := sshClient.NewSession()
		if err == nil {
			sess.Close()
			return sshClient, nil
		}
		// Kết nối đã chết, đóng và tạo mới
		sshClient.Close()
		sshClient = nil
	}

	client, err := dialSSH()
	if err != nil {
		return nil, err
	}
	sshClient = client
	return sshClient, nil
}

// Hàm thực thi lệnh SSH trên máy ảo (tái sử dụng kết nối pool, timeout 60s)
func executeSSHCommand(command string) (string, error) {
	client, err := getSSHClient()
	if err != nil {
		return "", err
	}

	session, err := client.NewSession()
	if err != nil {
		// Kết nối có thể bị drop, xóa pool và thử lại 1 lần
		sshClientMu.Lock()
		sshClient = nil
		sshClientMu.Unlock()

		client, err = getSSHClient()
		if err != nil {
			return "", err
		}
		session, err = client.NewSession()
		if err != nil {
			return "", err
		}
	}
	defer session.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		done <- runResult{err: session.Run(command)}
	}()

	select {
	case <-time.After(60 * time.Second):
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("SSH command timed out after 60s")
	case res := <-done:
		if res.err != nil {
			return "", fmt.Errorf("Lệnh lỗi: %v, Chi tiết: %s", res.err, stderrBuf.String())
		}
		return stdoutBuf.String(), nil
	}
}

// ===== HELPERS =====
type flushWriter struct {
	w io.Writer
	f func()
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil && fw.f != nil {
		fw.f()
	}
	return n, err
}

var snapshotPathRe = regexp.MustCompile(`^[A-Za-z0-9._\-\/]+$`)

func validSnapshotPath(p string) bool {
	return p != "" && !strings.Contains(p, "..") && snapshotPathRe.MatchString(p)
}

func main() {
	// Đọc cấu hình từ file .env
	godotenv.Load()

	r := gin.Default()

	// Cấu hình CORS để Frontend gọi được API
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	// Dọn dẹp tiến trình treo trên máy ảo khi khởi động backend
	go func() {
		time.Sleep(1 * time.Second)
		_, _ = executeSSHCommand("killall -9 rclone curl 2>/dev/null")
	}()

	r.GET("/api/debug-top", func(c *gin.Context) {
		out, err := executeSSHCommand("free -m")
		c.String(200, out+"\nERR: "+fmt.Sprintf("%v", err))
	})

	// API 1: Lấy thông số tài nguyên máy ảo (CPU, RAM, Disk, Uptime)

	// LOGIN - không cần auth
	r.POST("/api/login", func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		adminUser := getEnv("ADMIN_USER", "admin")
		adminPass := getEnv("ADMIN_PASSWORD", "admin123")
		if req.Username != adminUser || req.Password != adminPass {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Tên đăng nhập hoặc mật khẩu không đúng"})
			return
		}
		token, err := generateToken(req.Username)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token})
	})

	// Tất cả route bên dưới đều yêu cầu đăng nhập
	auth := r.Group("/")
	auth.Use(authMiddleware())
	{
		auth.GET("/api/server-status", func(c *gin.Context) {
			cmds := `
DISK=$(df -h / | awk 'NR==2 {printf "{\"total\":\"%s\", \"used\":\"%s\", \"free\":\"%s\", \"usage\":\"%s\"}", $2, $3, $4, $5}')
RAM=$(free -m | awk 'NR==2{printf "{\"total\":\"%sMB\", \"used\":\"%sMB\", \"free\":\"%sMB\", \"usage\":\"%.1f%%\"}", $2, $3, $4, $3*100/$2}')
CPU=$(vmstat 1 2 | tail -1 | awk '{printf "%.1f%%", 100 - $15}')
UPTIME=$(uptime -p | sed 's/up //')
WEBSITES=$(find /www/wwwroot -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -v "/default$" | wc -l)
DATABASES=$(find /www/server/data -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -vE "/(mysql|performance_schema|sys|phpmyadmin)$" | wc -l)
CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -q "acme" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -q "acme" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

DRIVE_CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -qE "acme|backup\.py" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
DRIVE_CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if ! grep -qE "acme|backup\.py" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

LOCAL_CRONJOBS=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if grep -q "backup\.py" "$cmd" 2>/dev/null; then echo 1; fi; done | wc -l)
LOCAL_CRON_TIMES=$(crontab -l 2>/dev/null | grep "/www/server/cron" | while read -r min hour d m w cmd remainder; do if grep -q "backup\.py" "$cmd" 2>/dev/null; then printf "%02d:%02d\n" "$hour" "$min"; fi; done | sort | paste -sd, - | sed 's/,/, /g')

LOCAL_BACKUP_DIR="/www/backup"
if [ -d "$LOCAL_BACKUP_DIR" ]; then
  LOCAL_SIZE=$(du -sh "$LOCAL_BACKUP_DIR" 2>/dev/null | awk '{print $1}')
  LOCAL_COUNT=$(find "$LOCAL_BACKUP_DIR" -type f 2>/dev/null | wc -l)
  LOCAL_LATEST=$(find "$LOCAL_BACKUP_DIR" -type f -printf '%T@ %p\n' 2>/dev/null | sort -rn | head -1 | awk '{print $2}')
  LOCAL_LATEST_DATE=$(stat -c '%y' "$LOCAL_LATEST" 2>/dev/null | cut -d' ' -f1)
  LOCAL_LATEST_NAME=$(basename "$LOCAL_LATEST" 2>/dev/null)
else
  LOCAL_SIZE="0"
  LOCAL_COUNT=0
  LOCAL_LATEST_DATE=""
  LOCAL_LATEST_NAME=""
fi

LOCAL_BACKUP="{\"size\":\"$LOCAL_SIZE\", \"count\":$LOCAL_COUNT, \"latest_date\":\"$LOCAL_LATEST_DATE\", \"latest_name\":\"$LOCAL_LATEST_NAME\"}"

echo "{\"disk\": $DISK, \"ram\": $RAM, \"cpu\": \"$CPU\", \"uptime\": \"$UPTIME\", \"websites\": $WEBSITES, \"databases\": $DATABASES, \"drive_crons\": $DRIVE_CRONJOBS, \"drive_cron_times\": \"$DRIVE_CRON_TIMES\", \"local_crons\": $LOCAL_CRONJOBS, \"local_cron_times\": \"$LOCAL_CRON_TIMES\", \"crons\": $CRONJOBS, \"cron_times\": \"$CRON_TIMES\", \"local_backup\": $LOCAL_BACKUP}"
`
			output, err := cachedGet("server-status", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kết nối SSH lấy thông số", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 2: Kiểm tra các file Backup trên Google Drive và Dung lượng
		r.GET("/api/backup-status", func(c *gin.Context) {
			cmds := `
ABOUT=$(timeout -k 5s 180s /usr/bin/rclone about gdrive: --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --json 2>/dev/null)
if [ -n "$ABOUT" ]; then
  echo "$ABOUT" > /tmp/rclone_about.json
elif [ -f /tmp/rclone_about.json ]; then
  ABOUT=$(cat /tmp/rclone_about.json)
else
  ABOUT="{}"
fi
TODAY=$(date +"%Y-%m-%d")

EVAL_OUT=$(timeout -k 5s 180s /usr/bin/rclone lsf -R --format "ps" gdrive:Backup --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list 2>/dev/null | awk -F';' -v today="$TODAY" '
$2 != "-1" && $2 != "" {
    total_bytes += $2
    total_files++
    split($1, a, "/")
    day = a[1]
    if (day != "") {
        days[day] = 1
        size[day] += $2
        count[day]++
        if (day == today && length(a[2]) > 0) {
            today_dirs[a[2]] = 1
            if (a[2] == "site") { today_site++; today_site_bytes += $2 }
            else if (a[2] == "database") { today_db++; today_db_bytes += $2 }
            else if (a[2] == "panel") { today_panel++; today_panel_bytes += $2 }
        }
    }
}
END {
    folders = 0
    for (d in days) folders++

    td = ""
    for (subd in today_dirs) td = td subd ","

    n = asorti(size, sorted_days)
    hist = "["
    first = 1
    for (i = 1; i <= n; i++) {
        d = sorted_days[i]
        if (!first) hist = hist ","
        hist = hist "{\"date\":\"" d "\", \"bytes\":" size[d] ", \"files\":" count[d] "}"
        first = 0
    }
    hist = hist "]"

    printf "%d|%d|%d|%s|%s|%d|%d|%d|%d|%d|%d", total_bytes, total_files, folders, td, hist, today_site+0, today_db+0, today_panel+0, today_site_bytes+0, today_db_bytes+0, today_panel_bytes+0
}')

if [ "$EVAL_OUT" != "0|0|0||[]" ] && [ -n "$EVAL_OUT" ]; then
  echo "$EVAL_OUT" > /tmp/rclone_lsf.txt
elif [ -f /tmp/rclone_lsf.txt ]; then
  EVAL_OUT=$(cat /tmp/rclone_lsf.txt)
fi

TOTAL_BYTES=$(echo "$EVAL_OUT" | cut -d'|' -f1)
TOTAL_FILES=$(echo "$EVAL_OUT" | cut -d'|' -f2)
TOTAL_FOLDERS=$(echo "$EVAL_OUT" | cut -d'|' -f3)
DIRS=$(echo "$EVAL_OUT" | cut -d'|' -f4)
RAW_TAIL=$(echo "$EVAL_OUT" | cut -d'|' -f5-)
TODAY_SITE=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-5)}')
TODAY_DB=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-4)}')
TODAY_PANEL=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-3)}')
TODAY_SITE_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-2)}')
TODAY_DB_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $(NF-1)}')
TODAY_PANEL_BYTES=$(echo "$EVAL_OUT" | awk -F'|' '{print $NF}')
HISTORY=$(echo "$RAW_TAIL" | sed 's/|[0-9]*|[0-9]*|[0-9]*|[0-9]*|[0-9]*|[0-9]*$//')

if [ -z "$TOTAL_BYTES" ]; then TOTAL_BYTES="0"; fi
if [ -z "$TOTAL_FILES" ]; then TOTAL_FILES="0"; fi
if [ -z "$TOTAL_FOLDERS" ]; then TOTAL_FOLDERS="0"; fi
if [ -z "$HISTORY" ] || [ "$HISTORY" = "]" ]; then HISTORY="[]"; fi
if [ -z "$ABOUT" ]; then ABOUT="{}"; fi

SIZE="{\"bytes\": $TOTAL_BYTES, \"count\": $TOTAL_FILES}"

ACTIVITY="["
FIRST=1
if [ -f /var/log/aapanel_backup.log ]; then
  while IFS='|' read -r ad at adur ast; do
    [ -z "$ad" ] && continue
    if [ "$FIRST" -ne 1 ]; then ACTIVITY="$ACTIVITY,"; fi
    FIRST=0
    ACTIVITY="$ACTIVITY{\"name\":\"Lưu trữ bản backup lên Google Drive\",\"date\":\"$ad\",\"time\":\"$at\",\"duration\":\"$adur\",\"status\":\"$ast\"}"
  done < /var/log/aapanel_backup.log
fi
ACTIVITY="$ACTIVITY]"

LOCAL_LOGS=$(awk '
    /start backup\[/ {
        match($0, /\[(.*)\]/, arr)
        dt = arr[1]
        split(dt, d, " ")
        date = d[1]
        time = d[2]
    }
    /Backup site: / {
        match($0, /site: (.*)/, arr)
        name = "Backup Website: " arr[1]
    }
    /Backup .*database: / {
        match($0, /database: (.*)/, arr)
        name = "Backup Database: " arr[1]
    }
    /Compression completed, took / {
        match($0, /took ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    /Database backup completed, taking / {
        match($0, /taking ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    ' /www/server/cron/*.log 2>/dev/null | sed 's/,$//')
LOCAL_ACTIVITY="[$LOCAL_LOGS]"

echo "{\"about\": $ABOUT, \"size\": $SIZE, \"dirs\": \"$DIRS\", \"totalFolders\": $TOTAL_FOLDERS, \"history\": $HISTORY, \"activity\": $ACTIVITY, \"localActivity\": $LOCAL_ACTIVITY, \"todayBreakdown\": {\"site\": ${TODAY_SITE:-0}, \"database\": ${TODAY_DB:-0}, \"panel\": ${TODAY_PANEL:-0}}, \"todayBreakdownBytes\": {\"site\": ${TODAY_SITE_BYTES:-0}, \"database\": ${TODAY_DB_BYTES:-0}, \"panel\": ${TODAY_PANEL_BYTES:-0}}}"
`
			output, err := cachedGet("backup-status", 120*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})

			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi đọc Google Drive", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 3: Kích hoạt chạy thủ công tiến trình Backup (có nhận tham số)
		r.POST("/api/run-job", func(c *gin.Context) {
			var req struct {
				Script string `json:"script"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				// Fallback: Nếu không gửi script, lấy đại 1 cái như cũ
				cmds := `
SCRIPT_FILE=$(find /www/server/cron /root /var/spool/cron -name "*.sh" -type f 2>/dev/null | xargs grep -l "rclone copy" 2>/dev/null | head -n 1)
if [ -n "$SCRIPT_FILE" ]; then
	bash "$SCRIPT_FILE" > /dev/null 2>&1 &
	echo "{\"status\": \"success\", \"message\": \"Triggered $SCRIPT_FILE\"}"
else
	echo "{\"status\": \"error\", \"message\": \"Script not found\"}"
fi
`
				output, err := executeSSHCommand(cmds)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "SSH error", "detail": err.Error()})
					return
				}
				invalidate("backup-status", "cron-jobs", "recovery-snapshots")
				c.Data(http.StatusOK, "application/json", []byte(output))
				return
			}

			// Nếu có script truyền lên, chạy đúng script đó
			if req.Script == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Script path required"})
				return
			}

			// Bảo mật cơ bản: chặn tiêm lệnh
			if strings.Contains(req.Script, ";") || strings.Contains(req.Script, "&") || strings.Contains(req.Script, "|") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid script path"})
				return
			}

			cmds := fmt.Sprintf(`
if [ -f "%s" ]; then
	bash "%s" > /dev/null 2>&1 &
	echo "{\"status\": \"success\", \"message\": \"Triggered %s\"}"
else
	echo "{\"status\": \"error\", \"message\": \"Script not found: %s\"}"
fi
`, req.Script, req.Script, req.Script, req.Script)

			output, err := executeSSHCommand(cmds)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "SSH error", "detail": err.Error()})
				return
			}
			invalidate("backup-status", "cron-jobs", "recovery-snapshots")
			c.Data(http.StatusOK, "application/json", []byte(output))
		})

		// API 4: Giám sát Uptime các website (động theo thư mục /www/wwwroot)
		r.GET("/api/websites-status", func(c *gin.Context) {
			cmds := `
sites=$(ls -d /www/wwwroot/*/ 2>/dev/null | sed 's#/$##' | xargs -n1 basename 2>/dev/null | grep -v '^default$' | head -n 10)
if [ -z "$sites" ]; then
  sites="web1.local
web2.local
web3.local"
fi
echo "["
first=1
for site in $sites; do
  res=$(curl -o /dev/null -s -w "%{http_code},%{time_total}" --resolve "$site:80:127.0.0.1" --max-time 2 "http://$site" 2>/dev/null)
  if [ -z "$res" ] || [ "$(echo "$res" | cut -d',' -f1)" = "000" ]; then
    res=$(curl -o /dev/null -s -w "%{http_code},%{time_total}" --max-time 2 "http://$site" 2>/dev/null || echo "000,0")
  fi
  code=$(echo "$res" | cut -d',' -f1)
  sec=$(echo "$res" | cut -d',' -f2)
  ms=$(awk -v s="$sec" 'BEGIN {printf "%.0fms", s*1000}')
  status="OFFLINE"
  if [ "$code" = "200" ] || [ "$code" = "301" ] || [ "$code" = "302" ] || [ "$code" = "403" ]; then
    status="ONLINE"
  fi
  if [ "$first" -ne 1 ]; then printf ","; fi
  first=0
  printf '{"name":"%s","status":"%s","code":"%s","time":"%s"}' "$site" "$status" "$code" "$ms"
done
echo "]"
`
			output, err := cachedGet("websites-status", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kiểm tra website", "detail": err.Error()})
				return
			}

			c.Data(http.StatusOK, "application/json", output)
		})

		// API 5: Đọc Log hệ thống Real-time
		r.GET("/api/logs", func(c *gin.Context) {
			logType := c.Query("type")
			cmd := ""

			switch logType {
			case "backup":
				// Kết hợp: Log cron + Chi tiết file backup trên Google Drive
				cmd = `
echo "══════════════════════════════════════════════════════"
echo "  LỊCH SỬ CHẠY SCRIPT (Cron Execution Log)"
echo "══════════════════════════════════════════════════════"
tail -n 15 /www/server/cron/*.log 2>/dev/null
echo ""
echo "══════════════════════════════════════════════════════"
echo "  CHI TIẾT BẢN SAO LƯU TRÊN GOOGLE DRIVE"
echo "══════════════════════════════════════════════════════"
for DAY in $(/usr/bin/rclone lsf --dirs-only gdrive:Backup --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | sort | tail -n 3); do
  DAYNAME=$(echo "$DAY" | tr -d '/')
  echo ""
  echo "📅 [$DAYNAME]"
  echo "  📁 site/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/site/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
  echo "  📁 database/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/database/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
done
`
			case "system":
				cmd = "tail -n 50 /var/log/messages"
			case "secure":
				cmd = "tail -n 50 /var/log/secure"
			default:
				cmd = "tail -n 50 /var/log/messages"
			}

			key := "logs-" + logType
			ttl := 10 * time.Second
			if logType == "backup" {
				ttl = 30 * time.Second
			}
			output, err := cachedGet(key, ttl, func() ([]byte, error) {
				out, e := executeSSHCommand(cmd)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log", "detail": err.Error()})
				return
			}

			c.String(http.StatusOK, string(output))
		})

		// API 6: Liệt kê các tiến trình định kỳ (Cron Jobs) với trạng thái thật
		r.GET("/api/cron-jobs", func(c *gin.Context) {
			cmds := `
CRONTAB=$(crontab -l 2>/dev/null | grep "/www/server/cron" | grep -v "acme")
echo "["
FIRST=1
printf '%s\n' "$CRONTAB" | while IFS= read -r line; do
  [ -z "$line" ] && continue
  # Tách lịch trình cron (5 trường đầu)
  SCHED=$(echo "$line" | awk '{print $1" "$2" "$3" "$4" "$5}')
  
  # Tìm đường dẫn script (ví dụ: /www/server/cron/a50afd8f7d0e95dc2fd7bbbccfdcccd82)
  SCRIPT=$(echo "$line" | grep -oE '/www/server/cron/[a-zA-Z0-9]+' | head -n 1)
  [ -z "$SCRIPT" ] && continue
  
  # Tên script (chính là chuỗi mã băm)
  HASHNAME=$(basename "$SCRIPT")
  LOG="/www/server/cron/${HASHNAME}.log"
    # Detect common aaPanel cron types
    CONTENT=$(cat "$SCRIPT" 2>/dev/null)
    if echo "$CONTENT" | grep -q "acme_v2.py"; then
        NAME="Gia hạn SSL"
    elif echo "$CONTENT" | grep -q "backup.py database"; then
        NAME="Backup Database"
    elif echo "$CONTENT" | grep -q "backup.py site"; then
        NAME="Backup Site"
    elif echo "$CONTENT" | grep -q "G-Drive" || echo "$CONTENT" | grep -q "rclone" || echo "$CONTENT" | grep -q "gdrive" || echo "$CONTENT" | grep -q "/root/backup-monitor" || echo "$CONTENT" | grep -q "auto_backup.sh"; then
        NAME="Lưu trữ bản backup lên Google Drive"
    else
        NAME="Cron Task ($HASHNAME)"
    fi

  LAST_RUN=""
  STATUS="never"
  if [ -f "$LOG" ]; then
    LAST_RUN=$(stat -c '%y' "$LOG" 2>/dev/null | cut -d'.' -f1)
    if grep -qiE 'error|fail|failed|exception|panic' "$LOG" 2>/dev/null; then
      STATUS="failed"
    elif [ -s "$LOG" ]; then
      STATUS="success"
    fi
  fi
  
  if [ "$FIRST" -ne 1 ]; then printf ","; fi
  FIRST=0
  printf '{"name":"%s","script":"%s","schedule":"%s","last_run":"%s","status":"%s"}' "$NAME" "$SCRIPT" "$SCHED" "$LAST_RUN" "$STATUS"
done
echo "]"
`
			output, err := cachedGet("cron-jobs", 15*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc tiến trình định kỳ", "detail": err.Error()})
				return
			}
			c.Data(http.StatusOK, "application/json", output)
		})

		// API 7: Đọc cấu hình hệ thống (lưu trên máy ảo)
		r.GET("/api/config", func(c *gin.Context) {
			cmds := `
CONFIG=/root/backup-monitor/config.json
if [ -f "$CONFIG" ]; then
  cat "$CONFIG"
else
  echo '{"retention_days":7,"notify_enabled":false,"notify_token":"","notify_chat_id":"","email_recipient":""}'
fi
`
			output, err := cachedGet("config", 10*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình", "detail": err.Error()})
				return
			}
			c.Data(http.StatusOK, "application/json", output)
		})

		// API 8: Ghi cấu hình hệ thống (validate JSON rồi base64 truyền qua SSH)
		r.PUT("/api/config", func(c *gin.Context) {
			payload, err := c.GetRawData()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Body không hợp lệ"})
				return
			}
			var cfg map[string]interface{}
			if err := json.Unmarshal(payload, &cfg); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "JSON không hợp lệ"})
				return
			}
			// Chỉ chấp nhận các trường đã biết
			allowed := map[string]bool{
				"retention_days":  true,
				"notify_enabled":  true,
				"notify_token":    true,
				"notify_chat_id":  true,
				"email_recipient": true,
			}
			clean := map[string]interface{}{}
			for k, v := range cfg {
				if allowed[k] {
					clean[k] = v
				}
			}
			normalized, _ := json.Marshal(clean)
			b64 := base64.StdEncoding.EncodeToString(normalized)

			cmds := fmt.Sprintf(`mkdir -p /root/backup-monitor && echo %s | base64 -d > /root/backup-monitor/config.json && echo OK`, b64)
			output, err := executeSSHCommand(cmds)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể ghi cấu hình", "detail": err.Error()})
				return
			}
			invalidate("config")
			c.JSON(http.StatusOK, gin.H{"status": "success", "message": strings.TrimSpace(output), "config": clean})
		})

		// API 9: Liệt kê các ảnh chụp sao lưu trên Google Drive
		r.GET("/api/recovery-snapshots", func(c *gin.Context) {
			cmds := `
/usr/bin/rclone lsf -R --format "pst" gdrive:Backup --config /root/.config/rclone/rclone.conf --fast-list --timeout 15s --contimeout 5s 2>/dev/null | while IFS=';' read -r p s t; do
  [ -z "$p" ] && continue
  if [ "$s" != "-1" ]; then
    DATE=$(echo "$p" | cut -d/ -f1)
    SEG=$(echo "$p" | awk -F'/' '{print NF}')
    if [ "$SEG" -ge 2 ]; then CAT=$(echo "$p" | cut -d/ -f2); else CAT="root"; fi
    NAME=$(basename "$p")
    printf '%s|%s|%s|%s|%s\n' "$DATE" "$CAT" "$NAME" "$s" "$t"
  fi
done
`
			output, err := cachedGet("recovery-snapshots", 30*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": err.Error()})
				return
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			type snap struct {
				Date     string `json:"date"`
				Category string `json:"category"`
				Name     string `json:"name"`
				Size     int64  `json:"size"`
				Modified string `json:"modified"`
			}
			snaps := []snap{}
			for _, line := range lines {
				parts := strings.Split(line, "|")
				if len(parts) < 4 {
					continue
				}
				var size int64
				fmt.Sscanf(parts[3], "%d", &size)
				modified := ""
				if len(parts) >= 5 {
					modified = parts[4]
				}
				snaps = append(snaps, snap{
					Date:     parts[0],
					Category: parts[1],
					Name:     parts[2],
					Size:     size,
					Modified: modified,
				})
			}
			c.JSON(http.StatusOK, snaps)
		})

		// API 10: Liệt kê các file backup cục bộ
		r.GET("/api/local-snapshots", func(c *gin.Context) {
			cmds := `
find /www/backup -type f -mtime -14 \( -name "*.tar.gz" -o -name "*.sql" -o -name "*.zip" -o -name "*.gz" \) -printf '%T@|%s|%P\n' 2>/dev/null | sort -rn | head -n 500 | awk -F'|' '
{
  # Convert epoch to date (requires GNU awk or shell date)
  cmd = "date -d @" int($1) " +\"%Y-%m-%d %H:%M:%S\"";
  cmd | getline date_str;
  close(cmd);
  size=$2;
  path=$3;
  # Category is the first dir
  split(path, p, "/");
  if(length(p)>1) { cat=p[1]; name=p[length(p)] } else { cat="root"; name=path }
  printf "%s|%s|%s|%s\n", date_str, cat, name, size
}'
`
			output, err := cachedGet("local-snapshots", 30*time.Second, func() ([]byte, error) {
				out, e := executeSSHCommand(cmds)
				return []byte(out), e
			})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu cục bộ", "detail": err.Error()})
				return
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			type localSnap struct {
				Date     string `json:"date"`
				Category string `json:"category"`
				Name     string `json:"name"`
				Size     int64  `json:"size"`
			}
			snaps := []localSnap{}
			for _, line := range lines {
				parts := strings.Split(line, "|")
				if len(parts) < 4 {
					continue
				}
				var size int64
				fmt.Sscanf(parts[3], "%d", &size)
				snaps = append(snaps, localSnap{
					Date:     parts[0],
					Category: parts[1],
					Name:     parts[2],
					Size:     size,
				})
			}
			c.JSON(http.StatusOK, snaps)
		})

		// API 11: Tải về snapshot từ Google Drive về trình duyệt (stream qua SSH)
		r.GET("/api/download-snapshot", func(c *gin.Context) {
			rel := c.Query("path")
			if !validSnapshotPath(rel) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
				return
			}

			config := &ssh.ClientConfig{
				User: getEnv("SSH_USER", "root"),
				Auth: []ssh.AuthMethod{
					ssh.Password(getEnv("SSH_PASSWORD", "")),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         15 * time.Second,
			}
			client, err := ssh.Dial("tcp", getEnv("SSH_HOST", "192.168.37.130:22"), config)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kết nối SSH", "detail": err.Error()})
				return
			}
			defer client.Close()

			session, err := client.NewSession()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mở phiên SSH"})
				return
			}
			defer session.Close()

			remotePath := "gdrive:Backup/" + rel
			name := filepath.Base(rel)
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
			c.Header("Content-Type", "application/octet-stream")

			rc := http.NewResponseController(c.Writer)
			fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
			session.Stdout = fw

			var errBuf bytes.Buffer
			session.Stderr = &errBuf

			cmd := fmt.Sprintf(`/usr/bin/rclone cat "%s" --config /root/.config/rclone/rclone.conf`, remotePath)
			if err := session.Run(cmd); err != nil {
				fmt.Println("Download error:", err, errBuf.String())
			}
		})

		fmt.Println("Backend đang chạy tại http://localhost:8080")
		// API 10: Xoa nhat ky
		r.POST("/api/clear-log", func(c *gin.Context) {
			var req struct {
				Target string `json:"target"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Lỗi tham số"})
				return
			}
			if req.Target == "drive" {
				_, err := executeSSHCommand("> /var/log/aapanel_backup.log")
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa log", "detail": err.Error()})
					return
				}
				cacheMu.Lock()
				delete(cache, "backup-status")
				cacheMu.Unlock()
				c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên Google Drive"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên máy chủ"})
		})

	}
	r.Run(":8080")
}
