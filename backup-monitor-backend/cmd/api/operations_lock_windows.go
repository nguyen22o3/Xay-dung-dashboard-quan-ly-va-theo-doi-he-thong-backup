//go:build windows

package main

import (
	"backup-monitor-backend/internal/backupops"
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func lockOperations(path string) (*os.File, error) {
	if err := backupops.SafeParents(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("unsafe operations lock")
	}
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		f.Close()
		return nil, errors.New("operations state already in use")
	}
	return f, nil
}
