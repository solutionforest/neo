package ui

import (
	"fmt"
	"strings"
)

// ProgressBar renders a simple progress bar.
func ProgressBar(current, total int, width int) string {
	if total <= 0 {
		return ""
	}
	pct := float64(current) / float64(total)
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s] %d%%", bar, int(pct*100))
}

// StatusBullet returns a colored bullet based on status.
func StatusBullet(status string) string {
	switch status {
	case "running":
		return Green.Render("●")
	case "stopped", "exited":
		return Gray.Render("○")
	case "pulling", "starting", "restarting":
		return Yellow.Render("◐")
	default:
		return Gray.Render("○")
	}
}
