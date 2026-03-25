package ui

import (
	"fmt"
	"strings"
)

const logo = `  █ █ ▀▄▀ █▀▀ █▀█ █▀█   ┃   █▄ █ █▀▀ █▀█
  ▀▄▀ █ █ ██▄ █▀▄ █▄█   ┃   █ ▀█ ██▄ █▄█`

func PrintBanner(version string) {
	currentVersion = version
	fmt.Println()
	fmt.Println(Cyan.Render(logo))
	fmt.Println(Faint.Render("  ⚡ neo " + version))
	fmt.Println()
}

// RenderBanner returns the banner as a string for embedding in TUI screen renders.
// All newlines are \r\n so the output is correct in raw terminal mode (OPOST disabled).
func RenderBanner() string {
	v := currentVersion
	if v == "" {
		v = "dev"
	}
	// Replace \n inside the lipgloss-rendered logo with \r\n so the cursor
	// returns to column 0 on each line even when OPOST is disabled.
	renderedLogo := strings.ReplaceAll(Cyan.Render(logo), "\n", "\r\n")
	return "\r\n" + renderedLogo + "\r\n" + Faint.Render("  ⚡ neo "+v) + "\r\n"
}
