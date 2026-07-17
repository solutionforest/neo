package ui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
)

// currentVersion is set by SetVersion/PrintBanner and used by Select to render the banner.
var currentVersion string

// SetVersion stores the CLI version for rendering in TUI menus.
func SetVersion(v string) {
	currentVersion = v
}

// Key constants returned by ReadKey.
const (
	KeyUp    = "up"
	KeyDown  = "down"
	KeyEnter = "enter"
	KeyEsc   = "esc"
	KeyQ     = "q"
)

// ReadKey reads a single keypress from stdin using raw mode.
// Arrow keys are 3-byte escape sequences (\033[A/B); other keys are 1 byte.
func ReadKey() string {
	fd := os.Stdin.Fd()
	old, err := term.MakeRaw(fd)
	if err != nil {
		// Not a TTY or unsupported — fall back to blocking read
		buf := make([]byte, 1)
		os.Stdin.Read(buf) //nolint:errcheck
		return KeyEnter
	}
	defer term.Restore(fd, old) //nolint:errcheck

	buf := make([]byte, 3)
	n, _ := os.Stdin.Read(buf)
	if n == 0 {
		return ""
	}

	b := buf[0]
	switch {
	case b == '\r' || b == '\n':
		return KeyEnter
	case b == 3: // Ctrl+C
		// os.Exit skips deferred calls, so restore the terminal out of raw mode
		// here — otherwise the shell is left with OPOST disabled ("staircase"
		// output). Restore cooked mode first, then show the cursor.
		term.Restore(fd, old) //nolint:errcheck
		ShowCursor()
		fmt.Println()
		os.Exit(130) // 128 + SIGINT
	case b == 27 && n == 1:
		return KeyEsc
	case b == 27 && n >= 3 && buf[1] == '[':
		switch buf[2] {
		case 'A':
			return KeyUp
		case 'B':
			return KeyDown
		}
	case b == 'q' || b == 'Q':
		return KeyQ
	}
	return ""
}

// ClearScreen clears the terminal and moves the cursor to the top-left.
func ClearScreen() {
	fmt.Print("\033[2J\033[H")
}

// HideCursor hides the terminal cursor to reduce flicker during redraws.
func HideCursor() {
	fmt.Print("\033[?25l")
}

// ShowCursor restores the terminal cursor. Call with defer in TUI entry points.
func ShowCursor() {
	fmt.Print("\033[?25h")
}

// ShowLoading displays an animated loading screen with the NEO banner.
// Call the returned stop function when the operation completes.
//
//	stop := ui.ShowLoading("Connecting to server...")
//	defer stop()
func ShowLoading(message string) func() {
	done := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				ClearScreen()
				fmt.Print(RenderBanner())
				fmt.Printf("\r\n  %s %s\r\n", Yellow.Render(frames[i%len(frames)]), message)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return func() { close(done) }
}
