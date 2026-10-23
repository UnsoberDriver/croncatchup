package main

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/UnsoberDriver/croncatchup/internal/config"
	"github.com/UnsoberDriver/croncatchup/internal/daemon"
	"github.com/UnsoberDriver/croncatchup/internal/view"
)

func (a *app) cmdDaemon(args []string) error {
	once := false
	for _, arg := range args {
		if arg == "--once" {
			once = true
		}
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	store, err := a.openStore(cfg)
	if err != nil {
		return err
	}

	d, err := daemon.New(cfg, store, a.ui, a.ui.Out)
	if err != nil {
		return err
	}

	if once {
		if err := d.Tick(context.Background()); err != nil {
			return err
		}
		rows := view.Rows(cfg, store.Snapshot(), time.Now())
		for i := range rows {
			rows[i][4] = a.colorState(rows[i][4])
		}
		a.ui.Table(view.Headers, rows)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()

	// SIGHUP reloads the config in place: editing a job should not require
	// killing the daemon that is three hours into a backup.
	reload := make(chan *config.Config, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, reloadSignals...)
	go func() {
		for range hup {
			fresh, err := config.Load(a.configPath())
			if err != nil {
				a.ui.Errf("reload: %v", err)
				continue
			}
			select {
			case reload <- fresh:
			default: // a reload is already queued
			}
		}
	}()

	return d.Run(ctx, reload)
}
