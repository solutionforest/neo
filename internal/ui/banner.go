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

// PrintUpgradeHint prints a styled Neo+ upsell block for free-tier users.
// Call this after PrintBanner or at natural pause points — not on every command.
func PrintUpgradeHint() {
	fmt.Printf("  %s  Unlock %s — unlimited servers, automated backups\n",
		Yellow.Render("★"), Bold.Render("Neo+"))
	fmt.Printf("      %s\n", Cyan.Render("neo.vxero.dev"))
	fmt.Printf("      %s\n", Faint.Render("Already have a key?  neo plus activate <key>"))
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
