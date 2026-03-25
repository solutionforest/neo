package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/ui"
)

// askSkill represents a guided workflow that asks the user a series of questions.
type askSkill struct {
	name    string
	desc    string
	handler func(ask func(string) string, quit *bool)
}

func newAskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ask",
		Short: "Interactive skill assistant — guides you through common tasks",
		Long: `Start an interactive question-and-answer session.
neo asks questions to guide you through deploying, configuring, or troubleshooting.
Type 'quit' at any prompt to exit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk()
		},
	}
}

func runAsk() error {
	scanner := bufio.NewScanner(os.Stdin)
	stopped := false

	// ask prints a question, reads a line, and returns the answer.
	// Sets stopped=true and returns "" if the user types quit/exit/q.
	ask := func(question string) string {
		fmt.Printf("\n  %s %s\n  %s ", ui.Cyan.Render("?"), question, ui.Faint.Render(">"))
		if !scanner.Scan() {
			stopped = true
			return ""
		}
		ans := strings.TrimSpace(scanner.Text())
		if isQuitAnswer(ans) {
			stopped = true
			return ""
		}
		return ans
	}

	skills := []askSkill{
		deployGuide(),
		envGuide(),
		domainGuide(),
		serviceGuide(),
		troubleshootGuide(),
		serverGuide(),
	}

	ui.PrintBanner(cliVersion)
	fmt.Printf("  %s\n\n", ui.Faint.Render("Skill assistant — type 'quit' at any prompt to exit."))

	for {
		opts := make([]ui.SelectOption, 0, len(skills)+1)
		for _, s := range skills {
			label := fmt.Sprintf("%-14s %s", s.name, ui.Faint.Render(s.desc))
			opts = append(opts, ui.SelectOption{Label: label, Value: s.name})
		}
		opts = append(opts, ui.SelectOption{Label: "quit", Value: "quit"})

		chosen := ui.Select("  What do you need help with?", opts)
		if chosen == "" || chosen == "quit" || stopped {
			fmt.Println("\n  Goodbye!\n")
			return nil
		}

		for _, s := range skills {
			if s.name == chosen {
				s.handler(ask, &stopped)
				break
			}
		}

		if stopped {
			fmt.Println("\n  Goodbye!\n")
			return nil
		}
	}
}

// isQuitAnswer returns true for "quit", "exit", "q", "bye".
func isQuitAnswer(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "quit", "exit", "q", "bye":
		return true
	}
	return false
}

// --- Skills ---

func deployGuide() askSkill {
	return askSkill{
		name: "deploy",
		desc: "Deploy an app to a server",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Let's build your deploy command")
			fmt.Println()

			appName := ask("App name? (e.g. my-blog, defaults to directory name if blank)")
			if *quit {
				return
			}

			server := ask("Target server? (name from 'neo servers', or root@1.2.3.4, blank = current)")
			if *quit {
				return
			}

			domain := ask("Domain? (e.g. app.example.com — leave blank for a temp sslip.io domain)")
			if *quit {
				return
			}

			port := ask("Container port? (default: 8080, auto-detected from Dockerfile EXPOSE)")
			if *quit {
				return
			}
			if port == "" {
				port = "8080"
			}

			envFile := ask("Path to .env file? (leave blank to skip)")
			if *quit {
				return
			}

			fmt.Println()
			card := ui.NewCard()
			card.Add(ui.Bold.Render("Your deploy command:"))
			card.Blank()

			parts := []string{"neo deploy"}
			if appName != "" {
				parts = append(parts, "--name "+appName)
			}
			if server != "" {
				parts = append(parts, "--server "+server)
			}
			if domain != "" {
				parts = append(parts, "--domain "+domain)
			}
			if port != "8080" {
				parts = append(parts, "--port "+port)
			}
			if envFile != "" {
				parts = append(parts, "--env-file "+envFile)
			}

			card.Add("  " + strings.Join(parts, " "))
			card.Blank()
			card.Add(ui.Faint.Render("Run this from your project directory (where Dockerfile lives)"))
			if domain != "" {
				card.Blank()
				card.Add("After DNS is ready, enable HTTPS:")
				card.Add("  neo domain " + func() string {
					if appName != "" {
						return appName
					}
					return "<app>"
				}() + " --temp")
			}
			card.Render()
		},
	}
}

func envGuide() askSkill {
	return askSkill{
		name: "env",
		desc: "Set or view environment variables",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Environment variable management")
			fmt.Println()

			action := ask("What do you want to do? (set / view / import / unset)")
			if *quit {
				return
			}

			appName := ask("App name?")
			if *quit {
				return
			}

			fmt.Println()
			switch strings.ToLower(action) {
			case "set":
				key := ask("Variable name? (e.g. DATABASE_URL)")
				if *quit {
					return
				}
				val := ask("Value?")
				if *quit {
					return
				}
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo env set %s %s=%s", appName, key, val))
				ui.Info("(This restarts the container with the new value)")

			case "view":
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo env %s", appName))
				ui.Info("(Secret values are masked in the output)")

			case "import":
				file := ask("Path to .env file? (e.g. .env.production)")
				if *quit {
					return
				}
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo env import %s %s", appName, file))

			case "unset":
				key := ask("Variable name to remove?")
				if *quit {
					return
				}
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo env unset %s %s", appName, key))

			default:
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Available env commands:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo env %s                    view vars", appName))
				card.Add(fmt.Sprintf("  neo env set %s KEY=val        set a variable", appName))
				card.Add(fmt.Sprintf("  neo env unset %s KEY          remove a variable", appName))
				card.Add(fmt.Sprintf("  neo env import %s .env        bulk import from file", appName))
				card.Render()
			}
		},
	}
}

func domainGuide() askSkill {
	return askSkill{
		name: "domain",
		desc: "Set up domains and SSL certificates",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Domain and SSL setup")
			fmt.Println()

			appName := ask("App name?")
			if *quit {
				return
			}

			action := ask("What do you need? (set-domain / temp-domain / ssl-status)")
			if *quit {
				return
			}

			fmt.Println()
			switch strings.ToLower(action) {
			case "set-domain":
				domain := ask("Domain name? (e.g. app.example.com)")
				if *quit {
					return
				}
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Steps to set up your domain:"))
				card.Blank()
				card.Add("1. Add an A record in your DNS:")
				card.Add(fmt.Sprintf("     %s  →  <your server IP>", domain))
				card.Blank()
				card.Add("2. Set the domain in neo:")
				card.Add(fmt.Sprintf("     neo domain %s %s", appName, domain))
				card.Blank()
				card.Add(ui.Faint.Render("SSL is auto-provisioned via Let's Encrypt once DNS resolves"))
				card.Render()

			case "temp-domain":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Assign a temporary sslip.io domain with auto-SSL:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo domain %s --temp", appName))
				card.Blank()
				card.Add(ui.Faint.Render("Format: <app>.<server-ip>.sslip.io"))
				card.Add(ui.Faint.Render("No DNS setup needed — works instantly"))
				card.Render()

			case "ssl-status":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Check SSL / Caddy status:"))
				card.Blank()
				card.Add("  neo list                         all apps and domains")
				card.Add(fmt.Sprintf("  neo logs %s               container logs", appName))
				card.Render()

			default:
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Domain commands:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo domain %s app.example.com   set a real domain", appName))
				card.Add(fmt.Sprintf("  neo domain %s --temp            assign temp sslip.io domain", appName))
				card.Render()
			}
		},
	}
}

func serviceGuide() askSkill {
	return askSkill{
		name: "service",
		desc: "Create databases and shared services",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Shared database / service setup")
			fmt.Println()

			action := ask("What do you need? (create / link / list / remove)")
			if *quit {
				return
			}

			fmt.Println()
			switch strings.ToLower(action) {
			case "create":
				svcType := ask("Service type? (postgres / mysql / redis / mariadb)")
				if *quit {
					return
				}
				svcName := ask("Service name? (e.g. main-db, leave blank for default)")
				if *quit {
					return
				}
				if svcName == "" {
					svcName = svcType
				}
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo service create %s %s", svcType, svcName))
				ui.Info("Then link it to your app: neo service link " + svcName + " <app>")

			case "link":
				svcName := ask("Service name? (from 'neo service list')")
				if *quit {
					return
				}
				appName := ask("App to link to?")
				if *quit {
					return
				}
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Link service to app:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo service link %s %s", svcName, appName))
				card.Blank()
				card.Add(ui.Faint.Render("This creates a database + user and injects DATABASE_URL into your app"))
				card.Render()

			case "list":
				fmt.Println()
				ui.Success("Run:  neo service list")

			case "remove":
				svcName := ask("Service name to remove?")
				if *quit {
					return
				}
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Remove service:"))
				card.Blank()
				card.Add("First unlink all apps:")
				card.Add(fmt.Sprintf("  neo service unlink %s <app>", svcName))
				card.Blank()
				card.Add("Then remove:")
				card.Add(fmt.Sprintf("  neo service remove %s", svcName))
				card.Render()

			default:
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Service commands:"))
				card.Blank()
				card.Add("  neo service create postgres db   create a Postgres instance")
				card.Add("  neo service create redis cache   create a Redis instance")
				card.Add("  neo service list                 show all services")
				card.Add("  neo service link db myapp        link service to app")
				card.Add("  neo service logs db              stream service logs")
				card.Render()
			}
		},
	}
}

func troubleshootGuide() askSkill {
	return askSkill{
		name: "troubleshoot",
		desc: "Diagnose and fix common issues",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Let's troubleshoot")
			fmt.Println()

			symptom := ask("What is the problem?\n  (app-down / deploy-failed / no-domain / ssl-error / slow / other)")
			if *quit {
				return
			}

			appName := ask("Which app is affected? (leave blank for general advice)")
			if *quit {
				return
			}

			app := appName
			if app == "" {
				app = "<app>"
			}

			fmt.Println()
			switch strings.ToLower(symptom) {
			case "app-down":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("App is down — try these steps:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo logs %s              check recent logs", app))
				card.Add(fmt.Sprintf("  neo restart %s           restart the container", app))
				card.Add(fmt.Sprintf("  neo status %s            view container status", app))
				card.Blank()
				card.Add(ui.Faint.Render("If the container keeps crashing, check for missing env vars:"))
				card.Add(fmt.Sprintf("  neo env %s", app))
				card.Render()

			case "deploy-failed":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Deploy failed — check these:"))
				card.Blank()
				card.Add("  1. Does your Dockerfile build locally?")
				card.Add("       docker build -t test .")
				card.Blank()
				card.Add("  2. Check startup errors:")
				card.Add(fmt.Sprintf("       neo logs %s --tail 50", app))
				card.Blank()
				card.Add("  3. Check server memory:")
				card.Add("       neo list")
				card.Blank()
				card.Add("  4. If env vars are missing:")
				card.Add(fmt.Sprintf("       neo env set %s KEY=value", app))
				card.Render()

			case "no-domain":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Domain not resolving — check these:"))
				card.Blank()
				card.Add("  1. Verify the A record exists in your DNS provider")
				card.Add("  2. Check the domain is set in neo:")
				card.Add(fmt.Sprintf("       neo list"))
				card.Blank()
				card.Add("  3. DNS can take up to 48 hours to propagate")
				card.Add("  4. Test with: dig " + func() string {
					if appName != "" {
						return "<your-domain>"
					}
					return "<your-domain>"
				}())
				card.Render()

			case "ssl-error":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("SSL certificate issue — check these:"))
				card.Blank()
				card.Add("  1. Make sure port 80 is reachable (needed for ACME challenge)")
				card.Add("  2. DNS must resolve to your server IP before SSL can issue")
				card.Add("  3. Check Caddy logs on the server:")
				card.Add("       neo ssh   →   docker logs neo-caddy --tail 50")
				card.Blank()
				card.Add("  Alternative: use --temp for a free sslip.io domain with instant SSL:")
				card.Add(fmt.Sprintf("    neo domain %s --temp", app))
				card.Render()

			case "slow":
				card := ui.NewCard()
				card.Add(ui.Bold.Render("App is slow — investigate:"))
				card.Blank()
				card.Add("  Check resource usage:   neo list")
				card.Add(fmt.Sprintf("  Check logs for errors:  neo logs %s --tail 100", app))
				card.Blank()
				card.Add("  Common causes:")
				card.Add("    • Missing database index (check slow query logs)")
				card.Add("    • Server needs more RAM — check free memory")
				card.Add("    • Too many workers/sidecars competing for resources")
				card.Render()

			default:
				card := ui.NewCard()
				card.Add(ui.Bold.Render("General diagnostics:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo logs %s --tail 100     recent container logs", app))
				card.Add(fmt.Sprintf("  neo status %s              container state", app))
				card.Add(fmt.Sprintf("  neo restart %s             restart app", app))
				card.Add(fmt.Sprintf("  neo env %s                 check env vars", app))
				card.Add("  neo list                   all apps + services")
				card.Render()
			}
		},
	}
}

func serverGuide() askSkill {
	return askSkill{
		name: "server",
		desc: "Manage servers and SSH access",
		handler: func(ask func(string) string, quit *bool) {
			fmt.Println()
			ui.Info("Server management")
			fmt.Println()

			action := ask("What do you need? (add / switch / ssh / status / remove)")
			if *quit {
				return
			}

			fmt.Println()
			switch strings.ToLower(action) {
			case "add":
				host := ask("Server address? (e.g. root@1.2.3.4)")
				if *quit {
					return
				}
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Initialize a new server:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo init %s", host))
				card.Blank()
				card.Add(ui.Faint.Render("This installs Docker + Caddy and sets up the neo network"))
				card.Add(ui.Faint.Render("Requires Ubuntu 24.04+ or Debian"))
				card.Render()

			case "switch":
				name := ask("Server name to switch to? (from 'neo servers')")
				if *quit {
					return
				}
				fmt.Println()
				ui.Success(fmt.Sprintf("Run:  neo use %s", name))

			case "ssh":
				fmt.Println()
				ui.Success("Run:  neo ssh")
				ui.Info("Opens an interactive SSH session to the current server")

			case "status":
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Check server status:"))
				card.Blank()
				card.Add("  neo servers       list all configured servers")
				card.Add("  neo list          all apps on current server")
				card.Add("  neo ssh           SSH into server for direct access")
				card.Render()

			case "remove":
				name := ask("Server name to remove from neo config?")
				if *quit {
					return
				}
				fmt.Println()
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Remove server from config:"))
				card.Blank()
				card.Add(fmt.Sprintf("  neo config remove-server %s", name))
				card.Blank()
				card.Add(ui.Faint.Render("Note: this only removes it from local config, it does not destroy the server"))
				card.Render()

			default:
				card := ui.NewCard()
				card.Add(ui.Bold.Render("Server commands:"))
				card.Blank()
				card.Add("  neo init root@1.2.3.4   initialize a new server")
				card.Add("  neo servers             list all servers")
				card.Add("  neo use <name>          switch active server")
				card.Add("  neo ssh                 SSH into current server")
				card.Render()
			}
		},
	}
}
