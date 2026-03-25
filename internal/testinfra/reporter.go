package testinfra

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	faintStyle  = lipgloss.NewStyle().Faint(true)
	boldStyle   = lipgloss.NewStyle().Bold(true)
	cyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

// VMInfo holds the test VM details for the report header.
type VMInfo struct {
	Region string
	Size   string
	Image  string
	IP     string
}

// StepResult holds the outcome of a single test step.
type StepResult struct {
	Phase    string
	Name     string
	Passed   bool
	Duration time.Duration
	Error    string
}

// PrintResults renders a grouped, phase-level test results report.
func PrintResults(info VMInfo, results []StepResult) {
	// Compute totals
	passed, failed := 0, 0
	total := time.Duration(0)
	for _, r := range results {
		if r.Passed {
			passed++
		} else {
			failed++
		}
		total += r.Duration
	}

	w := 68

	fmt.Println()
	fmt.Printf("  %s\n", strings.Repeat("═", w))
	fmt.Printf("  %s\n", boldStyle.Render("Neo Integration Test Results"))
	fmt.Printf("  %s\n", strings.Repeat("═", w))
	if info.IP != "" {
		fmt.Printf("  %-10s %s  ·  %s  ·  %s\n", "VM:", info.Size, info.Region, info.Image)
		fmt.Printf("  %-10s %s\n", "IP:", cyanStyle.Render(info.IP))
	}
	fmt.Printf("  %-10s %s\n", "Total:", total.Round(time.Second))
	fmt.Printf("  %s\n", strings.Repeat("═", w))

	// Group results by phase
	type phaseGroup struct {
		name    string
		steps   []StepResult
		dur     time.Duration
		allPass bool
	}
	var phases []phaseGroup
	phaseIdx := map[string]int{}

	for _, r := range results {
		idx, ok := phaseIdx[r.Phase]
		if !ok {
			idx = len(phases)
			phaseIdx[r.Phase] = idx
			phases = append(phases, phaseGroup{name: r.Phase, allPass: true})
		}
		phases[idx].steps = append(phases[idx].steps, r)
		phases[idx].dur += r.Duration
		if !r.Passed {
			phases[idx].allPass = false
		}
	}

	for _, ph := range phases {
		fmt.Println()
		icon := greenStyle.Render("✓")
		if !ph.allPass {
			icon = redStyle.Render("✗")
		}
		phDur := faintStyle.Render(fmt.Sprintf("(%s)", ph.dur.Round(time.Millisecond)))
		fmt.Printf("  %s  %s  %s\n", icon, boldStyle.Render(ph.name), phDur)

		for _, r := range ph.steps {
			stepIcon := greenStyle.Render("✓")
			if !r.Passed {
				stepIcon = redStyle.Render("✗")
			}
			dur := faintStyle.Render(fmt.Sprintf("%s", r.Duration.Round(time.Millisecond)))
			fmt.Printf("    %s  %-46s %s\n", stepIcon, r.Name, dur)
			if !r.Passed && r.Error != "" {
				errMsg := r.Error
				if len(errMsg) > 72 {
					errMsg = errMsg[:69] + "..."
				}
				fmt.Printf("       %s\n", redStyle.Render(errMsg))
			}
		}
	}

	fmt.Println()
	fmt.Printf("  %s\n", strings.Repeat("─", w))
	summary := fmt.Sprintf("%s passed", greenStyle.Render(fmt.Sprintf("%d", passed)))
	if failed > 0 {
		summary += fmt.Sprintf("  ·  %s", redStyle.Render(fmt.Sprintf("%d failed", failed)))
	}
	summary += fmt.Sprintf("  ·  %s total", faintStyle.Render(total.Round(time.Second).String()))
	if failed == 0 {
		fmt.Printf("  %s  %s\n", greenStyle.Render("✓"), summary)
	} else {
		fmt.Printf("  %s  %s\n", yellowStyle.Render("!"), summary)
	}
	fmt.Println()
}
