package orchestrator

import (
	"bytes"
	"context"
	"path/filepath"
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func testCluster(ctx context.Context, clusterPath string, opts test.TestOptions) ClusterResult {
	start := time.Now()
	result := ClusterResult{
		Name: filepath.Base(clusterPath),
	}

	// Update path for this cluster
	opts.Path = clusterPath

	// Capture output to a buffer instead of os.Stderr
	// This prevents output mixing and doesn't require mutex
	var outputBuf bytes.Buffer
	opts.Output = &outputBuf

	// Create test runner
	runner, err := test.NewTestRunner(ctx, opts)
	if err != nil {
		result.Error = err
		result.Passed = false
		result.Duration = time.Since(start)
		return result
	}

	// Run tests (output goes to buffer, not stderr)
	testResults, err := runner.Run(ctx)

	// Save captured output
	result.CapturedOutput = outputBuf.String()
	result.TestResults = testResults
	result.Passed = (err == nil && testResults.Failed == 0)
	result.Error = err
	result.Duration = time.Since(start)

	return result
}

// printClusterProgress reports the result of the index-th cluster (1-based)
// out of total. The index is passed explicitly instead of being reconstructed
// from a progress percentage, which lost precision on integer division.
func printClusterProgress(p *output.Printer, result ClusterResult, index int, total int) {
	if p.IsVerbose() {
		printClusterProgressVerbose(p, result, (index*100)/total)
	} else {
		printClusterProgressCompact(p, result, index, total)
	}
}

func printClusterProgressCompact(p *output.Printer, result ClusterResult, index int, total int) {
	p.Info("\n[%d/%d] %s\n", index, total, result.Name)

	// Non-test results (e.g. build) carry no TestResults — print a generic line
	if result.TestResults == nil {
		if result.Passed {
			p.Info("      ✓ done (%.2fs)\n", result.Duration.Seconds())
		} else {
			p.Info("      ✗ failed (%.2fs)\n", result.Duration.Seconds())
			if result.Error != nil {
				p.Info("        Error: %v\n", result.Error)
			}
		}
		return
	}

	if result.Passed {
		p.Info("      ✓ %d tests passed (%.2fs)\n", result.TestResults.Total, result.Duration.Seconds())
		return
	}

	testsCount := 0
	failedCount := 0
	passedCount := 0
	if result.TestResults != nil {
		testsCount = result.TestResults.Total
		failedCount = result.TestResults.Failed
		passedCount = result.TestResults.Passed
	}

	if failedCount > 0 {
		p.Info("      ✗ %d failed, %d passed (%.2fs)\n", failedCount, passedCount, result.Duration.Seconds())
		p.Info("        Failed tests:\n")
		if result.TestResults != nil {
			for _, testResult := range result.TestResults.Tests {
				if testResult.Status == test.TestFailed {
					p.Info("          ✗ %s\n", testResult.Name)
					if testResult.Error != nil {
						p.Info("            Error: %v\n", testResult.Error)
					}
				}
			}
		}
	} else if result.Error != nil {
		p.Info("      ✗ test failed (%.2fs)\n", result.Duration.Seconds())
		p.Info("        Error: %v\n", result.Error)
	} else {
		p.Info("      ✗ %d tests (%.2fs)\n", testsCount, result.Duration.Seconds())
	}
}

func printClusterProgressVerbose(p *output.Printer, result ClusterResult, progress int) {
	p.Info("\n========================================== %s [%3d%%] ==========================================\n",
		result.Name, progress)

	if result.CapturedOutput != "" {
		p.Info("%s", result.CapturedOutput)
	}

	// Non-test results (e.g. build) carry no TestResults — keep the label generic
	label := "test"
	if result.TestResults == nil {
		label = "cluster"
	}
	if result.Passed {
		p.Info("%s PASSED (%.2fs)\n", label, result.Duration.Seconds())
	} else {
		p.Info("%s FAILED (%.2fs)\n", label, result.Duration.Seconds())
		if result.Error != nil {
			p.Info("Error: %v\n", result.Error)
		}
	}
}
