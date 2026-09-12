package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/yq"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewYqCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "yq [eval-all] [filter] [path]",
		Short: "Process YAML manifests with yq filter in parallel",
		Long: `Process YAML manifests with yq filter in parallel.

This is a thin wrapper around yq that processes files in parallel.
You can use it in two ways:

1. With YQ_FILTER environment variable:
   YQ_FILTER='<filter>' flux-tools yq [path]

2. With explicit filter argument (like native yq):
   flux-tools yq eval-all '<filter>' [path]

Output:
  By default, processed files are saved to <input-dir>/cleaned/<filename>.yaml
  Use -o flag to specify a different output directory.

Examples:
  # Using environment variable (output to cluster-manifests/cleaned/)
  YQ_FILTER='select(.kind != "HelmRelease")' flux-tools yq ./cluster-manifests/

  # Using explicit filter (output to cluster-manifests/cleaned/)
  flux-tools yq eval-all 'select(.kind != "HelmRelease")' ./cluster-manifests/

  # With custom output directory
  flux-tools yq eval-all 'del(.webhooks[].clientConfig.caBundle)' -o ./output ./cluster-manifests/

  # With custom concurrency
  flux-tools yq -j 10 eval-all 'select(.kind != "HelmRelease")' ./cluster-manifests/

  # Tolerate a missing or empty input: create the output directory and stop.
  # Used when the diff baseline is empty (nothing deployed, or a new environment).
  flux-tools yq eval-all 'select(.kind != "HelmRelease")' ./manifests -o ./out --allow-missing-path

  # Complex filter with custom output
  flux-tools yq -o ./processed eval-all 'select(.kind != "HelmRelease") | del(
    .webhooks[].clientConfig.caBundle,
    .spec.template.metadata.annotations.rollme)' ./cluster-manifests/
`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// DisableFlagParsing hands every argument to RunE, cobra's own help
			// among them, so the command has to answer it itself.
			if isHelpRequest(args) {
				return cmd.Help()
			}

			// Parse arguments manually
			concurrency, filter, path, outputDir, verbose, allowMissing, err := parseYqArgs(args)
			if err != nil {
				return err
			}
			return runYq(commandContext(cmd), path, filter, outputDir, concurrency, verbose, allowMissing)
		},
		DisableFlagParsing: true,
	}

	return cmd
}

// parseYqArgs extracts flux-tools-owned flags (-j, -o, -v) and then interprets
// the leftover positionals, supporting both formats:
//  1. flux-tools yq [flags] <path>                    (YQ_FILTER from env)
//  2. flux-tools yq [flags] eval-all <filter> <path>  (explicit filter)
func parseYqArgs(args []string) (concurrency int, filter string, path string, outputDir string, verbose bool, allowMissingPath bool, err error) {
	f, err := extractOwnFlags(args, ownFlagsSpec{concurrency: true, outputDir: true, allowMissingPath: true})
	if err != nil {
		return 0, "", "", "", false, false, err
	}

	concurrency = runtime.NumCPU()
	if f.concurrencySet {
		concurrency = f.concurrency
	}

	// Interpret positionals from the leftover args (own flags already stripped).
	for i := 0; i < len(f.rest); i++ {
		arg := f.rest[i]
		if arg == "eval-all" {
			// eval-all <filter> <path>: take the next two non-flag positionals.
			var next []string
			for j := i + 1; j < len(f.rest) && len(next) < 2; j++ {
				if !strings.HasPrefix(f.rest[j], "-") {
					next = append(next, f.rest[j])
				}
			}
			if len(next) >= 2 {
				filter = next[0]
				path = next[1]
			}
			return concurrency, filter, path, f.outputDir, f.verbose, f.allowMissingPath, nil
		} else if !strings.HasPrefix(arg, "-") {
			path = arg
		}
	}

	return concurrency, filter, path, f.outputDir, f.verbose, f.allowMissingPath, nil
}

func runYq(ctx context.Context, path string, filter string, outputDir string, concurrency int, verbose bool, allowMissingPath bool) error {
	p := output.New(verbose)

	// Check if path exists
	if path == "" {
		return fmt.Errorf("path argument is required (use --help for usage)")
	}

	inputInfo, err := os.Stat(path)
	if err != nil {
		if allowMissingPath && os.IsNotExist(err) {
			return emptyResult(p, outputDir, path)
		}
		return fmt.Errorf("path does not exist: %s", path)
	}

	// Get filter from argument or environment variable
	if filter == "" {
		filter = os.Getenv("YQ_FILTER")
	}

	if filter == "" {
		return fmt.Errorf("filter is required: provide via YQ_FILTER environment variable or as argument")
	}

	// Determine output directory
	if outputDir == "" {
		// Default: <input-dir>/cleaned/
		if inputInfo.IsDir() {
			outputDir = filepath.Join(path, "cleaned")
		} else {
			// If single file, use parent dir + cleaned
			outputDir = filepath.Join(filepath.Dir(path), "cleaned")
		}
	}

	// Find all YAML files
	files, err := yq.FindManifests(path)
	if err != nil {
		return fmt.Errorf("failed to find manifests: %w", err)
	}

	if len(files) == 0 {
		if allowMissingPath {
			return emptyResult(p, outputDir, path)
		}
		return fmt.Errorf("no YAML files found in: %s", path)
	}

	// Always show found files count
	p.Info("Found %d files to process\n", len(files))

	// Show details only in verbose mode
	p.Verbose("Using %d workers\n", concurrency)
	p.Verbose("Output directory: %s\n", outputDir)
	p.Verbose("Filter: %s\n", filter)

	// Process files in parallel
	results := yq.ProcessFilesParallel(ctx, files, filter, outputDir, concurrency)

	// Report results
	successCount := 0
	failedCount := 0

	for _, result := range results {
		if result.Success {
			successCount++
			// Show progress only in verbose mode
			p.Verbose("✓ %s -> %s\n", result.InputPath, result.OutputPath)
		} else {
			failedCount++
			// Always show errors
			p.Info("✗ %s: %v\n", result.InputPath, result.Error)
		}
	}

	// Always show summary
	p.Info("Summary: %d succeeded, %d failed\n", successCount, failedCount)

	if failedCount > 0 {
		return fmt.Errorf("%d file(s) failed yq processing", failedCount)
	}

	return nil
}
