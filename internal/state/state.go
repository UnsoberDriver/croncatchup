// Package state persists what happened last, so that a job missed during a
// week of sleep is still recognisable as missed when the laptop wakes up.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version is bumped when the on-disk shape changes incompatibly.
const Version = 1

// Results a job can end with.
const (
	ResultSuccess = "success"
	ResultFailed  = "failed"
	ResultSkipped = "skipped"
)

// JobState is everything debrouille remembers about one job.
type JobState struct {
	NextDue          time.Time `json:"next_due,omitzero"`
	LastStart        time.Time `json:"last_start,omitzero"`
	LastRun          time.Time `json:"last_run,omitzero"`
	LastExit         int       `json:"last_exit"`
	LastDurationMS   int64     `json:"last_duration_ms"`
	LastResult       string    `json:"last_result,omitempty"`
	Runs             int       `json:"runs"`
	Skips            int       `json:"skips"`
	ConsecutiveFails int       `json:"consecutive_failures"`
}

// Data is the serialised store.
type Data struct {
	Version int                  `json:"version"`
	Jobs    map[string]*JobState `json:"jobs"`
}

// Store is a JSON file guarded by a mutex. The daemon is the only long-lived
// writer, but the CLI must be able to read and mutate the same file safely.
type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

// Load reads the store, or returns an empty one if the file does not exist.
// A corrupted file is moved aside rather than silently discarded.
func Load(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: Data{Version: Version, Jobs: map[string]*JobState{}},
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}
	if len(raw) == 0 {
		return s, nil
	}

	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		aside := path + ".corrupt-" + time.Now().Format("20060102-150405")
		_ = os.Rename(path, aside)
		return s, fmt.Errorf("state file was corrupt (%v); moved to %s and starting fresh", err, aside)
	}
	if d.Version > Version {
		return nil, fmt.Errorf("state file %s was written by a newer debrouille (format %d > %d)", path, d.Version, Version)
	}
	if d.Jobs == nil {
		d.Jobs = map[string]*JobState{}
	}
	d.Version = Version
	s.data = d
	return s, nil
}

// Reload re-reads the file from disk. The daemon calls this at the start of
// every tick so that a `debrouille run backup` in another terminal is not
// silently overwritten by the daemon's in-memory copy.
func (s *Store) Reload() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(raw) == 0) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload state %s: %w", s.path, err)
	}

	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		// Do not destroy anything on a partial write; keep the in-memory view.
		return fmt.Errorf("reload state %s: %w", s.path, err)
	}
	if d.Version > Version {
		return fmt.Errorf("state file was written by a newer debrouille (format %d)", d.Version)
	}
	if d.Jobs == nil {
		d.Jobs = map[string]*JobState{}
	}
	d.Version = Version

	s.mu.Lock()
	s.data = d
	s.mu.Unlock()
	return nil
}

// Get returns the state for a job, creating it if needed.
func (s *Store) Get(name string) *JobState {
	s.mu.Lock()
	defer s.mu.Unlock()
	js, ok := s.data.Jobs[name]
	if !ok {
		js = &JobState{}
		s.data.Jobs[name] = js
	}
	return js
}

// Update applies fn to the job state under the lock.
func (s *Store) Update(name string, fn func(*JobState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	js, ok := s.data.Jobs[name]
	if !ok {
		js = &JobState{}
		s.data.Jobs[name] = js
	}
	fn(js)
}

// Prune drops state for jobs that no longer exist in the config.
func (s *Store) Prune(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.data.Jobs {
		if !keep[name] {
			delete(s.data.Jobs, name)
		}
	}
}

// Snapshot returns a copy safe to read without holding the lock.
func (s *Store) Snapshot() map[string]JobState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]JobState, len(s.data.Jobs))
	for name, js := range s.data.Jobs {
		out[name] = *js
	}
	return out
}

// Save writes the store atomically: a crash mid-write must never leave a
// truncated JSON file that looks like "you never ran that backup".
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}

	// fsync the directory so the rename itself survives a power cut.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
