package commands

import (
	"fmt"

	"github.com/spf13/cobra"
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
	fmt.Println()
	openBrowser("https://vxero.dev")
	return nil
}
