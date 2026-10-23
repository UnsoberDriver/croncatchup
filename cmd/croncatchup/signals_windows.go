//go:build windows

package main

import (
	"os"
)

var stopSignals = []os.Signal{os.Interrupt}
var reloadSignals = []os.Signal{}
