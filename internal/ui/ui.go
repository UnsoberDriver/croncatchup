// Package ui renders the terminal output. Colours are dropped automatically
// when stdout is not a TTY or NO_COLOR is set, so piping to a log stays clean.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Printer writes human output.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	Color bool
}

// New builds a Printer for stdout/stderr.
func New() *Printer {
	return &Printer{
		Out:   os.Stdout,
		Err:   os.Stderr,
		Color: isTTY(os.Stdout) && os.Getenv("NO_COLOR") == "",
	}
}

// Plain builds a Printer with no colour, for tests and JSON-adjacent output.
func Plain(out, err io.Writer) *Printer {
	return &Printer{Out: out, Err: err}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	cyan   = "\x1b[36m"
)

func (p *Printer) paint(code, s string) string {
	if !p.Color {
		return s
	}
	return code + s + reset
}

// Bold renders s in bold.
func (p *Printer) Bold(s string) string { return p.paint(bold, s) }

// Dim renders s dimmed.
func (p *Printer) Dim(s string) string { return p.paint(dim, s) }

// Errf writes an error line to stderr.
func (p *Printer) Errf(format string, args ...any) {
	fmt.Fprintf(p.Err, p.paint(red, "error: ")+format+"\n", args...)
}

// Warnf writes a warning line to stderr.
func (p *Printer) Warnf(format string, args ...any) {
	fmt.Fprintf(p.Err, p.paint(yellow, "warning: ")+format+"\n", args...)
}

// Infof writes a neutral line.
func (p *Printer) Infof(format string, args ...any) {
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Event writes a timestamped event line: "14:03:12  run    backup  caught up 2h late".
func (p *Printer) Event(now time.Time, kind, job, message string) {
	label := fmt.Sprintf("%-6s", kind)
	var painted string
	switch kind {
	case "run":
		painted = p.paint(green, label)
	case "skip":
		painted = p.paint(yellow, label)
	case "wait":
		painted = p.paint(blue, label)
	case "fail":
		painted = p.paint(red, label)
	default:
		painted = p.Dim(label)
	}
	fmt.Fprintf(p.Out, "%s  %s %s  %s\n",
		p.Dim(now.Format("15:04:05")), painted, p.Bold(job), message)
}

// Table prints a left-aligned table with a dim header.
func (p *Printer) Table(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				// Strip ANSI escapes so coloured cells do not skew the layout.
				if l := len(stripANSI(cell)); l > widths[i] {
					widths[i] = l
				}
			}
		}
	}

	var b strings.Builder
	for i, h := range headers {
		b.WriteString(p.Dim(pad(h, widths[i])))
		if i < len(headers)-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")
	for _, row := range rows {
		for i, cell := range row {
			b.WriteString(pad(cell, widths[i]))
			if i < len(row)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprint(p.Out, b.String())
}

func pad(s string, width int) string {
	gap := width - len(stripANSI(s))
	if gap < 0 {
		gap = 0
	}
	return s + strings.Repeat(" ", gap)
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Green renders s in green.
func (p *Printer) Green(s string) string { return p.paint(green, s) }

// Yellow renders s in yellow.
func (p *Printer) Yellow(s string) string { return p.paint(yellow, s) }

// Red renders s in red.
func (p *Printer) Red(s string) string { return p.paint(red, s) }

// Cyan renders s in cyan.
func (p *Printer) Cyan(s string) string { return p.paint(cyan, s) }
