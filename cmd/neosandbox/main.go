package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vxero/neo/internal/sandbox"
)

func main() {
	host := flag.String("host", "root@localhost", "SSH host (user@host)")
	port := flag.Int("port", 2222, "SSH port")
	key := flag.String("key", "", "path to SSH private key")
	flag.Parse()

	if *key == "" {
		fmt.Println("  Error: --key is required")
		os.Exit(1)
	}

	keyData, err := os.ReadFile(*key)
	if err != nil {
		fmt.Printf("  Error reading key: %s\n", err)
		os.Exit(1)
	}

	runner := &sandbox.Runner{
		Host:       *host,
		Port:       *port,
		PrivateKey: keyData,
	}

	if err := runner.Run(); err != nil {
		fmt.Printf("  Fatal: %s\n", err)
	}

	if runner.HasFailures() {
		os.Exit(1)
	}
}
