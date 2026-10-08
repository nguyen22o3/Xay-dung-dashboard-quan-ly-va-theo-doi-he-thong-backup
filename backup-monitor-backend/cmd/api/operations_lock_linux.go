//go:build linux

package main

import (
	"backup-monitor-backend/internal/backupops"
	"errors"
	"os"
	"syscall"
)

func lockOperations(path string) (*os.File, error) {
	if err := backupops.SafeParents(path); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("unsafe operations lock")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("operations state already in use")
	}
	return f, nil
}
