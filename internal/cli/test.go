package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
	"github.com/alexandervidyaev/flux-tools/pkg/config"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func NewTestCmd() *cobra.Command {
	return newTestCmd(testCmdConfig{
		use: "test [path...]",
		long: `Check that a cluster's resources render, the way the cluster would render them.

Every Kustomization found becomes one test case: its spec.path is resolved,
.sourceignore is applied and kustomize build runs. With --enable-helm every
HelmRelease becomes one too, and helm template runs against the assembled
values. A case passes when the render succeeds and fails with the renderer's
own error, so this answers "does it build at all", not "are the fields right".
For the fields, run kubeconform over what build produced.

Cluster Secrets referenced through valuesFrom are not available here, so a
missing reference is replaced with a placeholder instead of failing the case.

Clusters run in parallel; --sequential forces one at a time. A chart that fails
to template is fatal unless --skip-failed-charts is given, and a Kustomization
with an OCIRepository source is an error unless --skip-oci is.

Examples:
  # One cluster, detailed output
  flux-tools test clusters/dev/kube-dev --enable-helm

  # Every cluster below a path, in parallel
  flux-tools test clusters/dev --enable-helm

  # A flux-operator layout: name the entries, or mark them
  flux-tools test operator/clusters/dev operator/clusters/qa
  flux-tools test --cluster-marker .flux-cluster operator/clusters/

  # JUnit XML for GitLab artifacts:reports:junit
  flux-tools test clusters/dev --enable-helm --junit-report report.xml
`,
	})
}

type testCmdConfig struct {
	use  string
	long string
}

func newTestCmd(c testCmdConfig) *cobra.Command {
	var (
		enableHelm       bool
		sequential       bool
		verbose          bool
		strict           bool
		skipFailedCharts bool
		skipOCI          bool
		cacheDir         string
		helmTimeout      int
		skipHelmPull     bool
		noTemplateCache  bool
		junitReport      string
		fluxWorkdir      string
		clusterMarker    string
	)

	cmd := &cobra.Command{
		Use:   c.use,
		Short: "Check that every Kustomization builds and every HelmRelease templates",
		Long:  c.long,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)

			// Resolve config from env
			cfg := config.LoadDefaults()
			if cacheDir == "" {
				cacheDir = cfg.CacheDir
			}
			if helmTimeout == 0 {
				helmTimeout = cfg.HelmTimeout
			}

			mode, clusters, rootPath, err := resolveEntries(args, fluxWorkdir, clusterMarker)
			if err != nil {
				return err
			}

			// Auto-pull Helm charts if enabled and not skipped
			if enableHelm && !skipHelmPull {
				if err := autoPullHelmCharts(ctx, clusters, rootPath, cacheDir, helmTimeout, verbose); err != nil {
					return fmt.Errorf("failed to pull helm charts: %w", err)
				}
			}

			opts := test.TestOptions{
				RootPath:         rootPath,
				EnableHelm:       enableHelm,
				Sequential:       sequential,
				Verbose:          verbose,
				Strict:           strict,
				SkipFailedCharts: skipFailedCharts,
				SkipOCI:          skipOCI,
				NoTemplateCache:  noTemplateCache,
				CacheDir:         cacheDir,
				HelmTimeout:      helmTimeout,
			}

			switch mode {
			case orchestrator.ModeSingle:
				return runTestSingle(ctx, clusters[0], opts, junitReport)
			case orchestrator.ModeMulti:
				return runTestMulti(ctx, clusters, opts, junitReport)
			default:
				return fmt.Errorf("invalid mode")
			}
		},
	}

	cmd.Flags().BoolVar(&enableHelm, "enable-helm", false, "Enable Helm processing")
	cmd.Flags().BoolVar(&sequential, "sequential", false, "Force sequential execution (escape hatch for parallel Helm testing)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	cmd.Flags().BoolVar(&strict, "strict", false, "Strict validation")
	cmd.Flags().BoolVar(&skipFailedCharts, "skip-failed-charts", false, "Skip charts that fail to template (by default, template errors are fatal)")
	cmd.Flags().BoolVar(&skipOCI, "skip-oci", false, "Skip Kustomizations with OCIRepository sourceRef (by default, OCI sources produce an error)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Helm cache directory")
	cmd.Flags().IntVar(&helmTimeout, "helm-timeout", 0, "Helm timeout in seconds")
	cmd.Flags().BoolVar(&skipHelmPull, "skip-helm-pull", false, "Skip automatic Helm chart pulling (only with --enable-helm)")
	cmd.Flags().BoolVar(&noTemplateCache, "no-template-cache", false, "Disable the persistent helm template cache in the cache directory")
	registerEntryFlags(cmd, &fluxWorkdir, &clusterMarker)
	cmd.Flags().StringVar(&junitReport, "junit-report", "", "Write test results as a JUnit XML report to the given path (for GitLab artifacts:reports:junit)")

	return cmd
}

func runTestSingle(ctx context.Context, clusterPath string, opts test.TestOptions, junitReport string) error {
	p := output.New(opts.Verbose)
	p.Info("Testing single cluster: %s\n\n", filepath.Base(clusterPath))

	opts.Path = clusterPath
	runner, err := test.NewTestRunner(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to create test runner: %w", err)
	}

	results, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("test execution failed: %w", err)
	}

	if junitReport != "" {
		// Wrap the single cluster into the aggregated format: one testsuite.
		passed := 0
		failed := 0
		if results.Failed > 0 {
			failed = 1
		} else {
			passed = 1
		}
		agg := &orchestrator.AggregatedResults{
			Clusters: []orchestrator.ClusterResult{{
				Name:        filepath.Base(clusterPath),
				Passed:      results.Failed == 0,
				TestResults: results,
				Duration:    results.Duration,
			}},
			Total:    1,
			Passed:   passed,
			Failed:   failed,
			Duration: results.Duration,
		}
		if err := orchestrator.WriteJUnitReport(junitReport, agg); err != nil {
			return err
		}
	}

	if results.Failed > 0 {
		return fmt.Errorf("%d test(s) failed", results.Failed)
	}
	return nil
}

func runTestMulti(ctx context.Context, clusters []string, opts test.TestOptions, junitReport string) error {
	p := output.New(opts.Verbose)

	// Verbose mode: show cluster list
	p.Verbose("Discovered %d cluster(s):\n", len(clusters))
	for i, cluster := range clusters {
		p.Verbose("  %d. %s\n", i+1, filepath.Base(cluster))
	}
	p.Verbose("\n")

	// Single cluster hint
	if len(clusters) == 1 && !p.IsVerbose() {
		p.Info("Note: Testing 1 cluster. For detailed output, use the cluster path directly or add --verbose\n")
		p.Info("  flux-tools test %s\n\n", clusters[0])
	}

	p.Info("Testing %d cluster(s) %s\n",
		len(clusters), getExecutionMode(opts.Sequential))

	var results *orchestrator.AggregatedResults
	var err error

	if opts.Sequential {
		// --sequential: escape hatch, run strictly one cluster at a time
		results, err = orchestrator.RunTestsSequential(ctx, clusters, opts)
	} else {
		// Parallel for both modes. Helm mode is parallel-safe: charts are
		// pre-pulled before the fan-out (autoPullHelmCharts), so templating
		// runs from the local cache without touching shared repo config;
		// the helm client itself is concurrency-safe since 1.5.
		results, err = orchestrator.RunTestsParallel(ctx, clusters, opts)
	}

	if err != nil {
		return fmt.Errorf("test execution failed: %w", err)
	}

	orchestrator.PrintTestResults(results, p)

	if junitReport != "" {
		if err := orchestrator.WriteJUnitReport(junitReport, results); err != nil {
			return err
		}
	}

	if results.Failed > 0 {
		return fmt.Errorf("%d of %d cluster(s) failed tests", results.Failed, results.Total)
	}
	return nil
}

func getExecutionMode(sequential bool) string {
	if sequential {
		return "(sequential execution)"
	}
	return "in parallel"
}
