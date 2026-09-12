package orchestrator

import (
	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// PrintTestResults prints the aggregated test results.
func PrintTestResults(results *AggregatedResults, p *output.Printer) {
	p.Info("\n============================================== ")
	if results.Failed > 0 {
		p.Info("%d failed, ", results.Failed)
	}
	p.Info("%d passed in %.2fs ==============================================\n",
		results.Passed, results.Duration.Seconds())
}
