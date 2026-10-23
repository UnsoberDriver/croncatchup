# croncatchup

[![CI](https://github.com/UnsoberDriver/croncatchup/actions/workflows/ci.yml/badge.svg)](https://github.com/UnsoberDriver/croncatchup/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/UnsoberDriver/croncatchup)](https://goreportcard.com/report/github.com/UnsoberDriver/croncatchup)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Run missed jobs when your laptop wakes up.**

If your laptop is closed, suspended, or powered off at the time `cron`, `anacron`, or a `systemd --user` timer was supposed to run, those jobs are simply skipped and never come back. `croncatchup` fixes that.

## The problem

You set up `restic backup` for 03:00 every night. Your laptop sleeps at 02:00 and wakes up at 08:00. Nothing runs. Next night same thing. A week later your backups are silently out of date.

## The solution

`croncatchup` keeps track of when each job *should* have run. When your machine comes back online/awake, it checks for missed runs and executes them — but only if they're still relevant.

- **Catch up on wake** - Remembers due times across sleeps/reboots. Runs missed jobs as soon as conditions are met after waking up.
- **Grace window** - If you haven't used your laptop in 7 days, you probably don't want 7 nightly backups firing all at once. Jobs older than `grace` are skipped and rescheduled to the next slot.
- **Laptops first** - Can wait for AC power, minimum battery %, idle session (via `logind`/`xprintidle`), or network before running heavy jobs.
- **No root required** - Runs entirely as a user daemon via `systemd --user`.
- **Safe by default** - Per-job locking (won't run twice), atomic state writes, logs per job with rotation, timeouts, and `SIGHUP` reload without killing running jobs.

## Install

```bash
# With Go
go install github.com/UnsoberDriver/croncatchup/cmd/croncatchup@latest

# Or build from source
git clone https://github.com/UnsoberDriver/croncatchup
cd croncatchup
go build ./cmd/croncatchup
```

## Quick start

```bash
# Create config
mkdir -p ~/.config/croncatchup
cp example/config.toml ~/.config/croncatchup/config.toml
$EDITOR ~/.config/croncatchup/config.toml

# Check conditions and config
croncatchup check --explain

# Test without starting the daemon
croncatchup daemon --once

# Run as user service (auto-start on login)
croncatchup systemd > ~/.config/systemd/user/croncatchup.service
systemctl --user daemon-reload
systemctl --user enable --now croncatchup.service

# See status
croncatchup status
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
every = "24h"        # run every 24h from last run
require_ac = true
require_network = true
catch_up = true
grace = "18h"
command = "restic backup --exclude-caches ~/Documents ~/Pictures"

[[job]]
name = "cleanup"
schedule = "weekly"
day = "sunday"
at = "04:00"       # run every Sunday at 04:00 (catches up if missed)
require_ac = true
require_idle = true
catch_up = true
command = "journalctl --vacuum-size=500M"
```

See [example/config.toml](example/config.toml) for more options.

## Commands

| Command | What it does |
| --- | --- |
| `croncatchup daemon` | Start the scheduler. Listens for `SIGHUP` to reload config. |
| `croncatchup daemon --once` | Evaluate all jobs once and exit (great for testing). |
| `croncatchup status` | Table of jobs: schedule, last run, next due, state (`ok`/`due`/`missed`/`failing`). |
| `croncatchup run <job> [--force]` | Run a job immediately. Respects conditions unless `--force`. Updates schedule as if it ran normally. |
| `croncatchup check [--explain]` | Validate config and show AC/battery/idle/network checks. |
| `croncatchup logs <job> [--tail N]` | View recent output from a job's log file. |
| `croncatchup systemd` | Generate a `systemd --user` unit file. |

## How catch-up works

1. **Track** - After each run, `croncatchup` records `next_due` in `~/.local/state/croncatchup/state.json` (atomically written).
2. **Miss** - If the daemon isn't running while asleep, `next_due` stays in the past.
3. **Wake & check** - On next tick, it sees the job is due. It checks conditions (AC/idle/network/battery). If blocked, it waits and retries each tick.
4. **Decide** - If `now - next_due <= grace` → run it (marked "caught up X late"). If `> grace` → skip and advance to the next slot without flooding runs.
5. **Advance** - For intervals, next due is computed by steps from the previous slot (prevents drift). For daily/weekly, it finds the next occurrence in the future.

State lives in `~/.local/state/croncatchup/`, logs in `~/.local/state/croncatchup/logs/`. Both are user-writable, no root needed.

## License

[MIT](LICENSE)
