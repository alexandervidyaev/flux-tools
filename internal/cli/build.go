package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
	"github.com/alexandervidyaev/flux-tools/pkg/config"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// NewBuildCmd creates `flux-tools build`: every Kustomization and HelmRelease
// of every given cluster rendered into plain manifests, in one go. There is no
// per-object variant; the whole cluster is the unit.
func NewBuildCmd() *cobra.Command {
	return newBuildCmd(buildCmdConfig{
		long: `Render every Kubernetes manifest of the given clusters: every Kustomization is
built, every HelmRelease is templated (with --enable-helm), and the result is
written per cluster. All objects at once; there is no way to build one object.

Output:
- Single cluster: ./cluster-manifests/<cluster-name>.yaml
- Several clusters: one file per cluster in ./cluster-manifests/

Output directory priority (highest to lowest):
1. --output-dir flag
2. FLUX_TOOLS_GENERATED_MANIFESTS_DIR environment variable
3. Default: ./cluster-manifests

With --output sliced, each cluster is written as a directory tree instead of
a single file: every object goes to <output-dir>/<cluster>/<name rendered by
--slice-template>. The template and the resulting tree are compatible with
kubectl-slice, so the tree can be fed to anything expecting that layout. Kinds
can be dropped from the output with --skip-kind (repeatable, case-insensitive).

diff accepts either shape, so --output sliced is a choice about the artifact
you want to keep, not something the comparison needs.

Examples:
  # Single cluster (saves to file)
  flux-tools build clusters/dev/kube-dev --enable-helm
  # Output: ./cluster-manifests/kube-dev.yaml

  # Every cluster under a directory
  flux-tools build clusters/dev --enable-helm
  # Output: ./cluster-manifests/*.yaml

  # flux-operator layout: no flux-system/ in git, entries listed
  flux-tools build operator/clusters/dev operator/clusters/qa

  # Custom output directory via ENV
  FLUX_TOOLS_GENERATED_MANIFESTS_DIR=/tmp/manifests-main flux-tools build clusters/dev --enable-helm

  # Custom output directory via flag (overrides ENV)
  flux-tools build clusters/dev --enable-helm --output-dir /tmp/manifests-feature

  # Drop a kind and keep one file per object as the CI artifact
  flux-tools build clusters/dev --enable-helm --skip-kind HelmRelease --output sliced
  # Output: ./cluster-manifests/<cluster>/Namespace:<ns>/Kind:<kind>/Name:<name>.yaml
`,
	})
}

type buildCmdConfig struct {
	long string
}

func newBuildCmd(c buildCmdConfig) *cobra.Command {
	var (
		enableHelm       bool
		verbose          bool
		strict           bool
		skipCRDs         bool
		skipSecrets      bool
		skipFluxSystem   bool
		skipFailedCharts bool
		skipOCI          bool
		skipKinds        []string
		outputFormat     string
		sliceTemplate    string
		outputDir        string
		allowMissingPath bool
		cacheDir         string
		helmTimeout      int
		skipHelmPull     bool
		noTemplateCache  bool
		concurrency      int
		substitute       []string
		cpuProfile       string
		fluxWorkdir      string
		clusterMarker    string
	)

	cmd := &cobra.Command{
		Use:   "build [path...]",
		Short: "Render every manifest of the given clusters",
		Long:  c.long,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)

			// The command used to be `build all`; a literal "all" that is not a
			// directory is that old spelling, not a path.
			if args[0] == "all" {
				if _, statErr := os.Stat("all"); os.IsNotExist(statErr) {
					return fmt.Errorf("`build all` is now `build`: flux-tools build <path...>")
				}
			}

			// CPU profiling for performance analysis (I-9). Covers the whole
			// run: helm pull, builds and serialization.
			if cpuProfile != "" {
				f, err := os.Create(cpuProfile)
				if err != nil {
					return fmt.Errorf("failed to create CPU profile file: %w", err)
				}
				defer f.Close()
				if err := pprof.StartCPUProfile(f); err != nil {
					return fmt.Errorf("failed to start CPU profile: %w", err)
				}
				defer pprof.StopCPUProfile()
			}

			// Resolve config from env
			cfg := config.LoadDefaults()
			if cacheDir == "" {
				cacheDir = cfg.CacheDir
			}
			if helmTimeout == 0 {
				helmTimeout = cfg.HelmTimeout
			}

			// An empty baseline is a legitimate outcome, not a broken pipeline:
			// see emptyResult. The check is before the resolver because the
			// resolver's job is to classify a tree that exists.
			if allowMissingPath {
				var present []string
				for _, path := range args {
					if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
						present = append(present, path)
					}
				}
				if len(present) == 0 {
					return emptyResult(output.New(verbose), getManifestsDir(outputDir), strings.Join(args, ", "))
				}
				args = present
			}

			_, clusters, rootPath, err := resolveEntries(args, fluxWorkdir, clusterMarker)
			if err != nil {
				return err
			}

			// Auto-pull Helm charts if enabled and not skipped
			if enableHelm && !skipHelmPull {
				if err := autoPullHelmCharts(ctx, clusters, rootPath, cacheDir, helmTimeout, verbose); err != nil {
					return fmt.Errorf("failed to pull helm charts: %w", err)
				}
			}

			globalSub := map[string]string{}
			for _, kv := range substitute {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("invalid --substitute %q, expected key=value", kv)
				}
				globalSub[k] = v
			}

			opts := build.BuildOptions{
				RootPath:         rootPath,
				EnableHelm:       enableHelm,
				Verbose:          verbose,
				Strict:           strict,
				SkipCRDs:         skipCRDs,
				SkipSecrets:      skipSecrets,
				SkipFluxSystem:   skipFluxSystem,
				SkipFailedCharts: skipFailedCharts,
				SkipOCI:          skipOCI,
				SkipKinds:        skipKinds,
				NoTemplateCache:  noTemplateCache,
				OutputFormat:     outputFormat,
				SliceTemplate:    sliceTemplate,
				CacheDir:         cacheDir,
				HelmTimeout:      helmTimeout,
				GlobalSubstitute: globalSub,
				// Shared across all cluster builds so stage durations are
				// aggregated over the whole run.
				Timers: output.NewStageTimer(),
			}

			return runBuildMulti(ctx, clusters, opts, outputDir, concurrency)
		},
	}

	cmd.Flags().BoolVar(&enableHelm, "enable-helm", false, "Enable Helm processing")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	cmd.Flags().BoolVar(&strict, "strict", false, "Strict mode")
	cmd.Flags().BoolVar(&skipCRDs, "skip-crds", false, "Skip CRDs")
	cmd.Flags().BoolVar(&skipSecrets, "skip-secrets", false, "Skip Secrets")
	cmd.Flags().BoolVar(&skipFluxSystem, "skip-flux-system", false, "Skip flux-system objects")
	cmd.Flags().BoolVar(&skipFailedCharts, "skip-failed-charts", false, "Skip charts that fail to template (by default, template errors are fatal)")
	cmd.Flags().BoolVar(&skipOCI, "skip-oci", false, "Skip Kustomizations with OCIRepository sourceRef (by default, OCI sources produce an error)")
	cmd.Flags().StringArrayVar(&skipKinds, "skip-kind", nil, "Skip objects of the given kind, case-insensitive (repeatable); replaces the yq 'select(.kind != ...)' CI step")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "yaml", "Output format (yaml, json, sliced)")
	cmd.Flags().StringVar(&sliceTemplate, "slice-template", build.DefaultSliceTemplate, "File name template for --output sliced (kubectl-slice compatible; supports .kind, .apiVersion, .metadata.name, .metadata.namespace and the lower/dottodash functions)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Output directory for manifests (default: FLUX_TOOLS_GENERATED_MANIFESTS_DIR or ./cluster-manifests)")
	registerEntryFlags(cmd, &fluxWorkdir, &clusterMarker)
	cmd.Flags().BoolVar(&allowMissingPath, "allow-missing-path", false, "A missing path produces an empty result instead of an error (for a diff baseline that does not exist yet)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Helm cache directory")
	cmd.Flags().IntVar(&helmTimeout, "helm-timeout", 0, "Helm timeout in seconds")
	cmd.Flags().BoolVar(&skipHelmPull, "skip-helm-pull", false, "Skip automatic Helm chart pulling (only with --enable-helm)")
	cmd.Flags().BoolVar(&noTemplateCache, "no-template-cache", false, "Disable the persistent helm template cache in the cache directory")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "j", 0, "Number of parallel builds (default: number of clusters, 0 = unlimited)")
	cmd.Flags().StringArrayVar(&substitute, "substitute", nil, "Post-build substitution variable key=value (repeatable); applied to all built objects")
	cmd.Flags().StringVar(&cpuProfile, "cpuprofile", "", "Write CPU profile to file (for performance analysis)")

	return cmd
}

func runBuildMulti(ctx context.Context, clusters []string, opts build.BuildOptions, outputDirFlag string, concurrencyLimit int) error {
	outputDir := getManifestsDir(outputDirFlag)
	p := output.New(opts.Verbose)

	// Ensure a shared stage timer exists so per-cluster builds aggregate into
	// one summary (callers normally pass it via opts).
	if opts.Timers == nil {
		opts.Timers = output.NewStageTimer()
	}

	slicer, err := newSlicer(opts)
	if err != nil {
		return err
	}

	// Show cluster list
	p.Info("\nBuilding %d cluster(s):\n", len(clusters))
	for i, cluster := range clusters {
		clusterName := filepath.Base(cluster)
		p.Info("  %d. %s\n", i+1, clusterName)
	}
	p.Info("\n")
	p.Info("Output directory: %s\n\n", outputDir)

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Determine concurrency
	concurrency := len(clusters)
	if concurrencyLimit > 0 && concurrencyLimit < concurrency {
		concurrency = concurrencyLimit
	}

	p.Info("Building %d cluster(s) in parallel (concurrency: %d)\n\n", len(clusters), concurrency)

	results := orchestrator.RunOverClusters(ctx, clusters, concurrency, opts.Verbose,
		func(ctx context.Context, clusterPath string) orchestrator.ClusterResult {
			return buildOneCluster(ctx, clusterPath, opts, outputDir, slicer)
		})

	return reportBuildResults(p, results, opts)
}

// newSlicer parses the sliced-mode file name template once up front, so a
// broken template fails fast instead of once per cluster. Returns nil when the
// output format is not sliced.
func newSlicer(opts build.BuildOptions) (*build.Slicer, error) {
	if build.OutputFormat(opts.OutputFormat) != build.OutputFormatSliced {
		return nil, nil
	}

	tpl := opts.SliceTemplate
	if tpl == "" {
		tpl = build.DefaultSliceTemplate
	}
	return build.NewSlicer(tpl)
}

// buildOneCluster renders a single cluster and writes it out. It runs on a
// worker, so it takes opts by value: mutating the shared opts.Path directly
// races and can build a cluster against another cluster's path.
func buildOneCluster(ctx context.Context, clusterPath string, opts build.BuildOptions, outputDir string, slicer *build.Slicer) orchestrator.ClusterResult {
	start := time.Now()
	clusterName := filepath.Base(clusterPath)
	result := orchestrator.ClusterResult{Name: clusterName}

	fail := func(err error) orchestrator.ClusterResult {
		result.Error = err
		result.Duration = time.Since(start)
		return result
	}

	opts.Path = clusterPath
	builder, err := build.NewBuilder(ctx, opts)
	if err != nil {
		return fail(err)
	}

	objects, err := builder.BuildAll(ctx)
	if err != nil {
		return fail(err)
	}

	// Serialize and write: stream objects straight into the file
	// instead of assembling the whole cluster in memory (I-6). The
	// serialize stage timer covers serialization plus file write.
	// Sliced mode writes a per-object file tree under a directory
	// named after the cluster instead of a single <cluster>.yaml.
	stopSerialize := opts.Timers.Start(build.StageSerialize)
	if slicer != nil {
		err = build.WriteSlicedObjects(filepath.Join(outputDir, clusterName), objects, slicer)
	} else {
		outputFile := filepath.Join(outputDir, clusterName+".yaml")
		err = writeClusterManifests(outputFile, objects, build.OutputFormat(opts.OutputFormat))
	}
	stopSerialize()
	if err != nil {
		return fail(err)
	}

	result.Passed = true
	result.Duration = time.Since(start)
	return result
}

// reportBuildResults prints the failure list, the totals line and the stage
// timing summary, and turns a non-zero failure count into the command error.
func reportBuildResults(p *output.Printer, results *orchestrator.AggregatedResults, opts build.BuildOptions) error {
	if results.Failed > 0 {
		p.Info("\nFailed clusters:\n")
		for _, result := range results.Clusters {
			if result.Error != nil {
				p.Info("  ✗ %s: %v\n", result.Name, result.Error)
			}
		}
	}

	p.Info("\n============================================== ")
	if results.Failed > 0 {
		p.Info("%d failed, ", results.Failed)
	}
	p.Info("%d built ==============================================\n", results.Passed)

	// Stage timing summary (I-9): per-stage lines in verbose mode,
	// a single totals line always.
	if summary := opts.Timers.Summary(build.StageOrder); summary != "" {
		if opts.Verbose {
			p.Verbose("\nStage timings (aggregated across clusters):\n")
			for _, stage := range build.StageOrder {
				if d, ok := opts.Timers.Duration(stage); ok {
					p.Verbose("  %-17s %s\n", stage, d.Round(time.Millisecond))
				}
			}
		}
		p.Info("Stages: %s\n", summary)
	}

	if results.Failed > 0 {
		return fmt.Errorf("%d cluster(s) failed to build", results.Failed)
	}
	return nil
}

// writeClusterManifests streams serialized objects into path through a
// buffered writer, so the full manifest set never has to be assembled in
// memory (I-6). Flush and Close failures are reported as write errors.
func writeClusterManifests(path string, objects []*unstructured.Unstructured, format build.OutputFormat) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create output file %s: %w", path, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close output file %s: %w", path, closeErr)
		}
	}()

	w := bufio.NewWriter(f)
	if err := build.SerializeObjectsTo(w, objects, format); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("failed to write output file %s: %w", path, err)
	}
	return nil
}

func getManifestsDir(outputDirFlag string) string {
	// Priority: CLI flag > ENV > default
	if outputDirFlag != "" {
		return outputDirFlag
	}
	if dir := os.Getenv("FLUX_TOOLS_GENERATED_MANIFESTS_DIR"); dir != "" {
		return dir
	}
	return "./cluster-manifests"
}
