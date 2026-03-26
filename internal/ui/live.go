package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// LiveViewConfig configures a live polling view.
type LiveViewConfig struct {
	Title    string        // displayed below the banner
	Interval time.Duration // polling interval (e.g., 3*time.Second)
	Render   func() (string, error)
}

// RunLiveView displays a live-updating view that polls Render() on the given interval.
// It runs until the user presses ESC, q, or Ctrl+C.
func RunLiveView(cfg LiveViewConfig) error {
	fd := os.Stdin.Fd()
	old, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("cannot enter raw mode: %w", err)
	}
	defer term.Restore(fd, old) //nolint:errcheck
	defer ShowCursor()

	HideCursor()

	stop := make(chan struct{})

	// Background goroutine: read stdin for ESC/q/Ctrl+C
	go func() {
		buf := make([]byte, 3)
		for {
			n, readErr := os.Stdin.Read(buf)
			if readErr != nil {
				return
			}
			if n == 0 {
				continue
			}
			b := buf[0]
			switch {
			case b == 3: // Ctrl+C
				close(stop)
				return
			case b == 27 && n == 1: // ESC
				close(stop)
				return
			case b == 'q' || b == 'Q':
				close(stop)
				return
			}
		}
	}()

	interval := cfg.Interval
	if interval == 0 {
		interval = 3 * time.Second
	}

	// Render immediately, then on each tick
	render := func() bool {
		content, renderErr := cfg.Render()

		ClearScreen()
		fmt.Print(RenderBanner())

		if cfg.Title != "" {
			safe := strings.ReplaceAll(cfg.Title, "\r\n", "\n")
			safe = strings.ReplaceAll(safe, "\n", "\r\n")
			fmt.Printf("\r\n%s\r\n", safe)
		}

		if renderErr != nil {
			fmt.Printf("\r\n  %s\r\n", Red.Render("Error: "+renderErr.Error()))
		} else {
			// Normalise newlines for raw mode
			safe := strings.ReplaceAll(content, "\r\n", "\n")
			safe = strings.ReplaceAll(safe, "\n", "\r\n")
			fmt.Print(safe)
		}

		fmt.Printf("\r\n%s\r\n", Faint.Render(fmt.Sprintf("  ESC back · refreshing every %ds", int(interval.Seconds()))))
		return true
	}

	// First render
	select {
	case <-stop:
		return nil
	default:
		render()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			render()
		}
	}
}
