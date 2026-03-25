package bridge

import (
	"fmt"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// Connect installs the Vxero agent on the remote server and registers it.
func Connect(exec *ssh.Executor, vxeroURL, apiToken string) error {
	// Download and install the agent
	installCmd := fmt.Sprintf("curl -fsSL %s | sh", config.AgentInstallURL())
	if err := exec.RunQuiet(installCmd); err != nil {
		return fmt.Errorf("install agent: %w", err)
	}

	// Register the server with the control plane
	registerCmd := fmt.Sprintf(
		"vxero-agent register --url %s --token %s",
		vxeroURL, apiToken,
	)
	if err := exec.RunQuiet(registerCmd); err != nil {
		return fmt.Errorf("register agent: %w", err)
	}

	// Start the agent service
	if err := exec.RunQuiet("systemctl enable --now vxero-agent"); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	// Update remote state
	st, err := state.Load(exec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	st.Connected = true
	st.VxeroURL = vxeroURL
	st.VxeroToken = apiToken

	return state.Save(exec, st)
}

