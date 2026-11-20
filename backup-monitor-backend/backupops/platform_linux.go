//go:build linux

package backupops

import (
	"errors"
	"fmt"
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
func scriptProcessRunning(path string) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		args := strings.Split(string(raw), "\x00")
		// Includes direct Bash runs, but does not guess from log timestamps.
		if len(args) >= 2 && (filepath.Base(args[0]) == "bash" || filepath.Base(args[0]) == "sh") && args[1] == path {
			return true, nil
		}
	}
	return false, nil
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

func validateTaskProcess(s TaskState, wrapper string) (bool, error) {
	root := "/proc/" + strconv.Itoa(s.PID)
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !privateRootOwned(info) {
		return false, errors.New("Không xác minh được chủ tiến trình")
	}
	raw, err := os.ReadFile(root + "/stat")
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	index := strings.LastIndex(string(raw), ") ")
	if index < 0 {
		return false, errors.New("Trạng thái tiến trình không hợp lệ")
	}
	fields := strings.Fields(string(raw)[index+2:])
	if len(fields) < 20 || fields[19] != s.StartTicks || fields[2] != strconv.Itoa(s.PID) || fields[3] != strconv.Itoa(s.PID) {
		return false, nil
	}
	raw, err = os.ReadFile(root + "/cmdline")
	if err != nil {
		return false, err
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args) < 2 || filepath.Base(args[0]) != "bash" {
		return false, nil
	}
	scheduled := len(args) >= 3 && args[1] == wrapper && args[2] == s.JobID
	manual := len(args) == 3 && args[1] == "-c" && strings.HasPrefix(args[2], fmt.Sprintf("SCRIPT=%q\nTASK_ID=%q\n", s.Script, s.JobID))
	if !scheduled && !manual {
		return false, nil
	}
	return true, nil
}
func cancelTaskProcess(s TaskState, wrapper string) error {
	// Recheck identity immediately before signalling a dedicated process group;
	// never pkill by script name or accept a PID from an HTTP request.
	valid, err := validateTaskProcess(s, wrapper)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("Tiến trình đã thay đổi; không gửi tín hiệu")
	}
	return syscall.Kill(-s.PID, syscall.SIGTERM)
}
func restoreFreeBytes(path string) (uint64, error) {
	var info syscall.Statfs_t
	err := syscall.Statfs(path, &info)
	return info.Bavail * uint64(info.Bsize), err
}
