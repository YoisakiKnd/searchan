//go:build !windows

package task

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("task directory is already in use by another process")
	}
	return f, nil
}
