# debrouille

[![CI](https://github.com/UnsoberDriver/debrouille/actions/workflows/ci.yml/badge.svg)](https://github.com/UnsoberDriver/debrouille/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/UnsoberDriver/debrouille)](https://goreportcard.com/report/github.com/UnsoberDriver/debrouille)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A catch-up scheduler for machines that sleep.

`cron` and `systemd --user` timers run jobs at a wall-clock time. If your laptop is asleep, docked off the network, or on battery, that window is missed and nothing ever comes back to it. `debrouille` remembers what was due, catches up when you wake up, and skips runs that are too old to be useful.

## Why

- **Catches up.** A backup due at 03:00 while you were asleep runs the moment the machine is awake and conditions are met.
- **Grace window.** If you haven't opened your laptop in a week, `debrouille` won't queue seven nightly backups at once. It advances to the next occurrence and logs a `skip` with the reason.
- **Respects your machine.** Require AC power, a minimum battery percentage, an idle session, or network connectivity before starting heavy jobs.
- **Survives reboots.** State is written atomically to disk. `SIGHUP` reloads `config.toml` without restarting a running job.
- **Runs as a user service.** No root, no system-wide daemon required. Prints a ready-to-use `systemd --user` unit.

## Quick start

```bash
# Install
go install github.com/UnsoberDriver/debrouille/cmd/debrouille@latest

# Create a config
mkdir -p ~/.config/debrouille
cp example/config.toml ~/.config/debrouille/config.toml
$EDITOR ~/.config/debrouille/config.toml

# Validate it
debrouille check --explain

# Run once in the foreground (useful for testing)
debrouille daemon --once

# Or run as a user service
debrouille systemd > ~/.config/systemd/user/debrouille.service
systemctl --user daemon-reload
systemctl --user enable --now debrouille.service
```

## Example config

```toml
[settings]
tick = "30s"
grace = "12h"
require_ac = true
min_battery = 20
run_missed_on_start = true

[[job]]
name = "backup"
every = "24h"
require_ac = true
catch_up = true
command = "restic backup ~/Documents"
```

More examples: [example/config.toml](example/config.toml).

## Commands

| Command | Purpose |
| --- | --- |
| `debrouille daemon` | Long-running scheduler loop. `SIGHUP` reloads the config. |
| `debrouille daemon --once` | Evaluate every job once and exit. |
| `debrouille status` | Show schedule, last run, next due and state. |
| `debrouille run <job> [--force]` | Run a job now, skipping the schedule but still respecting conditions unless `--force`. |
| `debrouille check [--explain]` | Validate the config and inspect power/idle/network conditions. |
| `debrouille logs <job> [--tail N]` | Show the end of a job's log. |
| `debrouille systemd` | Print a `systemd --user` unit. |

## How catch-up works

1. **Remember.** `debrouille` writes `state.json` after every evaluation with the next due time.
2. **Sleep.** If the daemon is not running, the due date stays the same.
3. **Wake.** On the next `tick`, `decide` sees `now >= next_due`. If `now - next_due <= grace` it runs (and logs `caught up ... late`). If it's outside the grace window, it skips and advances to `next_due + k*interval` (or the next wall-clock occurrence), so you don't get a flood of missed runs.
4. **Advance.** For intervals, the next due is calculated by steps from the previous slot (not `now`) to avoid drift.

## License

MIT
