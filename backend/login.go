package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"backup-dashboard-backend/middleware"
)

// loginRequest holds the credentials sent by the dashboard login form.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

const (
	maxLoginFailures = 5
	blockDuration    = 10 * time.Minute
)

var (
	loginMu       sync.Mutex
	loginAttempts = map[string]*loginAttempt{}
)

type loginAttempt struct {
	count   int
	blocked time.Time // thời điểm bắt đầu bị khoá tạm thời
}

// loginBlockWait trả về thời gian còn phải chờ nếu IP đang bị khoá (không thay đổi trạng thái).
func loginBlockWait(ip string) time.Duration {
	loginMu.Lock()
	defer loginMu.Unlock()
	a := loginAttempts[ip]
	if a == nil || a.blocked.IsZero() {
		return 0
	}
	remaining := time.Until(a.blocked.Add(blockDuration))
	if remaining < 0 {
		return 0
	}
	return remaining
}

// recordLoginFailure ghi nhận một lần đăng nhập thất bại cho IP.
// Trả về thời gian bị khoá nếu IP vừa vượt ngưỡng.
func recordLoginFailure(ip string) time.Duration {
	loginMu.Lock()
	defer loginMu.Unlock()

	a := loginAttempts[ip]
	if a == nil {
		a = &loginAttempt{}
		loginAttempts[ip] = a
	}

	// Nếu đang bị khoá, vẫn giữ nguyên trạng thái.
	if !a.blocked.IsZero() {
		remaining := time.Until(a.blocked.Add(blockDuration))
		if remaining > 0 {
			return remaining
		}
		a.blocked = time.Time{}
		a.count = 0
	}

	a.count++
	if a.count >= maxLoginFailures {
		a.blocked = time.Now()
		return blockDuration
	}
	return 0
}

// resetLoginAttempts xoá trạng thái theo dõi của IP sau khi đăng nhập thành công.
func resetLoginAttempts(ip string) {
	loginMu.Lock()
	delete(loginAttempts, ip)
	loginMu.Unlock()
}

func clientIP(r *http.Request) string {
	// Dùng IP từ kết nối thực tế (RemoteAddr). Không tin X-Forwarded-For vì client
	// có thể tự đặt header này để giả mạo IP và vô hiệu hoá rate-limit.
	// Lưu ý: nếu đặt sau một reverse proxy tin cậy, IP ở đây là IP của proxy
	// (rate-limit sẽ tính chung cho mọi client đằng sau proxy).
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// handleLogin verifies admin credentials and returns a JWT token.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Chỉ hỗ trợ POST", http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)

	// Rate-limit: chặn trước nếu IP đang bị khoá tạm thời.
	if wait := loginBlockWait(ip); wait > 0 {
		w.Header().Set("Retry-After", strconvItoa(int(wait.Seconds())))
		http.Error(w, "Quá nhiều lần đăng nhập thất bại. Hãy thử lại sau vài phút.",
			http.StatusTooManyRequests)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Dữ liệu không hợp lệ", http.StatusBadRequest)
		return
	}

	adminUser := os.Getenv("ADMIN_USERNAME")
	adminPass := os.Getenv("ADMIN_PASSWORD")
	if adminUser == "" || adminPass == "" {
		http.Error(w, "Server chưa cấu hình tài khoản admin", http.StatusInternalServerError)
		return
	}

	if req.Username != adminUser || req.Password != adminPass {
		recordLoginFailure(ip)
		http.Error(w, "Sai tên đăng nhập hoặc mật khẩu", http.StatusUnauthorized)
		return
	}

	resetLoginAttempts(ip)

	token, err := middleware.GenerateToken()
	if err != nil {
		http.Error(w, "Lỗi tạo token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"token":   token,
		"message": "Đăng nhập thành công",
	})
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
