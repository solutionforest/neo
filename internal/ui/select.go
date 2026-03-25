package ui

import (
	"fmt"
	"strings"
)

// SelectOption represents a single option in a select menu.
type SelectOption struct {
	Label string
	Value string
}

// Select shows a full-screen arrow-key-navigable menu with the NEO banner at the top.
// title is printed below the banner (can be multiline for context headers).
// Returns the selected Value, or "" if the user cancels with q or Esc.
func Select(title string, options []SelectOption) string {
	if len(options) == 0 {
		return ""
	}

	selected := 0
	HideCursor()
	defer ShowCursor()

	for {
		ClearScreen()
		fmt.Print(RenderBanner())

		if title != "" {
			// Normalise any bare \n in the title to \r\n so lines stay at column 0
			// in raw terminal mode (OPOST disabled).
			safe := strings.ReplaceAll(title, "\r\n", "\n")
			safe = strings.ReplaceAll(safe, "\n", "\r\n")
			fmt.Printf("\r\n%s\r\n", safe)
		}
		fmt.Print("\r\n")

		for i, opt := range options {
			if i == selected {
				fmt.Printf("  %s %s\r\n", Cyan.Render(">"), opt.Label)
			} else {
				fmt.Printf("    %s\r\n", opt.Label)
			}
		}

		fmt.Print("\r\n")
		fmt.Print(Faint.Render("  ↑↓ navigate   Enter select   q back") + "\r\n")

		key := ReadKey()
		switch key {
		case KeyUp:
			if selected > 0 {
				selected--
			}
		case KeyDown:
			if selected < len(options)-1 {
				selected++
			}
		case KeyEnter:
			return options[selected].Value
		case KeyEsc, KeyQ:
			return ""
		}
	}
}
