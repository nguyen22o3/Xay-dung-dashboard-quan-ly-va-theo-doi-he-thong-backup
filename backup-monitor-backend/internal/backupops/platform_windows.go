//go:build windows

// Windows support is only for local unit tests. The production CLI refuses to
// run outside Linux; root ownership/device identities use their Linux checks.
package backupops

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func privateRootOwned(info os.FileInfo) bool { return true } // Only isolated Windows tests; CLI refuses Windows.
func device(info os.FileInfo) uint64         { return 1 }
func inode(info os.FileInfo) uint64          { return 0 }
func openRegular(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("Tệp không phải tệp thường")
	}
	return f, nil
}
func acquire(path string, shared, nonblock bool) (*os.File, error) {
	if err := SafeParents(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	var mode uint32
	if !shared {
		mode |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if nonblock {
		mode |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), mode, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		f.Close()
		return nil, errors.New("Có tác vụ đang chạy")
	}
	return f, nil
}
func restartMonitor(old, new Settings) ([]string, error) { return []string{}, nil }
