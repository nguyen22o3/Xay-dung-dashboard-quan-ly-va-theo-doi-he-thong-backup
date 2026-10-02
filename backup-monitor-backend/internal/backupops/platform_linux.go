//go:build linux

package backupops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func privateRootOwned(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == 0 && info.Mode().Perm()&0022 == 0
}
func device(info os.FileInfo) uint64 { return uint64(info.Sys().(*syscall.Stat_t).Dev) }
func inode(info os.FileInfo) uint64  { return info.Sys().(*syscall.Stat_t).Ino }
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("Tệp không phải tệp thường: " + path)
	}
	return f, nil
}
func acquire(path string, shared, nonblock bool) (*os.File, error) {
	if err := SafeParents(path); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	mode := syscall.LOCK_EX
	if shared {
		mode = syscall.LOCK_SH
	}
	if nonblock {
		mode |= syscall.LOCK_NB
	}
	if err = syscall.Flock(fd, mode); err != nil {
		f.Close()
		return nil, errors.New("Có tác vụ đang chạy; hãy thử lại khi tác vụ kết thúc")
	}
	return f, nil
}
func restartMonitor(old, new Settings) ([]string, error) {
	warnings := []string{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return warnings, err
	}
	path := filepath.Join(old.ScriptsDir, "realtime_monitor.sh")
	found := false
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(string(raw), "\x00")
		if len(args) < 2 || filepath.Base(args[0]) != "bash" || args[1] != path {
			continue
		}
		found = true
		_ = exec.Command("pkill", "-TERM", "-P", strconv.Itoa(pid)).Run()
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	if !found {
		return warnings, nil
	}
	for i := 0; i < 10; i++ {
		lock, err := acquire("/run/backup-monitor-inotify.lock", false, true)
		if err == nil {
			lock.Close()
			cmd := exec.Command("/bin/bash", filepath.Join(new.ScriptsDir, "realtime_monitor.sh"))
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err = cmd.Start(); err != nil {
				return warnings, err
			}
			_ = cmd.Process.Release()
			return warnings, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return []string{"Trình giám sát cũ chưa nhả khóa; cần kiểm tra lại dịch vụ giám sát"}, nil
}
