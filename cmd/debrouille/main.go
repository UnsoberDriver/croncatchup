// Command debrouille is a catch-up scheduler for machines that sleep.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnsoberDriver/debrouille/internal/config"
	"github.com/UnsoberDriver/debrouille/internal/state"
	"github.com/UnsoberDriver/debrouille/internal/ui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

type app struct {
	cfgPath string
	json    bool
	noColor bool
	verbose bool
	ui      *ui.Printer
}

// jobError carries the exit code of a failed job so scripts can react to it.
type jobError struct {
	name string
	code int
}

func (e *jobError) Error() string { return fmt.Sprintf("job %q failed with exit code %d", e.name, e.code) }
func (e *jobError) ExitCode() int { return e.code }

func main() {
	a := &app{ui: ui.New()}
	err := a.dispatch(os.Args[1:])
	if err == nil {
		return
	}
	if errors.Is(err, flagHelp) {
		a.usage()
		return
	}
	a.ui.Errf("%v", err)

	var je *jobError
	if errors.As(err, &je) {
		os.Exit(je.ExitCode())
	}
	os.Exit(1)
}

var flagHelp = errors.New("help requested")

func (a *app) dispatch(args []string) error {
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 >= len(args) {
				return errors.New("--config needs a path")
			}
			a.cfgPath = args[i+1]
			i++
		case "--json":
			a.json = true
		case "--no-color":
			a.noColor = true
		case "--verbose", "-v":
			a.verbose = true
		case "-h", "--help":
			return flagHelp
		case "--version":
			fmt.Println("debrouille " + version)
			return nil
		default:
			rest = append(rest, args[i:]...)
			i = len(args)
		}
	}

	if a.noColor {
		a.ui = ui.Plain(os.Stdout, os.Stderr)
	}
	if len(rest) == 0 {
		return flagHelp
	}

	cmd, argv := rest[0], rest[1:]
	switch cmd {
	case "status":
		return a.cmdStatus(argv)
	case "run":
		return a.cmdRun(argv)
	case "check":
		return a.cmdCheck(argv)
	case "daemon":
		return a.cmdDaemon(argv)
	case "logs":
		return a.cmdLogs(argv)
	case "systemd":
		return a.cmdSystemd(argv)
	case "version":
		fmt.Println("debrouille " + version)
		return nil
	case "help":
		return flagHelp
	default:
		return fmt.Errorf("unknown command %q (try `debrouille help`)", cmd)
	}
}

func (a *app) usage() {
	out := a.ui.Out
	fmt.Fprintf(out, `%s — a catch-up scheduler for machines that sleep

Usage:
  debrouille [global flags] <command> [args]

Commands:
  daemon [--once]         run the scheduler loop (--once: evaluate jobs once)
  status                  show every job, its last run and its next one
  run <job> [--force]     run a job now, bypassing the schedule
  check [--explain]       validate the config and show the conditions
  logs <job> [--tail N]   show the job's log (default: last 40 lines)
  systemd                 print a systemd user unit for the daemon
  version                 print the version

Global flags:
  -c, --config <path>     config file (default: %s)
      --json              machine-readable output where supported
      --no-color          disable colours
  -v, --verbose           more detail
  -h, --help              this text

The point of debrouille is catch_up: a job that was due while the laptop was
asleep still runs when it wakes up — unless it is older than its grace window,
in which case it is skipped instead of firing five backups at once.
`, a.ui.Bold("debrouille"), config.DefaultPath())
}

func (a *app) configPath() string {
	if a.cfgPath != "" {
		return config.ExpandPath(a.cfgPath)
	}
	return config.DefaultPath()
}

func (a *app) loadConfig() (*config.Config, error) {
	path := a.configPath()
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (a *app) openStore(cfg *config.Config) (*state.Store, error) {
	if err := os.MkdirAll(cfg.Settings.StateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Settings.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return state.Load(filepath.Join(cfg.Settings.StateDir, "state.json"))
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// colorState paints the STATUS column without breaking the table alignment.
func (a *app) colorState(s string) string {
	switch {
	case strings.HasPrefix(s, "failing"), s == "missed":
		return a.ui.Red(s)
	case s == "due":
		return a.ui.Yellow(s)
	case s == "ok":
		return a.ui.Green(s)
	case s == "new":
		return a.ui.Cyan(s)
	}
	return s
}

// nextRun finds the soonest upcoming job, for the status footer.
func nextRun(cfg *config.Config, snap map[string]state.JobState, now time.Time) (string, time.Time) {
	var name string
	var when time.Time
	for i := range cfg.Jobs {
		js := snap[cfg.Jobs[i].Name]
		if js.NextDue.IsZero() || js.NextDue.Before(now) {
			continue
		}
		if when.IsZero() || js.NextDue.Before(when) {
			name, when = cfg.Jobs[i].Name, js.NextDue
		}
	}
	return name, when
}

func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, 5<<20))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
