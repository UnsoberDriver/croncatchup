//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// configure puts the child in its own process group so that a timeout can take
// down the whole tree, not just the shell.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// kill terminates the child's entire process group.
func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
