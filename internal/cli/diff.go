package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/diff"
	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewDiffCmd() *cobra.Command {
	var concurrency int
	var contextLines int
	var outputDir string
	var format string

	cmd := &cobra.Command{
		Use:   "diff <current-dir> <incoming-dir>",
		Short: "Compare cluster manifests between two directories",
		Long: `Compare cluster manifests between current and incoming directories.

This command compares manifests for each cluster in parallel, showing:
- Added resources (new files in incoming)
- Deleted resources (files removed from current)
- Modified resources (files that changed)

Either shape of build output works on each side, and the two may differ:

  # one directory per cluster (build --output sliced)
  current/
    cluster1/Namespace:prod/Kind:deployment/Name:app1.yaml
    cluster2/...

  # one file per cluster (build, by default)
  incoming/
    cluster1.yaml
    cluster2.yaml

A flat side is sliced into a temporary directory first, so comparison is per
object either way and no separate slicing step is needed.

Output modes:
  1. Console output (default): Print all changes to stdout
  2. File output (-o flag): Save each cluster diff to separate file
     - cluster1-diff.md
     - cluster2-diff.md
     - index.md (summary table, which clusters changed)

Examples:
  # Console output (default)
  flux-tools diff ./current ./incoming

  # Save to separate Markdown files (recommended for GitLab MR)
  flux-tools diff ./current ./incoming -o ./diffs/

  # With custom concurrency
  flux-tools diff -j 10 ./current ./incoming -o ./diffs/

  # With more context lines (like git diff -U20)
  flux-tools diff --context 20 ./current ./incoming -o ./diffs/

  # Markdown format to stdout
  flux-tools diff --format markdown ./current ./incoming
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			currentDir := args[0]
			incomingDir := args[1]
			return runDiff(commandContext(cmd), currentDir, incomingDir, concurrency, contextLines, outputDir, format)
		},
	}

	cmd.Flags().IntVarP(&concurrency, "concurrency", "j", 0, "Number of parallel workers (default: number of clusters)")
	cmd.Flags().IntVarP(&contextLines, "context", "U", 3, "Number of context lines in diff output")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for per-cluster diff files (Markdown)")
	cmd.Flags().StringVar(&format, "format", "console", "Output format: console, markdown")

	return cmd
}

func runDiff(ctx context.Context, currentDir string, incomingDir string, concurrency int, contextLines int, outputDir string, format string) error {
	p := output.New(false)

	// Validate directories
	if _, err := os.Stat(currentDir); err != nil {
		return fmt.Errorf("current directory does not exist: %s", currentDir)
	}

	if _, err := os.Stat(incomingDir); err != nil {
		return fmt.Errorf("incoming directory does not exist: %s", incomingDir)
	}

	// Comparison is per object, which needs one directory per cluster. Given
	// build's default output instead, slice it into that shape first, so a
	// plain build result can be diffed without a separate slicing step.
	currentTree, cleanCurrent, err := prepareDiffInput(currentDir, p)
	if err != nil {
		return err
	}
	defer cleanCurrent()

	incomingTree, cleanIncoming, err := prepareDiffInput(incomingDir, p)
	if err != nil {
		return err
	}
	defer cleanIncoming()

	// Find all clusters (union across both directories) so that clusters
	// appearing only on the incoming side — for example after a layout
	// migration that renamed the cluster directory between branches — are
	// still reported as additions in the diff.
	clusters, err := diff.FindClustersUnion(currentTree, incomingTree)
	if err != nil {
		return fmt.Errorf("failed to find clusters: %w", err)
	}

	if len(clusters) == 0 {
		return fmt.Errorf("no manifests found in %s or %s: expected either build's output (one <cluster>.yaml per cluster) or a sliced tree (one directory per cluster)",
			currentDir, incomingDir)
	}

	// Determine concurrency (like in build command)
	if concurrency <= 0 {
		concurrency = len(clusters)
	}

	p.Info("Found %d clusters to compare\n", len(clusters))
	p.Info("Using %d workers\n", concurrency)
	p.Info("Context lines: %d\n", contextLines)

	// Compare clusters in parallel
	diffs := diff.CompareClustersParallel(ctx, clusters, currentTree, incomingTree, contextLines, concurrency)

	// Output mode: files or console
	if outputDir != "" {
		// Save to separate files (Markdown format)
		p.Info("Saving diffs to: %s\n", outputDir)

		if err := diff.SaveDiffsToFiles(diffs, outputDir); err != nil {
			return fmt.Errorf("failed to save diffs: %w", err)
		}

		// Print summary
		totalChanges := 0
		clustersWithChanges := 0
		for _, d := range diffs {
			if d.Error == nil && len(d.Changes) > 0 {
				clustersWithChanges++
				totalChanges += len(d.Changes)
			}
		}

		p.Info("\n✓ Saved %d cluster diffs to %s\n", len(diffs), outputDir)
		if totalChanges > 0 {
			p.Info("  %d clusters with changes (%d total changes)\n", clustersWithChanges, totalChanges)
		} else {
			p.Info("  No changes detected\n")
		}

	} else {
		// Console output
		var output string
		if format == "markdown" {
			// Print each cluster diff as Markdown
			for _, clusterDiff := range diffs {
				output += diff.FormatClusterDiffMarkdown(clusterDiff)
				output += "\n\n"
			}
		} else {
			// Console format (original)
			output = diff.FormatAllDiffs(diffs)
		}

		fmt.Print(output)

		// Check if there were any changes
		hasChanges := false
		for _, d := range diffs {
			if d.Error == nil && len(d.Changes) > 0 {
				hasChanges = true
				break
			}
		}

		if !hasChanges {
			p.Info("\n✓ No changes detected\n")
		}
	}

	return nil
}

// prepareDiffInput normalises one side of the comparison. `diff` compares one
// directory per cluster, which is what `build --output sliced` writes. Given
// build's default output instead — one <cluster>.yaml holding the whole
// cluster — it is sliced into the same shape in a temporary directory, using
// the default template so the reports read the same either way.
//
// The returned cleanup removes what was created, and does nothing when the
// directory already had the right shape.
func prepareDiffInput(dir string, p *output.Printer) (string, func(), error) {
	noop := func() {}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", noop, fmt.Errorf("failed to read %s: %w", dir, err)
	}

	var flat []string
	for _, entry := range entries {
		if entry.IsDir() {
			// One directory per cluster: already the shape the comparison needs.
			return dir, noop, nil
		}
		if fsutil.IsYAMLFile(entry.Name()) {
			flat = append(flat, entry.Name())
		}
	}
	if len(flat) == 0 {
		// Empty or unrecognised: let the caller report it against the path the
		// user actually typed.
		return dir, noop, nil
	}

	slicer, err := build.NewSlicer(build.DefaultSliceTemplate)
	if err != nil {
		return "", noop, err
	}

	tmp, err := os.MkdirTemp("", "flux-tools-diff-")
	if err != nil {
		return "", noop, fmt.Errorf("failed to create a working directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	for _, name := range flat {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("failed to read %s: %w", filepath.Join(dir, name), err)
		}

		objects, err := kustomize.ParseKustomizeOutput(data)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("failed to parse %s: %w", filepath.Join(dir, name), err)
		}

		cluster := strings.TrimSuffix(name, filepath.Ext(name))
		if err := build.WriteSlicedObjects(filepath.Join(tmp, cluster), objects, slicer); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("failed to slice %s: %w", filepath.Join(dir, name), err)
		}
	}

	p.Info("Sliced %d cluster file(s) from %s for comparison\n", len(flat), dir)
	return tmp, cleanup, nil
}
