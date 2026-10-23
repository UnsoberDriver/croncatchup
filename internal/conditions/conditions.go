// Package conditions answers "can this job run right now?" — the part that
// makes debrouille a laptop scheduler rather than a server scheduler: do not
// start a two-hour backup on battery at 12 %, and do not start anything while
// the user is typing.
package conditions

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/UnsoberDriver/debrouille/internal/human"
)

// Requirements is what a job needs before it may start.
type Requirements struct {
	RequireAC      bool
	MinBattery     int
	RequireIdle    bool
	IdleFor        time.Duration
	IdleCheck      string
	RequireNetwork bool
	NetworkCheck   string
}

// Check states.
const (
	StateOK      = "ok"
	StateBlocked = "blocked"
	StateSkipped = "skipped"
)

// Check is one evaluated condition, kept even when it is not required so that
// `debrouille check --explain` can show the whole picture.
type Check struct {
	Name   string
	State  string
	Detail string
}

// Report is the outcome of evaluating every condition.
type Report struct {
	OK      bool
	Reasons []string
	Checks  []Check
}

// Evaluate inspects the machine. sysRoot exists so tests can point at a fake
// /sys tree instead of mocking the kernel.
func Evaluate(req Requirements, sysRoot string) Report {
	var r Report
	add := func(name, state, detail string) {
		r.Checks = append(r.Checks, Check{Name: name, State: state, Detail: detail})
		if state == StateBlocked {
			r.Reasons = append(r.Reasons, detail)
		}
	}

	p := readPower(sysRoot)

	if req.RequireAC {
		switch {
		case p.onAC && p.detail != "":
			add("power", StateOK, p.detail)
		case p.onAC:
			add("power", StateOK, "on AC power")
		default:
			add("power", StateBlocked, fmt.Sprintf("needs AC power, running on battery (%d%%)", p.battery))
		}
	} else if p.hasBattery {
		add("power", StateSkipped, "AC not required")
	}

	if req.MinBattery > 0 {
		switch {
		case !p.hasBattery:
			add("battery", StateSkipped, "no battery detected, check not applicable")
		case p.onAC:
			add("battery", StateOK, fmt.Sprintf("on AC, battery at %d%%", p.battery))
		case p.battery < 0:
			add("battery", StateBlocked, "cannot read battery level")
		case p.battery < req.MinBattery:
			add("battery", StateBlocked,
				fmt.Sprintf("battery at %d%%, below the %d%% threshold", p.battery, req.MinBattery))
		default:
			add("battery", StateOK, fmt.Sprintf("battery at %d%%", p.battery))
		}
	}

	if req.RequireIdle {
		ok, detail := checkIdle(req)
		if ok {
			add("idle", StateOK, detail)
		} else {
			add("idle", StateBlocked, detail)
		}
	}

	if req.RequireNetwork {
		target := req.NetworkCheck
		if target == "" {
			target = "1.1.1.1:53"
		}
		if !strings.Contains(target, ":") {
			target += ":443"
		}
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err != nil {
			add("network", StateBlocked, fmt.Sprintf("cannot reach %s", target))
		} else {
			_ = conn.Close()
			add("network", StateOK, "reachable "+target)
		}
	}

	if len(r.Checks) == 0 {
		add("conditions", StateOK, "none required")
	}
	r.OK = len(r.Reasons) == 0
	return r
}

type powerInfo struct {
	onAC       bool
	hasBattery bool
	battery    int
	sources    int
	detail     string
}

// readPower walks /sys/class/power_supply. It deliberately avoids upower and
// D-Bus: a single static binary with no daemon dependency is the whole point.
func readPower(sysRoot string) powerInfo {
	p := powerInfo{battery: -1}
	dir := filepath.Join(sysRoot, "class", "power_supply")

	entries, err := os.ReadDir(dir)
	if err != nil {
		// No power supply at all: this is a desktop, AC is implicit.
		p.onAC = true
		p.detail = "no power supply exposed by the kernel, assuming AC"
		return p
	}

	for _, e := range entries {
		base := filepath.Join(dir, e.Name())
		typ := strings.TrimSpace(readFile(filepath.Join(base, "type")))
		if typ == "" {
			continue
		}
		p.sources++

		switch typ {
		case "Battery":
			p.hasBattery = true
			if cap, err := strconv.Atoi(strings.TrimSpace(readFile(filepath.Join(base, "capacity")))); err == nil {
				if p.battery < 0 || cap < p.battery {
					p.battery = cap
				}
			}
		case "Mains", "USB", "USB_C", "USB_PD", "ADP", "AC":
			if strings.TrimSpace(readFile(filepath.Join(base, "online"))) == "1" {
				p.onAC = true
				p.detail = "on AC power"
			}
		}
	}

	if p.sources == 0 {
		p.onAC = true
		p.detail = "no power supply exposed by the kernel, assuming AC"
	}
	return p
}

func checkIdle(req Requirements) (bool, string) {
	if req.IdleCheck != "" {
		cmd := exec.Command("/bin/sh", "-c", req.IdleCheck)
		if err := cmd.Run(); err != nil {
			return false, "not idle (idle_check command failed)"
		}
		return true, "idle (idle_check command succeeded)"
	}

	// Wayland and GNOME both feed logind's IdleHint, so this works where
	// xprintidle cannot see anything.
	if sid := os.Getenv("XDG_SESSION_ID"); sid != "" {
		if out, err := exec.Command("loginctl", "show-session", sid, "-p", "IdleHint", "--value").Output(); err == nil {
			switch strings.TrimSpace(string(out)) {
			case "yes":
				return true, "session reported idle by logind"
			case "no":
				return false, "session reported active by logind"
			}
		}
	}

	if os.Getenv("DISPLAY") != "" {
		if out, err := exec.Command("xprintidle").Output(); err == nil {
			ms, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
			if perr == nil {
				idle := time.Duration(ms) * time.Millisecond
				need := req.IdleFor
				if need <= 0 {
					need = 5 * time.Minute
				}
				if idle >= need {
					return true, fmt.Sprintf("no input for %s", human.Duration(idle))
				}
				return false, fmt.Sprintf("last input %s ago, needs %s", human.Duration(idle), human.Duration(need))
			}
		}
	}

	return false, "cannot determine idle state: set settings.idle_check or install xprintidle"
}

func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
