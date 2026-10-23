// Package view turns state into the rows both `debrouille status` and the
// daemon banner display. Keeping it in one place means the two can never drift.
package view

import (
	"fmt"
	"time"

	"github.com/UnsoberDriver/croncatchup/internal/config"
	"github.com/UnsoberDriver/croncatchup/internal/human"
	"github.com/UnsoberDriver/croncatchup/internal/state"
)

// Headers are the status table columns.
var Headers = []string{"JOB", "SCHEDULE", "LAST RUN", "NEXT DUE", "STATE"}

// Rows builds the status table.
func Rows(cfg *config.Config, snap map[string]state.JobState, now time.Time) [][]string {
	rows := make([][]string, 0, len(cfg.Jobs))
	for i := range cfg.Jobs {
		job := &cfg.Jobs[i]
		spec, err := job.Spec()
		scheduleText := "?"
		if err == nil {
			scheduleText = spec.Human()
		}

		js := snap[job.Name]
		rows = append(rows, []string{
			job.Name,
			scheduleText,
			lastRun(js, now),
			nextDue(js.NextDue, now),
			Status(js, now, job.EffectiveGrace(cfg), job.CatchUpEnabled()),
		})
	}
	return rows
}

func lastRun(js state.JobState, now time.Time) string {
	if js.LastRun.IsZero() {
		return "never"
	}
	ago := human.Ago(js.LastRun, now)
	switch js.LastResult {
	case state.ResultSuccess:
		return ago + " ok"
	case state.ResultFailed:
		return ago + " failed"
	case state.ResultSkipped:
		return ago + " skipped"
	}
	return ago
}

func nextDue(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	thatDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	switch int(thatDay.Sub(today).Hours() / 24) {
	case 0:
		return "today " + t.Format("15:04")
	case 1:
		return "tomorrow " + t.Format("15:04")
	case -1:
		return "yesterday " + t.Format("15:04")
	}
	return t.Format("Mon 02 Jan 15:04")
}

// Status returns the one-word state of a job.
func Status(js state.JobState, now time.Time, grace time.Duration, catchUp bool) string {
	if js.NextDue.IsZero() {
		return "new"
	}

	label := "ok"
	if now.After(js.NextDue) {
		late := now.Sub(js.NextDue)
		allowed := grace
		if !catchUp {
			allowed = 0
		}
		if late > allowed {
			label = "missed"
		} else {
			label = "due"
		}
	}

	if js.LastResult == state.ResultFailed {
		if js.ConsecutiveFails > 1 {
			return fmt.Sprintf("failing (%dx)", js.ConsecutiveFails)
		}
		return "failing"
	}
	return label
}
