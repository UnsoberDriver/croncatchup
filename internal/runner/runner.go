// Package runner executes a job command and captures its output.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLogBytes is the size at which a job log is rotated to <name>.log.old.
const maxLogBytes = 5 << 20

// Options configures one execution.
type Options struct {
	LogDir     string
	Shell      string
	Timeout    time.Duration
	WorkingDir string
	Env        map[string]string
	// Stream, when non-nil, receives the output as well as the log file.
	Stream io.Writer
}

// Result describes a finished execution. A non-zero ExitCode is not an error:
// it is a job that failed, which is information, not a malfunction.
type Result struct {
	ExitCode int
	Started  time.Time
	Duration time.Duration
	TimedOut bool
	LogPath  string
}

// Run executes command through the shell and returns what happened. The error
// return is reserved for infrastructure problems (no log file, no shell); a
// command that exits non-zero is reported through Result.
func Run(ctx context.Context, name, command string, opts Options) (Result, error) {
	shell := opts.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	logPath, err := openLog(opts.LogDir, name)
	if err != nil {
		return Result{}, err
	}
	defer logPath.Close()

	header := fmt.Sprintf("\n=== %s | %s ===\n$ %s\n",
		time.Now().Format("2006-01-02 15:04:05"), name, command)
	if _, err := io.WriteString(logPath, header); err != nil {
		return Result{}, fmt.Errorf("write log header: %w", err)
	}

	var out io.Writer = logPath
	if opts.Stream != nil {
		out = io.MultiWriter(logPath, opts.Stream)
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = opts.WorkingDir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = mergeEnv(os.Environ(), opts.Env)

	// Let the context kill the whole process group (a shell that spawned
	// tar, restic, rsync...) rather than orphaning its children.
	configure(cmd)
	cmd.Cancel = func() error { return kill(cmd) }
	cmd.WaitDelay = 5 * time.Second

	res := Result{LogPath: logPath.Name(), Started: time.Now()}
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("start %q: %w", command, err)
	}

	err = cmd.Wait()
	res.Duration = time.Since(res.Started)

	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.ExitCode = 124 // same convention as timeout(1)
	case errors.Is(ctx.Err(), context.Canceled):
		res.TimedOut = true
		res.ExitCode = 130
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			if res.ExitCode < 0 {
				res.ExitCode = 137 // killed by a signal
			}
		} else {
			res.ExitCode = 1
		}
	}

	footer := fmt.Sprintf("--- exit %d in %s", res.ExitCode, res.Duration.Round(time.Millisecond))
	if res.TimedOut {
		footer += " (timed out)"
	}
	footer += " ---\n"
	_, _ = io.WriteString(logPath, footer)

	return res, nil
}

func openLog(dir, name string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	path := filepath.Join(dir, name+".log")

	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".old")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return f, nil
}

// mergeEnv adds overrides to base without duplicating keys.
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	env := make(map[string]string, len(base)+len(overrides))
	for _, kv := range base {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range overrides {
		env[k] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
