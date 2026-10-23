//go:build unix

// Package lock is a small advisory file lock. It exists so that a manual
// `debrouille run backup` cannot start while the daemon is already running the
// same backup.
package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrBusy means somebody else holds the lock.
var ErrBusy = errors.New("already running")

// Lock is a held advisory lock.
type Lock struct {
	file *os.File
}

// Acquire takes an exclusive, non-blocking lock. It returns ErrBusy if another
// process holds it.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{file: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	l.file = nil
	return err
}
