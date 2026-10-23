package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnsoberDriver/debrouille/internal/conditions"
	"github.com/UnsoberDriver/debrouille/internal/config"
	"github.com/UnsoberDriver/debrouille/internal/human"
	"github.com/UnsoberDriver/debrouille/internal/lock"
	"github.com/UnsoberDriver/debrouille/internal/runner"
	"github.com/UnsoberDriver/debrouille/internal/state"
)

func (a *app) cmdRun(args []string) error {
	force := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			force = true
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		return errors.New("usage: debrouille run <job> [--force]")
	}
	name := positional[0]

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	job, ok := cfg.Job(name)
	if !ok {
		return fmt.Errorf("unknown job %q (known: %s)", name, strings.Join(jobNames(cfg), ", "))
	}
	spec, err := job.Spec()
	if err != nil {
		return err
	}
	store, err := a.openStore(cfg)
	if err != nil {
		return err
	}

	if !force {
		req := conditions.Requirements{
		RequireAC:      job.NeedsAC(cfg),
		MinBattery:     cfg.Settings.MinBattery,
		RequireIdle:    job.NeedsIdle(cfg),
		IdleFor:        cfg.Settings.IdleFor.Or(5 * time.Minute),
		IdleCheck:      cfg.Settings.IdleCheck,
		RequireNetwork: job.NeedsNetwork(cfg),
		NetworkCheck:   cfg.Settings.NetworkCheck,
	}
	report := conditions.Evaluate(req, "/sys")
		if !report.OK {
			fmt.Fprintf(a.ui.Out, "%s  %s\n", a.ui.Yellow("waiting"), strings.Join(report.Reasons, "; "))
			fmt.Fprintf(a.ui.Out, "%s\n", a.ui.Dim("use --force to run anyway"))
			return nil
		}
	}

	lk, err := lock.Acquire(filepath.Join(cfg.Settings.StateDir, name+".lock"))
	if errors.Is(err, lock.ErrBusy) {
		return fmt.Errorf("job %q is already running", name)
	}
	if err != nil {
		return err
	}
	defer lk.Release()

	timeout := job.EffectiveTimeout(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fmt.Fprintf(a.ui.Out, "%s %s %s\n", a.ui.Bold("running"), name, a.ui.Dim("("+human.Duration(timeout)+" timeout)"))
	res, runErr := runner.Run(ctx, name, job.Command, runner.Options{
		LogDir:     cfg.Settings.LogDir,
		Shell:      cfg.Settings.Shell,
		Timeout:    timeout,
		WorkingDir: job.WorkingDir,
		Env:        job.Env,
		Stream:     a.ui.Out,
	})

	finished := time.Now()
	var prev state.JobState
	store.Update(name, func(js *state.JobState) { prev = *js })

	base := prev.NextDue
	if base.IsZero() {
		base = finished
	}
	next := spec.Advance(base, finished)

	success := runErr == nil && res.ExitCode == 0
	store.Update(name, func(js *state.JobState) {
		js.NextDue = next
		js.LastStart = res.Started
		js.LastRun = finished
		js.LastExit = res.ExitCode
		js.LastDurationMS = res.Duration.Milliseconds()
		js.Runs++
		if success {
			js.LastResult = state.ResultSuccess
			js.ConsecutiveFails = 0
		} else {
			js.LastResult = state.ResultFailed
			js.ConsecutiveFails++
		}
	})
	if err := store.Save(); err != nil {
		return err
	}

	if runErr != nil {
		return runErr
	}
	if !success {
		fmt.Fprintf(a.ui.Err, "%s exit %d after %s, log: %s\n",
			a.ui.Red("failed:"), res.ExitCode, human.Duration(res.Duration), res.LogPath)
		return &jobError{name: name, code: res.ExitCode}
	}

	fmt.Fprintf(a.ui.Out, "%s %s in %s, next %s\n",
		a.ui.Green("ok"), name, human.Duration(res.Duration), next.Format("Mon 02 Jan 15:04"))
	return nil
}

func jobNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Jobs))
	for i := range cfg.Jobs {
		names = append(names, cfg.Jobs[i].Name)
	}
	return names
}
