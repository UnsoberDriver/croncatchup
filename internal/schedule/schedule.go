// Package schedule decides when a job is next due and whether a missed run
// should still happen.
//
// The scheduling model is intentionally smaller than cron. A debrouille config
// is meant to be readable six months later, by someone who has not memorised
// the five fields of a crontab:
//
//	every   = "6h"                  -> every 6 hours
//	schedule = "daily", at = "03:00"
//	schedule = "weekly", day = "sunday", at = "04:30"
//
// The interesting part is not Next, it is Decide: on a laptop, the machine is
// usually asleep when a job is due, so what matters is how late a run may be
// before it is no longer worth doing.
package schedule

import (
	"fmt"
	"time"

	"github.com/UnsoberDriver/croncatchup/internal/human"
)

// Kind is the shape of a recurrence.
type Kind string

const (
	// Interval recurs a fixed duration after the previous occurrence.
	Interval Kind = "interval"
	// Daily recurs once a day at a wall-clock time.
	Daily Kind = "daily"
	// Weekly recurs once a week on a weekday at a wall-clock time.
	Weekly Kind = "weekly"
)

// Spec is a resolved recurrence, ready to compute dates from.
type Spec struct {
	Kind  Kind
	Every time.Duration // Interval only
	At    time.Duration // offset from midnight, Daily and Weekly
	Day   time.Weekday  // Weekly only
}

// Human renders the spec the way the config file spells it.
func (s Spec) Human() string {
	switch s.Kind {
	case Interval:
		return "every " + human.Duration(s.Every)
	case Daily:
		return "daily @ " + clock(s.At)
	case Weekly:
		return "weekly on " + s.Day.String() + " @ " + clock(s.At)
	}
	return string(s.Kind)
}

func clock(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d/time.Hour), int((d%time.Hour)/time.Minute))
}

// Next returns the first occurrence strictly after t.
func (s Spec) Next(t time.Time) time.Time {
	switch s.Kind {
	case Interval:
		if s.Every <= 0 {
			return t.Add(24 * time.Hour)
		}
		return t.Add(s.Every)
	case Daily, Weekly:
		// Walk forward day by day, building the candidate from wall-clock
		// components so that a DST jump does not silently move a 03:00 job
		// to 02:00 or 04:00.
		for i := 0; i < 8; i++ {
			d := t.AddDate(0, 0, i)
			c := time.Date(d.Year(), d.Month(), d.Day(),
				int(s.At/time.Hour), int((s.At%time.Hour)/time.Minute), 0, 0, t.Location())
			if !c.After(t) {
				continue
			}
			if s.Kind == Weekly && c.Weekday() != s.Day {
				continue
			}
			return c
		}
	case "":
		// Unset spec: behave as a daily job so callers never loop forever.
		return Spec{Kind: Daily}.Next(t)
	}
	return t.AddDate(0, 0, 7)
}

// Advance returns the first occurrence strictly after now, given that from was
// the previous occurrence. This is what stops a two-week holiday from queuing
// fourteen nightly backups the moment the laptop wakes up: the interval case is
// computed in one shot instead of iterating once per missed run.
func (s Spec) Advance(from, now time.Time) time.Time {
	if from.After(now) {
		return from
	}
	if s.Kind == Interval && s.Every > 0 {
		steps := now.Sub(from)/s.Every + 1
		return from.Add(steps * s.Every)
	}
	next := s.Next(from)
	for !next.After(now) {
		next = s.Next(next)
	}
	return next
}

// Decision is the verdict for one job at one instant.
type Decision struct {
	Due    bool
	Run    bool
	Skip   bool
	Late   time.Duration
	Reason string
}

// Decide answers the only question the daemon really has: should this job run
// right now?
//
//	nextDue    the occurrence we are considering
//	now        the current time
//	grace      how late a run may be and still be worth doing
//	tolerance  jitter we refuse to call "late" (normally one tick)
//	catchUp    when false, a run that missed its slot is dropped, not deferred
//
// A zero nextDue means "never scheduled", i.e. a first run.
func Decide(nextDue, now time.Time, grace, tolerance time.Duration, catchUp bool) Decision {
	if nextDue.IsZero() {
		return Decision{Due: true, Run: true, Reason: "first run"}
	}
	if now.Before(nextDue) {
		return Decision{}
	}

	late := now.Sub(nextDue)
	d := Decision{Due: true, Late: late}

	allowed := tolerance
	if catchUp {
		allowed = max(grace, tolerance)
	}

	if late > allowed {
		d.Skip = true
		if catchUp {
			d.Reason = fmt.Sprintf("missed by %s, beyond the %s grace window",
				human.Duration(late), human.Duration(grace))
		} else {
			d.Reason = fmt.Sprintf("late by %s, catch-up disabled", human.Duration(late))
		}
		return d
	}

	d.Run = true
	if late <= tolerance {
		d.Reason = "on time"
	} else {
		d.Reason = fmt.Sprintf("caught up %s late", human.Duration(late))
	}
	return d
}
