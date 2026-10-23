package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/UnsoberDriver/debrouille/internal/conditions"
	"github.com/UnsoberDriver/debrouille/internal/human"
)

func (a *app) cmdCheck(args []string) error {
	explain := false
	for _, arg := range args {
		if arg == "--explain" {
			explain = true
		}
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	fmt.Fprintf(a.ui.Out, "%s config ok: %s\n", a.ui.Green("✓"), cfg.Path)
	fmt.Fprintf(a.ui.Out, "  %-7s %s\n", a.ui.Dim("state"), cfg.Settings.StateDir)
	fmt.Fprintf(a.ui.Out, "  %-7s %s\n", a.ui.Dim("logs"), cfg.Settings.LogDir)
	fmt.Fprintf(a.ui.Out, "  %-7s %s\n", a.ui.Dim("tick"), human.Duration(cfg.Tick()))

	for i := range cfg.Jobs {
		job := &cfg.Jobs[i]
		spec, err := job.Spec()
		if err != nil {
			return err
		}
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

		label := a.ui.Green("ready")
		if !report.OK {
			label = a.ui.Yellow("waiting")
		}
		fmt.Fprintf(a.ui.Out, "\n%s  %s  %s\n", a.ui.Bold(job.Name), label,
			a.ui.Dim("("+spec.Human()+", grace "+human.Duration(job.EffectiveGrace(cfg))+")"))

		if explain {
			for _, c := range report.Checks {
				switch c.State {
				case conditions.StateOK:
					fmt.Fprintf(a.ui.Out, "    %s %-10s %s\n", a.ui.Green("✓"), c.Name, a.ui.Dim(c.Detail))
				case conditions.StateBlocked:
					fmt.Fprintf(a.ui.Out, "    %s %-10s %s\n", a.ui.Red("✗"), c.Name, c.Detail)
				default:
					fmt.Fprintf(a.ui.Out, "    %s %-10s %s\n", a.ui.Dim("–"), c.Name, a.ui.Dim(c.Detail))
				}
			}
		} else if !report.OK {
			fmt.Fprintf(a.ui.Out, "    %s\n", a.ui.Dim(strings.Join(report.Reasons, "; ")))
		}
	}
	return nil
}
