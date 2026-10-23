//go:build unix

package main

import (
	"os"
	"syscall"
)

var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
var reloadSignals = []os.Signal{syscall.SIGHUP}
