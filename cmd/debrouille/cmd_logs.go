package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func (a *app) cmdLogs(args []string) error {
	tail := 40
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail", "-n":
			if i+1 >= len(args) {
				return errors.New("--tail needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid --tail %q", args[i+1])
			}
			tail = n
			i++
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 1 {
		return errors.New("usage: debrouille logs <job> [--tail N]")
	}
	name := positional[0]

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Job(name); !ok {
		return fmt.Errorf("unknown job %q", name)
	}

	path := filepath.Join(cfg.Settings.LogDir, name+".log")
	lines, err := tailLines(path, tail)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no log for %q yet (it has never run)", name)
	}
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(os.Stdout, line)
	}
	return nil
}
