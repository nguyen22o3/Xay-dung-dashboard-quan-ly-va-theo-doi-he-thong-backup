package backupops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RestoreCheckItem struct {
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	SHA256        string `json:"sha256,omitempty"`
	Status        string `json:"status"`
	Detail        string `json:"detail"`
	Files         int    `json:"files"`
	ExpandedBytes int64  `json:"expandedBytes"`
}
type RestoreCheck struct {
	Error      string             `json:"error,omitempty"`
	Token      string             `json:"token"`
	StartedAt  time.Time          `json:"startedAt"`
	FinishedAt *time.Time         `json:"finishedAt,omitempty"`
	Status     string             `json:"status"`
	Items      []RestoreCheckItem `json:"items"`
}
type drillImages struct {
	MySQL   string `json:"mysql"`
	Archive string `json:"archive"`
}

var digestImage = regexp.MustCompile(`^docker\.io/library/(mariadb|python)@sha256:[a-f0-9]{64}$`)

func (c *Controller) RestoreCheckStatus() ([]RestoreCheck, error) {
	root := filepath.Join(c.RecoveryRoot, "restore-checks")
	result := []RestoreCheck{}
	if err := SafeParents(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || !restoreTokenRe.MatchString(e.Name()) {
			continue
		}
		p := filepath.Join(root, e.Name(), "state.json")
		raw, err := readRegular(p)
		if err != nil || len(raw) > 1024*1024 {
			return nil, errors.New("invalid restore check state")
		}
		info, err := nearest(p)
		if err != nil || !privateRootOwned(info) {
			return nil, errors.New("unsafe restore check state")
		}
		var state RestoreCheck
		if json.Unmarshal(raw, &state) != nil || state.Token != e.Name() {
			return nil, errors.New("invalid restore check identity")
		}
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.After(result[j].StartedAt) })
	if len(result) > 50 {
		result = result[:50]
	}
	return result, nil
}

// Only the fixed service CLI calls this method. There is no user-controlled SQL
// endpoint and no production socket/port/environment is passed to containers.
func (c *Controller) RestoreCheckRun(ctx context.Context) (runErr error) {
	cfg, _, err := c.Active()
	if err != nil {
		return err
	}
	root := filepath.Join(c.RecoveryRoot, "restore-checks")
	if err = mkdirPrivate(root); err != nil {
		return err
	}
	lock, err := acquire(filepath.Join(root, ".worker.lock"), false, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(random[:])
	dir := filepath.Join(root, token)
	if err = mkdirPrivate(dir); err != nil {
		return err
	}
	state := RestoreCheck{Token: token, StartedAt: time.Now().UTC(), Status: "running", Items: []RestoreCheckItem{}}
	save := func() error {
		raw, _ := json.Marshal(state)
		return c.Write(filepath.Join(dir, "state.json"), raw, 0600)
	}
	if err = save(); err != nil {
		return err
	}
	defer func() {
		ended := time.Now().UTC()
		state.FinishedAt = &ended
		state.Status = "success"
		if runErr != nil {
			state.Status = "failed"
			state.Error = "Kiểm tra chưa đạt hoặc chưa hoàn tất; xem kết quả từng bản sao lưu và diagnostics riêng trên máy chủ"
		}
		for _, item := range state.Items {
			if item.Status != "success" {
				state.Status = "failed"
			}
		}
		if e := save(); e != nil {
			runErr = e
		}
	}()
	imagesRaw, err := readRegular("/root/backup-monitor/restore-check-images.json")
	if err != nil {
		return errors.New("restore images are not configured")
	}
	var images drillImages
	if json.Unmarshal(imagesRaw, &images) != nil || !strings.HasPrefix(images.MySQL, "docker.io/library/mariadb@") || !strings.HasPrefix(images.Archive, "docker.io/library/python@") || !digestImage.MatchString(images.MySQL) || !digestImage.MatchString(images.Archive) {
		return errors.New("restore images must use pinned official digests")
	}
	sources, err := drillSources(cfg.BackupRoot)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, item := range sources {
		seen[item.Kind] = true
	}
	for _, kind := range []string{"database", "site", "panel"} {
		if !seen[kind] {
			state.Items = append(state.Items, RestoreCheckItem{Kind: kind, Status: "failed", Detail: "Không tìm thấy bản sao lưu; chưa kiểm tra được"})
		}
	}
	for _, item := range sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		item.Status = "running"
		state.Items = append(state.Items, item)
		index := len(state.Items) - 1
		if err = save(); err != nil {
			return err
		}
		workspace, err := os.MkdirTemp(dir, "check-")
		if err != nil {
			return err
		}
		// This directory was created exclusively by this invocation and is never a
		// production path. Container writable data lives only in an anonymous tmpfs.
		e := func() error {
			defer os.RemoveAll(workspace)
			archive := filepath.Join(workspace, "input"+restoreExtension(item.Source))
			hash, e := snapshotDrillArchive(item.Source, archive)
			if e != nil {
				return e
			}
			state.Items[index].SHA256 = hash
			data := filepath.Join(workspace, "data")
			if e = mkdirPrivate(data); e != nil {
				return e
			}
			free, e := restoreFreeBytes(workspace)
			if e != nil || free < 1024*1024*1024 {
				return errors.New("insufficient drill disk margin")
			}
			files, expanded, e := extractRestore(archive, data, min(int64(free-512*1024*1024), int64(4*1024*1024*1024)))
			state.Items[index].Files = files
			state.Items[index].ExpandedBytes = expanded
			if e != nil {
				return e
			}
			if files == 0 {
				return errors.New("empty archive")
			}
			if item.Kind == "database" {
				sql := filepath.Join(data, "database.sql")
				if e = os.Chmod(sql, 0444); e != nil {
					return e
				}
				return runDrillContainer(ctx, images.MySQL, "999:999", sql, mysqlDrill, 768)
			}
			// Archive integrity only: never execute PHP, restore live vhosts, or start
			// aaPanel from extracted data. JSON structure is additionally validated.
			if e = filepath.WalkDir(data, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() {
					return os.Chmod(p, 0755)
				}
				return os.Chmod(p, 0444)
			}); e != nil {
				return e
			}
			validation := "CHECK_JSON=0\n" + archiveDrill
			if item.Kind == "panel" {
				validation = "CHECK_JSON=1\n" + archiveDrill
			}
			return runDrillContainer(ctx, images.Archive, "65534:65534", data, validation, 256)
		}()
		state.Items[index].Status = "success"
		state.Items[index].Detail = "Giải nén và kiểm tra cấu trúc archive; chưa kiểm tra ứng dụng chạy"
		if item.Kind == "database" {
			state.Items[index].Detail = "Đã phục hồi SQL, kiểm tra bảng và đọc số hàng trong container không có mạng"
		}
		if e != nil {
			state.Items[index].Status = "failed"
			state.Items[index].Detail = "Kiểm tra không đạt; xem diagnostics riêng trên máy chủ (không thay đổi dữ liệu đang chạy)"
			_ = c.Write(filepath.Join(dir, fmt.Sprintf("diagnostic-%d.txt", index)), []byte(e.Error()), 0600)
		}
		if err = save(); err != nil {
			return err
		}
	}
	for _, item := range state.Items {
		if item.Status == "failed" {
			return errors.New("one or more isolated restore checks failed")
		}
	}
	return nil
}
func drillSources(root string) ([]RestoreCheckItem, error) {
	if err := SafeParents(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	chosen := map[string]RestoreCheckItem{}
	days := map[string]string{}
	times := map[string]time.Time{}
	now := time.Now().Format("2006-01-02")
	count := 0
	for _, day := range entries {
		date, e := time.Parse("2006-01-02", day.Name())
		if e != nil || date.Format("2006-01-02") != day.Name() || day.Name() > now || !day.IsDir() || day.Type()&os.ModeSymlink != 0 {
			continue
		}
		err = filepath.WalkDir(filepath.Join(root, day.Name()), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			count++
			if count > 200000 {
				return errors.New("backup index exceeds drill budget")
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink in drill source")
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(root, p)
			if e != nil || !validRestorePath(filepath.ToSlash(rel)) {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			kind := parts[1]
			key := kind + ":" + filepath.Dir(strings.Join(parts[2:], "/"))
			if kind == "panel" {
				key = kind
			}
			info, e := d.Info()
			if e != nil || !info.Mode().IsRegular() {
				return errors.New("invalid backup source")
			}
			if day.Name() > days[key] || day.Name() == days[key] && (info.ModTime().After(times[key]) || info.ModTime() == times[key] && p > chosen[key].Source) {
				chosen[key] = RestoreCheckItem{Kind: kind, Source: p}
				days[key] = day.Name()
				times[key] = info.ModTime()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	result := []RestoreCheckItem{}
	for _, item := range chosen {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source < result[j].Source })
	if len(result) > 200 {
		return nil, errors.New("too many restore targets")
	}
	return result, nil
}
func snapshotDrillArchive(source, target string) (string, error) {
	if e := SafeParents(source); e != nil {
		return "", e
	}
	f, e := openRegular(source)
	if e != nil {
		return "", e
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || before.Size() <= 0 || before.Size() > 2*1024*1024*1024 {
		return "", errors.New("invalid archive size")
	}
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, before.Size()+1))
	closeErr := out.Close()
	after, statErr := f.Stat()
	if e != nil || closeErr != nil || statErr != nil || n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", errors.New("archive changed during snapshot")
	}
	if e = os.Chmod(target, 0444); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func runDrillContainer(ctx context.Context, image, user, input, script string, memory int) error {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := "backup-monitor-check-" + hex.EncodeToString(random[:])
	args := []string{"run", "--rm", "--name", name, "--pull=never", "--image-volume=ignore", "--network=none", "--read-only", "--cap-drop=all", "--security-opt=no-new-privileges", "--pids-limit=128", "--cpus=1", "--memory=" + fmt.Sprint(memory) + "m", "--memory-swap=" + fmt.Sprint(memory) + "m", "--user=" + user, "--tmpfs=/work:rw,nosuid,nodev,size=512m,mode=1777", "--volume=" + input + ":/input/archive:ro,Z", "--entrypoint=/bin/sh", image, "-c", script}
	command := exec.CommandContext(ctx, "/usr/bin/podman", args...)
	var output drillOutput
	command.Stdout = &output
	command.Stderr = &output
	// Killing the podman client does not necessarily remove its container.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "/usr/bin/podman", "rm", "--force", name).Run()
	}()
	if err := command.Run(); err != nil {
		return errors.New("isolated restore failed: " + string(output.data))
	}
	return nil
}

type drillOutput struct{ data []byte }

func (w *drillOutput) Write(p []byte) (int, error) {
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > 32768 {
		w.data = append([]byte(nil), w.data[len(w.data)-32768:]...)
	}
	return n, nil
}

const mysqlDrill = `set -eu
mariadb-install-db --no-defaults --auth-root-authentication-method=normal --datadir=/work/mysql > /work/init.log 2>&1
mariadbd --no-defaults --datadir=/work/mysql --socket=/work/mysql.sock --pid-file=/work/mysql.pid --skip-networking --innodb-buffer-pool-size=64M --innodb-log-buffer-size=8M --performance-schema=OFF --log-error=/work/mysql.log &
pid=$!;trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true' EXIT
ready=0;for i in $(seq 1 90); do if mysqladmin --no-defaults --socket=/work/mysql.sock -uroot ping >/dev/null 2>&1; then ready=1;break;fi;sleep 1;done;test "$ready" = 1
mysql --no-defaults --socket=/work/mysql.sock -uroot -e 'CREATE DATABASE checkdb'
mysql --no-defaults --socket=/work/mysql.sock -uroot checkdb < /input/archive
tables=$(mysql --no-defaults --socket=/work/mysql.sock -uroot -N -e "SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema')")
test "$tables" -gt 0
mysqlcheck --no-defaults --socket=/work/mysql.sock -uroot --all-databases --check >/work/checks
mysql --no-defaults --socket=/work/mysql.sock -uroot -N -e "SELECT CONCAT('SELECT COUNT(*) FROM ',CHAR(96),REPLACE(table_schema,CHAR(96),CONCAT(CHAR(96),CHAR(96))),CHAR(96),'.',CHAR(96),REPLACE(table_name,CHAR(96),CONCAT(CHAR(96),CHAR(96))),CHAR(96),';') FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','performance_schema','information_schema') AND table_type='BASE TABLE'" > /work/count.sql
mysql --no-defaults --socket=/work/mysql.sock -uroot < /work/count.sql >/work/counts
`
const archiveDrill = `set -eu
export CHECK_JSON
python - <<'PY'
import os, sqlite3, json, tempfile, shutil
root='/input/archive'
for directory, _, files in os.walk(root):
    for name in files:
        path=os.path.join(directory,name)
        with open(path,'rb') as f: header=f.read(16)
        if header == b'SQLite format 3\x00':
            # WAL/SHM recovery needs a writable PRIVATE copy, not the archived
            # read-only directory and never a live aaPanel database.
            with tempfile.TemporaryDirectory(dir='/work') as scratch:
                target=os.path.join(scratch,'check.sqlite')
                shutil.copyfile(path,target)
                for suffix in ('-wal','-shm','-journal'):
                    if os.path.isfile(path+suffix): shutil.copyfile(path+suffix,target+suffix)
                db=sqlite3.connect(target)
                try:
                    result=db.execute('PRAGMA integrity_check').fetchall()
                    if result != [('ok',)]: raise RuntimeError('SQLite integrity check failed: '+name)
                finally: db.close()
        if os.environ.get('CHECK_JSON')=='1' and name.lower().endswith('.json') and os.path.getsize(path)>0:
            if os.path.getsize(path)>32*1024*1024: raise RuntimeError('JSON too large')
            with open(path,'r',encoding='utf-8-sig') as f: json.load(f)
print('Archive integrity checked; application execution not tested')
PY
`
