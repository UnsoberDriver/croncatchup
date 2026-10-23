// Package daemon is the long-running loop: every tick it asks each job "are you
// due, are the conditions met, is anyone else running you?" and acts.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/UnsoberDriver/croncatchup/internal/conditions"
	"github.com/UnsoberDriver/croncatchup/internal/config"
	"github.com/UnsoberDriver/croncatchup/internal/human"
	"github.com/UnsoberDriver/croncatchup/internal/lock"
	"github.com/UnsoberDriver/croncatchup/internal/runner"
	"github.com/UnsoberDriver/croncatchup/internal/schedule"
	"github.com/UnsoberDriver/croncatchup/internal/state"
	"github.com/UnsoberDriver/croncatchup/internal/ui"
	"github.com/UnsoberDriver/croncatchup/internal/view"
)

// Daemon owns the schedule and the state file.
type Daemon struct {
	cfg     *config.Config
	store   *state.Store
	ui      *ui.Printer
	specs   map[string]schedule.Spec
	sysRoot string
	stream  io.Writer
	now     func() time.Time
	blocked map[string]string
}

// New prepares a daemon, creating initial due dates for jobs it has never seen.
func New(cfg *config.Config, store *state.Store, printer *ui.Printer, stream io.Writer) (*Daemon, error) {
	d := &Daemon{
		cfg:     cfg,
		store:   store,
		ui:      printer,
		sysRoot: "/sys",
		stream:  stream,
		now:     time.Now,
		blocked: map[string]string{},
	}
	if err := d.rebuild(); err != nil {
		return nil, err
	}
	return d, nil
}

// SetSystemRoot points condition checks at a fake /sys, for tests.
func (d *Daemon) SetSystemRoot(root string) { d.sysRoot = root }

// SetClock replaces the clock, for tests.
func (d *Daemon) SetClock(now func() time.Time) { d.now = now }

// rebuild recomputes schedule specs and gives new jobs a starting due date.
func (d *Daemon) rebuild() error {
	specs := make(map[string]schedule.Spec, len(d.cfg.Jobs))
	keep := make(map[string]bool, len(d.cfg.Jobs))

	for i := range d.cfg.Jobs {
		job := &d.cfg.Jobs[i]
		spec, err := job.Spec()
		if err != nil {
			return err
		}
		specs[job.Name] = spec
		keep[job.Name] = true
		d.initialise(job, spec)
	}

	d.specs = specs
	d.store.Prune(keep)
	return nil
}

func (d *Daemon) initialise(job *config.Job, spec schedule.Spec) {
	now := d.now()
	d.store.Update(job.Name, func(js *state.JobState) {
		if !js.NextDue.IsZero() {
			return
		}
		if d.cfg.RunMissed() {
			// Due immediately: the daemon may have been off for days, and
			// that is exactly the case debrouille exists for.
			js.NextDue = now
			return
		}
		js.NextDue = spec.Next(now)
	})
}

// Run loops until ctx is cancelled. A new config on reload replaces the running
// one without restarting the process.
func (d *Daemon) Run(ctx context.Context, reload <-chan *config.Config) error {
	tick := d.cfg.Tick()
	fmt.Fprintf(d.ui.Out, "%s  watching %d job(s), tick %s\n\n",
		d.ui.Dim(d.now().Format("15:04:05")), len(d.cfg.Jobs), human.Duration(tick))
	d.printStatus()

	if err := d.Tick(ctx); err != nil {
		d.ui.Errf("%v", err)
	}

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(d.ui.Out, "\n%s  stopped\n", d.ui.Dim(d.now().Format("15:04:05")))
			return nil

		case cfg := <-reload:
			if cfg == nil {
				continue
			}
			d.cfg = cfg
			if err := d.rebuild(); err != nil {
				d.ui.Errf("reload: %v", err)
				continue
			}
			if newTick := cfg.Tick(); newTick != tick {
				tick = newTick
				ticker.Reset(tick)
			}
			fmt.Fprintf(d.ui.Out, "%s  config reloaded, %d job(s)\n",
				d.ui.Dim(d.now().Format("15:04:05")), len(cfg.Jobs))

		case <-ticker.C:
			if err := d.Tick(ctx); err != nil {
				d.ui.Errf("%v", err)
			}
		}
	}
}

func (d *Daemon) printStatus() {
	snap := d.store.Snapshot()
	d.ui.Table(view.Headers, view.Rows(d.cfg, snap, d.now()))
	fmt.Fprintln(d.ui.Out)
}

// Tick evaluates every job once and persists the result.
func (d *Daemon) Tick(ctx context.Context) error {
	if err := d.store.Reload(); err != nil {
		d.ui.Warnf("%v", err)
	}
	for i := range d.cfg.Jobs {
		job := &d.cfg.Jobs[i]
		if _, ok := d.specs[job.Name]; !ok {
			spec, err := job.Spec()
			if err != nil {
				d.ui.Errf("%s: %v", job.Name, err)
				continue
			}
			d.specs[job.Name] = spec
		}
		d.initialise(job, d.specs[job.Name])
		if err := d.evaluate(ctx, job); err != nil {
			d.ui.Errf("%s: %v", job.Name, err)
		}
	}
	return d.store.Save()
}

func (d *Daemon) evaluate(ctx context.Context, job *config.Job) error {
	spec := d.specs[job.Name]
	now := d.now()

	var st state.JobState
	d.store.Update(job.Name, func(js *state.JobState) {
		if js.NextDue.IsZero() {
			js.NextDue = now
		}
		st = *js
	})

	grace := job.EffectiveGrace(d.cfg)
	dec := schedule.Decide(st.NextDue, now, grace, d.cfg.Tick(), job.CatchUpEnabled())
	if !dec.Due {
		return nil
	}

	// Too late to be worth doing: drop this occurrence and move on.
	if dec.Skip {
		next := spec.Advance(st.NextDue, now)
		d.store.Update(job.Name, func(js *state.JobState) {
			js.NextDue = next
			js.Skips++
			js.LastResult = state.ResultSkipped
		})
		d.ui.Event(now, "skip", job.Name, dec.Reason)
		return nil
	}

	req := conditions.Requirements{
		RequireAC:      job.NeedsAC(d.cfg),
		MinBattery:     d.cfg.Settings.MinBattery,
		RequireIdle:    job.NeedsIdle(d.cfg),
		IdleFor:        d.cfg.Settings.IdleFor.Or(5 * time.Minute),
		IdleCheck:      d.cfg.Settings.IdleCheck,
		RequireNetwork: job.NeedsNetwork(d.cfg),
		NetworkCheck:   d.cfg.Settings.NetworkCheck,
	}
	report := conditions.Evaluate(req, d.sysRoot)
	if !report.OK {
		// Keep NextDue where it is: the job stays due, and will run as soon
		// as the machine is plugged in / idle / online, until grace expires.
		reason := strings.Join(report.Reasons, "; ")
		if d.blocked[job.Name] != reason {
			d.blocked[job.Name] = reason
			d.ui.Event(now, "wait", job.Name, reason)
		}
		return nil
	}
	delete(d.blocked, job.Name)

	lk, err := lock.Acquire(filepath.Join(d.cfg.Settings.StateDir, job.Name+".lock"))
	if errors.Is(err, lock.ErrBusy) {
		d.ui.Event(now, "wait", job.Name, "already running")
		return nil
	}
	if err != nil {
		return err
	}
	defer lk.Release()

	timeout := job.EffectiveTimeout(d.cfg)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	d.ui.Event(now, "run", job.Name, dec.Reason)
	res, runErr := runner.Run(runCtx, job.Name, job.Command, runner.Options{
		LogDir:     d.cfg.Settings.LogDir,
		Shell:      d.cfg.Settings.Shell,
		Timeout:    timeout,
		WorkingDir: job.WorkingDir,
		Env:        job.Env,
		Stream:     d.stream,
	})

	finished := d.now()
	next := spec.Advance(st.NextDue, finished)
	success := runErr == nil && res.ExitCode == 0

	d.store.Update(job.Name, func(js *state.JobState) {
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
			if runErr == nil {
				js.LastExit = res.ExitCode
			}
		}
	})

	switch {
	case runErr != nil:
		d.ui.Event(finished, "fail", job.Name, runErr.Error())
		return runErr
	case success:
		d.ui.Event(finished, "ok", job.Name,
			fmt.Sprintf("done in %s, log: %s", human.Duration(res.Duration), res.LogPath))
	case res.TimedOut:
		d.ui.Event(finished, "fail", job.Name, fmt.Sprintf("timed out after %s", human.Duration(timeout)))
	default:
		d.ui.Event(finished, "fail", job.Name,
			fmt.Sprintf("exit %d after %s, log: %s", res.ExitCode, human.Duration(res.Duration), res.LogPath))
	}
	return nil
}
