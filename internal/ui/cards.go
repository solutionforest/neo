package ui

import (
	"fmt"
	"regexp"
	"strings"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

// visLen returns the visible character width of s, stripping ANSI escape codes.
func visLen(s string) int {
	return len([]rune(ansiRE.ReplaceAllString(s, "")))
}

// Success prints a green checkmark with a message.
func Success(msg string) {
	fmt.Printf("  %s %s\n", Green.Render("✓"), msg)
}

// Error prints a red cross with a message.
func Error(msg string) {
	fmt.Printf("  %s %s\n", Red.Render("✗"), msg)
}

// Info prints a cyan arrow with a message.
func Info(msg string) {
	fmt.Printf("  %s %s\n", Cyan.Render("→"), msg)
}

// Card renders a boxed card with a title and key-value lines.
type Card struct {
	Lines []string
}

// NewCard creates a new card.
func NewCard() *Card {
	return &Card{}
}

// Add appends a line to the card.
func (c *Card) Add(line string) *Card {
	c.Lines = append(c.Lines, line)
	return c
}

// AddKV appends a key-value line.
func (c *Card) AddKV(key, value string) *Card {
	c.Lines = append(c.Lines, fmt.Sprintf("%-8s %s", key+":", value))
	return c
}

// Blank adds an empty line.
func (c *Card) Blank() *Card {
	c.Lines = append(c.Lines, "")
	return c
}

// Render prints the card with a box border.
func (c *Card) Render() {
	maxLen := 0
	for _, l := range c.Lines {
		if vl := visLen(l); vl > maxLen {
			maxLen = vl
		}
	}
	width := maxLen + 4
	if width < 40 {
		width = 40
	}

	fmt.Println()
	fmt.Printf("  ╭%s╮\n", strings.Repeat("─", width))
	for _, l := range c.Lines {
		padding := width - visLen(l) - 2
		if padding < 0 {
			padding = 0
		}
		fmt.Printf("  │  %s%s│\n", l, strings.Repeat(" ", padding))
	}
	fmt.Printf("  ╰%s╯\n", strings.Repeat("─", width))
	fmt.Println()
}
