package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vxero/neo/internal/sandbox"
)

func main() {
	host := flag.String("host", "root@localhost", "SSH host (user@host)")
	port := flag.Int("port", 0, "SSH port (overrides matrix port)")
	key := flag.String("key", "", "path to SSH private key")
	distro := flag.String("distro", "", "run a single distro (e.g. ubuntu-24.04, debian-12)")
	listDistros := flag.Bool("list", false, "list all distros in the matrix")
	flag.Parse()

	if *listDistros {
		fmt.Println()
		fmt.Println("  Neo Sandbox — Distro Matrix")
		fmt.Println("  ───────────────────────────")
		fmt.Println()
		fmt.Printf("  %-18s %-16s %-6s %s\n", "NAME", "IMAGE", "PORT", "STATUS")
		fmt.Printf("  %-18s %-16s %-6s %s\n", "────", "─────", "────", "──────")
		for _, d := range sandbox.Matrix() {
			status := "supported"
			if !d.Supported {
				status = "unsupported"
			}
			fmt.Printf("  %-18s %-16s %-6d %s\n", d.Name, d.Image, d.Port, status)
		}
		fmt.Println()
		return
	}

	if *key == "" {
		fmt.Println("  Error: --key is required")
		os.Exit(1)
	}

	keyData, err := os.ReadFile(*key)
	if err != nil {
		fmt.Printf("  Error reading key: %s\n", err)
		os.Exit(1)
	}

	// Determine which distros to run
	var distros []sandbox.Distro
	if *distro != "" {
		// Single distro mode
		for _, d := range sandbox.Matrix() {
			if d.Name == *distro || d.Service == *distro {
				distros = append(distros, d)
				break
			}
		}
		if len(distros) == 0 {
			fmt.Printf("  Error: unknown distro %q\n", *distro)
			fmt.Printf("  Available: %s\n", distroNames())
			os.Exit(1)
		}
	} else {
		// All distros mode — when port is specified, just run one
		if *port != 0 {
			distros = []sandbox.Distro{{
				Name:      "custom",
				Port:      *port,
				Supported: true,
			}}
		} else {
			distros = sandbox.Matrix()
		}
	}

	// Override port if specified
	if *port != 0 {
		for i := range distros {
			distros[i].Port = *port
		}
	}

	// Run tests
	hasFailures := false
	var summaries []distroSummary

	for i, d := range distros {
		if i > 0 {
			fmt.Println()
			fmt.Println("  ════════════════════════════════════════════════════════════════")
		}

		runner := &sandbox.Runner{
			Host:       *host,
			Port:       d.Port,
			PrivateKey: keyData,
			Distro:     d.Name,
			Supported:  d.Supported,
		}

		runner.Run()

		passed := !runner.HasFailures()
		summaries = append(summaries, distroSummary{name: d.Name, supported: d.Supported, passed: passed})
		if !passed {
			hasFailures = true
		}
	}

	// Print final matrix summary if more than one distro
	if len(summaries) > 1 {
		printMatrixSummary(summaries)
	}

	if hasFailures {
		os.Exit(1)
	}
}

type distroSummary struct {
	name      string
	supported bool
	passed    bool
}

func printMatrixSummary(summaries []distroSummary) {
	fmt.Println()
	fmt.Println("  ════════════════════════════════════════════════════════════════")
	fmt.Println("  DISTRO MATRIX SUMMARY")
	fmt.Println("  ════════════════════════════════════════════════════════════════")
	fmt.Println()

	passed, failed := 0, 0
	for _, s := range summaries {
		icon := "✓"
		if !s.passed {
			icon = "✗"
			failed++
		} else {
			passed++
		}
		expected := "supported"
		if !s.supported {
			expected = "unsupported"
		}
		fmt.Printf("  %s  %-18s  (%s)\n", icon, s.name, expected)
	}

	fmt.Println()
	if failed == 0 {
		fmt.Printf("  All %d distros passed.\n", passed)
	} else {
		fmt.Printf("  %d passed, %d failed.\n", passed, failed)
	}
	fmt.Println()
}

func distroNames() string {
	var names []string
	for _, d := range sandbox.Matrix() {
		names = append(names, d.Name)
	}
	return strings.Join(names, ", ")
}
