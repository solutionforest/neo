package commands

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ui"
)

func newConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect",
		Short: "Open the Vxero platform in your browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnect()
		},
	}
}

func runConnect() error {
	url := "https://vxero.dev"
	fmt.Println()
	ui.Info("Opening " + url + " in your browser...")

	var err error
	switch runtime.GOOS {
	case "darwin":
		err = exec.Command("open", url).Start()
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}

	if err != nil {
		ui.Info("Visit: " + ui.Bold.Render(url))
	}
	return nil
}
