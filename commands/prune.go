package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ui"
)

func newPruneCmd() *cobra.Command {
	var keepCount int
	var forceFlag bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove old Docker images from the server",
		Long:  "Lists all neo-managed images grouped by app and removes old versions, keeping the most recent ones for rollback.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrune(keepCount, forceFlag, dryRun)
		},
	}

	cmd.Flags().IntVar(&keepCount, "keep", 2, "number of recent images to keep per app")
	cmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed without deleting")
	return cmd
}

func runPrune(keepCount int, force, dryRun bool) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	docker := remote.NewDocker(exec)

	sp := ui.NewSpinner("Scanning images...")
	sp.Start()
	groups, err := docker.ListNeoImages()
	sp.Stop()
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	if len(groups) == 0 {
		fmt.Println(ui.Faint.Render("  No neo-managed images found."))
		return nil
	}

	// Collect candidates for removal, grouped by repo.
	type candidate struct {
		tag  string
		size string
	}
	type repoGroup struct {
		repo       string
		keep       []candidate
		remove     []candidate
	}

	var repoNames []string
	for repo := range groups {
		repoNames = append(repoNames, repo)
	}
	sort.Strings(repoNames)

	var allGroups []repoGroup
	totalRemove := 0
	for _, repo := range repoNames {
		entries := groups[repo]
		rg := repoGroup{repo: repo}
		for i, e := range entries {
			c := candidate{tag: e.Tag, size: e.Size}
			if i < keepCount {
				rg.keep = append(rg.keep, c)
			} else {
				rg.remove = append(rg.remove, c)
				totalRemove++
			}
		}
		allGroups = append(allGroups, rg)
	}

	// Display summary table.
	fmt.Println()
	for _, rg := range allGroups {
		appName := strings.TrimPrefix(rg.repo, "neo-")
		fmt.Printf("  %s\n", ui.Bold.Render(appName))
		for _, c := range rg.keep {
			tag := strings.TrimPrefix(c.tag, rg.repo+":")
			fmt.Printf("    %s %-28s %s\n", ui.Green.Render("keep  "), tag, ui.Faint.Render(c.size))
		}
		for _, c := range rg.remove {
			tag := strings.TrimPrefix(c.tag, rg.repo+":")
			fmt.Printf("    %s %-28s %s\n", ui.Yellow.Render("remove"), tag, ui.Faint.Render(c.size))
		}
	}
	fmt.Println()

	if totalRemove == 0 {
		fmt.Println(ui.Faint.Render("  Nothing to prune — all apps already within the keep limit."))
		return nil
	}

	if dryRun {
		fmt.Printf("  %s %d image(s) would be removed (--dry-run, no changes made)\n",
			ui.Faint.Render("dry-run:"), totalRemove)
		return nil
	}

	if !force {
		var confirmed bool
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Remove %d old image(s)?", totalRemove)).
				Description("Running containers will not be affected.").
				Value(&confirmed),
		)).Run(); err != nil || !confirmed {
			fmt.Println(ui.Faint.Render("  Cancelled."))
			return nil
		}
	}

	sp = ui.NewSpinner("Removing old images...")
	sp.Start()
	removed, err := docker.PruneAllImages(keepCount)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	count := len(removed)
	skipped := totalRemove - count
	msg := fmt.Sprintf("  Removed %d image(s)", count)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d skipped — still in use by running containers)", skipped)
	}
	fmt.Println(ui.Green.Render(msg))
	return nil
}
