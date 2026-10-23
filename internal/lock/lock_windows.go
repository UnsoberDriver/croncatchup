//go:build windows

package lock

import (
	"errors"
	"os"
)

// ErrBusy means somebody else holds the lock.
var ErrBusy = errors.New("already running")

// Lock is a placeholder on Windows, where debrouille is not yet supported as a
// daemon. It always succeeds so that the rest of the program still builds.
type Lock struct {
	file *os.File
}

// Acquire is a no-op on Windows.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Release closes the file.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
