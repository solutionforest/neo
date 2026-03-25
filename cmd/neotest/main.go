package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vxero/neo/internal/testinfra"
)

func main() {
	token := flag.String("token", "", "DigitalOcean API token (or DIGITALOCEAN_TOKEN env)")
	region := flag.String("region", "sgp1", "DO region")
	size := flag.String("size", "s-1vcpu-1gb", "DO droplet size")
	keep := flag.Bool("keep", false, "Keep VM alive after tests")
	flag.Parse()

	// Resolve token
	tok := *token
	if tok == "" {
		tok = os.Getenv("DIGITALOCEAN_TOKEN")
	}
	if tok == "" {
		// Try loading from ../../.env (project root)
		tok = loadEnvToken()
	}
	if tok == "" {
		fmt.Println("  Error: no DIGITALOCEAN_TOKEN provided")
		fmt.Println("  Use --token flag, DIGITALOCEAN_TOKEN env, or set it in ../../.env")
		os.Exit(1)
	}

	// Find testdata directory
	testData := findTestData()
	if testData == "" {
		fmt.Println("  Error: could not find testdata/ directory")
		os.Exit(1)
	}

	runner := &testinfra.Runner{
		Token:    tok,
		Region:   *region,
		Size:     *size,
		Image:    "ubuntu-24-04-x64",
		Keep:     *keep,
		TestData: testData,
	}

	if err := runner.Run(); err != nil {
		fmt.Printf("  Fatal: %s\n", err)
	}

	if *keep {
		// Auto-register server in ~/.neo/config.json
		runner.SaveNeoConfig()

		fmt.Println()
		fmt.Println("  ┌──────────────────────────────────────────────────────────────┐")
		fmt.Printf("  │  IP:      %-52s│\n", runner.DropletIP())
		fmt.Printf("  │  SSH:     %-52s│\n", runner.SSHCommand())
		fmt.Println("  │  Key:     /tmp/neo-test-key                                  │")
		fmt.Println("  │  neo:     server registered as 'neo-test' in ~/.neo/config   │")
		fmt.Println("  └──────────────────────────────────────────────────────────────┘")
		fmt.Println()
		fmt.Println("  You can now run:  neo  (to open the dashboard)")
		fmt.Println("  Destroy when done: ./bin/neotest  (press Enter to destroy)")
		fmt.Println()
	} else {
		fmt.Println()
		fmt.Print("  Press Enter to destroy the VM (Ctrl+C to keep it)... ")
		bufio.NewReader(os.Stdin).ReadBytes('\n')
		runner.Teardown()
	}

	if runner.HasFailures() {
		os.Exit(1)
	}
}

func loadEnvToken() string {
	// Look for .env relative to the binary or CWD
	paths := []string{
		"../../.env",          // from neo/ dir
		"../../../.env",       // from neo/bin/ dir
		filepath.Join(os.Getenv("HOME"), "Development/solution-forest/projects/Vanguard/.env"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DIGITALOCEAN_TOKEN=") {
				val := strings.TrimPrefix(line, "DIGITALOCEAN_TOKEN=")
				val = strings.Trim(val, "\"'")
				if val != "" {
					return val
				}
			}
		}
	}
	return ""
}

func findTestData() string {
	// Try relative paths
	candidates := []string{
		"testdata",
		"../testdata",
		"../../testdata",
		filepath.Join(os.Getenv("HOME"), "Development/solution-forest/projects/Vanguard/neo/testdata"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}
