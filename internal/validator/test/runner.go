package test

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
)

// TestRunner executes tests
type TestRunner struct {
	options TestOptions
	builder *build.Builder
	output  io.Writer
}

// NewTestRunner creates a new test runner
func NewTestRunner(ctx context.Context, options TestOptions) (*TestRunner, error) {
	// Create builder with options
	builderOpts := build.BuildOptions{
		Path:             options.Path,
		RootPath:         options.RootPath,
		EnableHelm:       options.EnableHelm,
		Verbose:          options.Verbose,
		Strict:           options.Strict,
		SkipCRDs:         options.SkipCRDs,
		SkipSecrets:      options.SkipSecrets,
		SkipFluxSystem:   options.SkipFluxSystem,
		SkipFailedCharts: options.SkipFailedCharts,
		SkipOCI:          options.SkipOCI,
		NoTemplateCache:  options.NoTemplateCache,
		OutputFormat:     "yaml",
		CacheDir:         options.CacheDir,
		HelmTimeout:      options.HelmTimeout,
	}

	builder, err := build.NewBuilder(ctx, builderOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	// Set output destination (default to os.Stderr)
	output := options.Output
	if output == nil {
		output = os.Stderr
	}

	return &TestRunner{
		options: options,
		builder: builder,
		output:  output,
	}, nil
}

// Run executes all tests
func (r *TestRunner) Run(ctx context.Context) (*TestResults, error) {
	// 1. Discover tests
	tests, err := r.DiscoverTests(ctx)
	if err != nil {
		return nil, fmt.Errorf("test discovery failed: %w", err)
	}

	if len(tests) == 0 {
		return &TestResults{Total: 0}, nil
	}

	results := &TestResults{
		Total: len(tests),
		Tests: make([]TestResult, 0, len(tests)),
	}

	start := time.Now()

	// 2. Print header
	fmt.Fprintf(r.output, "============================================= test session starts =============================================\n")
	fmt.Fprintf(r.output, "collected %d items\n\n", len(tests))

	// 3. Run tests
	for i, test := range tests {
		// Always print test name
		fmt.Fprintf(r.output, "%s ", test.Name())

		result := test.Run(ctx)
		results.Tests = append(results.Tests, result)

		switch result.Status {
		case TestPassed:
			results.Passed++
			fmt.Fprintf(r.output, "PASSED")
			if r.options.Verbose {
				fmt.Fprintf(r.output, " [%3d%%]\n", (i+1)*100/len(tests))
				if result.Objects > 0 {
					fmt.Fprintf(r.output, "  Generated %d objects\n", result.Objects)
				}
			} else {
				fmt.Fprintf(r.output, " [%3d%%]\n", (i+1)*100/len(tests))
			}
		case TestFailed:
			results.Failed++
			fmt.Fprintf(r.output, "FAILED [%3d%%]\n", (i+1)*100/len(tests))
		case TestSkipped:
			results.Skipped++
			fmt.Fprintf(r.output, "SKIPPED [%3d%%]\n", (i+1)*100/len(tests))
		}
	}

	results.Duration = time.Since(start)

	// 4. Print failures
	if results.Failed > 0 {
		fmt.Fprintf(r.output, "\n============================================= FAILURES ==============================================\n")
		for _, result := range results.Tests {
			if result.Status == TestFailed {
				fmt.Fprintf(r.output, "\n_________________________________________ %s _________________________________________\n", result.Name)
				fmt.Fprintf(r.output, "Type: %s\n", result.Type)
				fmt.Fprintf(r.output, "Path: %s\n", result.Path)
				fmt.Fprintf(r.output, "Error: %v\n", result.Error)
			}
		}
	}

	// 5. Print summary
	fmt.Fprintf(r.output, "\n============================================== ")
	if results.Failed > 0 {
		fmt.Fprintf(r.output, "%d failed, ", results.Failed)
	}
	fmt.Fprintf(r.output, "%d passed", results.Passed)
	if results.Skipped > 0 {
		fmt.Fprintf(r.output, ", %d skipped", results.Skipped)
	}
	fmt.Fprintf(r.output, " in %.2fs ==============================================\n", results.Duration.Seconds())

	return results, nil
}
