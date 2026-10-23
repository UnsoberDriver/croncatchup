//go:build windows

package runner

import "os/exec"

// configure is a no-op on Windows: there is no process group here.
func configure(cmd *exec.Cmd) {}

// kill terminates the child process.
func kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
