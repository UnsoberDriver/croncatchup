// Package config loads and validates the debrouille TOML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/UnsoberDriver/debrouille/internal/schedule"
)

// Duration is a time.Duration decoded from a TOML string such as "90m" or "2h".
type Duration struct {
	time.Duration
}

// UnmarshalTOML implements toml.Unmarshaler.
func (d *Duration) UnmarshalTOML(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("expected a duration string like \"6h\", got %T", v)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

// Or returns the duration, or def when it was left out of the file.
func (d Duration) Or(def time.Duration) time.Duration {
	if d.Duration == 0 {
		return def
	}
	return d.Duration
}

// Settings are the defaults shared by every job.
type Settings struct {
	StateDir         string   `toml:"state_dir"`
	LogDir           string   `toml:"log_dir"`
	Tick             Duration `toml:"tick"`
	Grace            Duration `toml:"grace"`
	Timeout          Duration `toml:"timeout"`
	Shell            string   `toml:"shell"`
	RequireAC        bool     `toml:"require_ac"`
	MinBattery       int      `toml:"min_battery"`
	RequireIdle      bool     `toml:"require_idle"`
	IdleFor          Duration `toml:"idle_for"`
	IdleCheck        string   `toml:"idle_check"`
	RequireNetwork   bool     `toml:"require_network"`
	NetworkCheck     string   `toml:"network_check"`
	RunMissedOnStart *bool    `toml:"run_missed_on_start"`
}

// Job is one scheduled command.
type Job struct {
	Name           string            `toml:"name"`
	Command        string            `toml:"command"`
	Every          Duration          `toml:"every"`
	Schedule       string            `toml:"schedule"`
	At             string            `toml:"at"`
	Day            string            `toml:"day"`
	Timeout        Duration          `toml:"timeout"`
	Grace          Duration          `toml:"grace"`
	CatchUp        *bool             `toml:"catch_up"`
	RequireAC      *bool             `toml:"require_ac"`
	RequireIdle    *bool             `toml:"require_idle"`
	RequireNetwork *bool             `toml:"require_network"`
	WorkingDir     string            `toml:"working_dir"`
	Env            map[string]string `toml:"env"`
}

// Config is the whole file.
type Config struct {
	Path     string   `toml:"-"`
	Settings Settings `toml:"settings"`
	Jobs     []Job    `toml:"job"`
}

// Error reports every problem in the file at once instead of one per run.
type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// DefaultPath follows the XDG base directory specification.
func DefaultPath() string {
	if p := os.Getenv("DEBROUILLE_CONFIG"); p != "" {
		return ExpandPath(p)
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "debrouille", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "debrouille", "config.toml")
}

// ExpandPath turns a leading ~ into the user's home directory.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return p
}

// Load reads, defaults and validates a configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	md, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		var keys []string
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, &Error{Problems: []string{"unknown key(s): " + strings.Join(keys, ", ") +
			" (a typo in a key name is silently ignored by most tools, which is why it is fatal here)"}}
	}

	cfg.Path = path
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	s := &c.Settings
	s.StateDir = ExpandPath(s.StateDir)
	s.LogDir = ExpandPath(s.LogDir)
	if s.StateDir == "" || s.LogDir == "" {
		base := filepath.Dir(c.Path)
		home, err := os.UserHomeDir()
		if err == nil && base == filepath.Join(home, ".config", "debrouille") {
			base = filepath.Join(home, ".local", "state", "debrouille")
		}
		if s.StateDir == "" {
			s.StateDir = filepath.Join(base, "state")
		}
		if s.LogDir == "" {
			s.LogDir = filepath.Join(base, "logs")
		}
	}
	if s.Shell == "" {
		s.Shell = "/bin/sh"
	}
	if s.NetworkCheck == "" {
		s.NetworkCheck = "1.1.1.1:53"
	}

	for i := range c.Jobs {
		j := &c.Jobs[i]
		j.WorkingDir = ExpandPath(j.WorkingDir)
		if j.At == "" {
			j.At = "03:00"
		}
	}
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (c *Config) validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if len(c.Jobs) == 0 {
		add("no jobs defined: add at least one [[job]] table")
	}
	if c.Settings.MinBattery < 0 || c.Settings.MinBattery > 100 {
		add("settings.min_battery must be between 0 and 100, got %d", c.Settings.MinBattery)
	}
	if c.Settings.Tick.Duration < 0 {
		add("settings.tick cannot be negative")
	}
	if c.Settings.Grace.Duration < 0 {
		add("settings.grace cannot be negative")
	}

	seen := map[string]bool{}
	for i := range c.Jobs {
		j := &c.Jobs[i]
		where := fmt.Sprintf("job #%d", i+1)
		if j.Name == "" {
			add("%s: name is required", where)
		} else {
			where = fmt.Sprintf("job %q", j.Name)
			if !nameRe.MatchString(j.Name) {
				add("%s: name may only contain letters, digits, dot, dash and underscore", where)
			}
			if seen[j.Name] {
				add("%s: duplicate name (names must be unique, they identify state and logs)", where)
			}
			seen[j.Name] = true
		}
		if strings.TrimSpace(j.Command) == "" {
			add("%s: command is required", where)
		}
		if j.Every.Duration < 0 || j.Schedule != "" && j.Every.Duration > 0 {
			add("%s: set either every or schedule, not both", where)
		}
		if j.Every.Duration == 0 && j.Schedule == "" {
			add("%s: set every (e.g. every = \"6h\") or schedule (\"daily\" / \"weekly\")", where)
		}
		switch strings.ToLower(j.Schedule) {
		case "":
		case "daily":
			if j.Day != "" {
				add("%s: day is only valid with schedule = \"weekly\"", where)
			}
			if j.Every.Duration > 0 {
				add("%s: schedule and every are mutually exclusive", where)
			}
		case "weekly":
			if j.Day == "" {
				add("%s: schedule = \"weekly\" needs day (e.g. day = \"sunday\")", where)
			}
			if _, err := parseWeekday(j.Day); err != nil {
				add("%s: %v", where, err)
			}
		default:
			add("%s: unknown schedule %q (use \"daily\" or \"weekly\")", where, j.Schedule)
		}
		if strings.HasPrefix(strings.ToLower(j.Schedule), "daily") ||
			strings.HasPrefix(strings.ToLower(j.Schedule), "weekly") {
			if _, err := parseClock(j.At); err != nil {
				add("%s: %v", where, err)
			}
		}
		if j.Every.Duration > 0 && j.Every.Duration < time.Second {
			add("%s: every must be at least 1s", where)
		}
		if j.Timeout.Duration < 0 {
			add("%s: timeout cannot be negative", where)
		}
		if j.Grace.Duration < 0 {
			add("%s: grace cannot be negative", where)
		}
	}

	if len(problems) > 0 {
		return &Error{Problems: problems}
	}
	return nil
}

// Job looks a job up by name.
func (c *Config) Job(name string) (*Job, bool) {
	for i := range c.Jobs {
		if c.Jobs[i].Name == name {
			return &c.Jobs[i], true
		}
	}
	return nil, false
}

// Spec resolves the job's recurrence.
func (j *Job) Spec() (schedule.Spec, error) {
	if j.Every.Duration > 0 {
		return schedule.Spec{Kind: schedule.Interval, Every: j.Every.Duration}, nil
	}
	switch strings.ToLower(j.Schedule) {
	case "daily":
		at, err := parseClock(j.At)
		if err != nil {
			return schedule.Spec{}, err
		}
		return schedule.Spec{Kind: schedule.Daily, At: at}, nil
	case "weekly":
		at, err := parseClock(j.At)
		if err != nil {
			return schedule.Spec{}, err
		}
		day, err := parseWeekday(j.Day)
		if err != nil {
			return schedule.Spec{}, err
		}
		return schedule.Spec{Kind: schedule.Weekly, At: at, Day: day}, nil
	}
	return schedule.Spec{}, fmt.Errorf("job %q has no usable schedule", j.Name)
}

// Tick is how often the daemon evaluates jobs.
func (c *Config) Tick() time.Duration { return c.Settings.Tick.Or(30 * time.Second) }

// Grace is the default catch-up window.
func (c *Config) Grace() time.Duration { return c.Settings.Grace.Or(12 * time.Hour) }

// RunMissed reports whether jobs are evaluated immediately when the daemon starts.
func (c *Config) RunMissed() bool {
	if c.Settings.RunMissedOnStart == nil {
		return true
	}
	return *c.Settings.RunMissedOnStart
}

// EffectiveGrace is the job's grace window, falling back to the global one.
func (j *Job) EffectiveGrace(c *Config) time.Duration {
	return j.Grace.Or(c.Grace())
}

// CatchUpEnabled reports whether a run that missed its slot is still worth doing.
func (j *Job) CatchUpEnabled() bool {
	if j.CatchUp == nil {
		return true
	}
	return *j.CatchUp
}

// EffectiveTimeout is the job timeout, falling back to the global default.
func (j *Job) EffectiveTimeout(c *Config) time.Duration {
	return j.Timeout.Or(c.Settings.Timeout.Or(time.Hour))
}

// NeedsAC reports whether the job requires mains power.
func (j *Job) NeedsAC(c *Config) bool {
	if j.RequireAC != nil {
		return *j.RequireAC
	}
	return c.Settings.RequireAC
}

// NeedsIdle reports whether the job requires an idle machine.
func (j *Job) NeedsIdle(c *Config) bool {
	if j.RequireIdle != nil {
		return *j.RequireIdle
	}
	return c.Settings.RequireIdle
}

// NeedsNetwork reports whether the job requires connectivity.
func (j *Job) NeedsNetwork(c *Config) bool {
	if j.RequireNetwork != nil {
		return *j.RequireNetwork
	}
	return c.Settings.RequireNetwork
}

func parseClock(s string) (time.Duration, error) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("invalid at %q: expected HH:MM", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid at %q: expected a 24h time such as \"03:00\"", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

func parseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	}
	return 0, fmt.Errorf("invalid day %q: use monday..sunday", s)
}
