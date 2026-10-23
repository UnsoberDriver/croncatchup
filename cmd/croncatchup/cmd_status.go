package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/UnsoberDriver/croncatchup/internal/human"
	"github.com/UnsoberDriver/croncatchup/internal/view"
)

func (a *app) cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	store, err := a.openStore(cfg)
	if err != nil {
		return err
	}
	snap := store.Snapshot()

	if a.json {
		return a.printJSON(snap)
	}

	now := time.Now()
	rows := view.Rows(cfg, snap, now)
	for i := range rows {
		rows[i][4] = a.colorState(rows[i][4])
	}
	a.ui.Table(view.Headers, rows)

	if name, when := nextRun(cfg, snap, now); name != "" {
		fmt.Fprintf(a.ui.Out, "\nnext: %s %s\n",
			a.ui.Bold(name), a.ui.Dim("in "+human.Duration(when.Sub(now))))
	}

	if a.verbose {
		fmt.Fprintf(a.ui.Out, "\nconfig: %s\nstate:  %s\nlogs:   %s\n",
			cfg.Path, cfg.Settings.StateDir, cfg.Settings.LogDir)
	}

	blocked := 0
	for _, row := range rows {
		if strings.HasPrefix(row[4], "failing") {
			blocked++
		}
	}
	if blocked > 0 {
		fmt.Fprintf(a.ui.Out, "\n%s %d job(s) failing — see `debrouille logs <job>`\n",
			a.ui.Red("!"), blocked)
	}
	return nil
}
