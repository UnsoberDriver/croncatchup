// Package human formats values for terminal output.
package human

import (
	"fmt"
	"strings"
	"time"
)

// Duration renders a duration the way a person would say it: "2d 4h",
// "1h 12m", "45s". It is deliberately coarse: nobody cares about the
// milliseconds of a backup that ran three days ago.
func Duration(d time.Duration) string {
	if d < 0 {
		return "-" + Duration(-d)
	}
	if d < time.Second {
		return "0s"
	}

	days := int64(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int64(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int64(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int64(d / time.Second)

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 && days == 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 && days == 0 && hours == 0 && mins == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	if len(parts) == 0 {
		return "0s"
	}
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, " ")
}

// Ago renders how long ago t was, or "never" for the zero time.
func Ago(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return Duration(now.Sub(t)) + " ago"
}
