package main

import (
	"fmt"
	"os"
)

func (a *app) cmdSystemd(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "debrouille"
	}

	fmt.Fprintf(a.ui.Out, `# Save as: ~/.config/systemd/user/debrouille.service
# Then:    systemctl --user daemon-reload
#          systemctl --user enable --now debrouille.service
#          journalctl --user -u debrouille -f
#
# debrouille keeps its own state, so this unit is only responsible for
# restarting the loop after a reboot or a crash.

[Unit]
Description=debrouille catch-up scheduler
Documentation=%s
After=default.target

[Service]
Type=simple
ExecStart=%s daemon --config %s
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`, "https://github.com/UnsoberDriver/debrouille", exe, a.configPath())
	return nil
}
