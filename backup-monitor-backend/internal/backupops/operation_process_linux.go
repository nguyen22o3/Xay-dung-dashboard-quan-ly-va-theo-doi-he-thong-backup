//go:build linux

package backupops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

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
