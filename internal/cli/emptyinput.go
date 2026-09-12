package cli

import (
	"fmt"
	"os"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

// emptyResult is what --allow-missing-path does: create the output directory and
// stop, so the next stage of a pipeline finds an empty tree instead of a missing
// one.
//
// It exists because a diff has two legitimate empty baselines — nothing is
// deployed to the environment yet, and the environment is created by this very
// merge request — and in both the truthful diff is the whole configuration
// appearing. Without a way to say "empty is fine", each stage has to be guarded
// by a shell conditional in the consumer pipeline, which is what the flag
// removes: the stage runs unconditionally and produces nothing.
//
// The default stays an error. A missing input is normally a broken pipeline, and
// silently turning it into an empty diff would hide exactly the breakage the diff
// is supposed to reveal.
func emptyResult(p *output.Printer, outputDir, inputPath string) error {
	if outputDir == "" {
		return fmt.Errorf("--allow-missing-path needs an output directory (-o)")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", outputDir, err)
	}

	p.Info("No manifests in %s: empty result, created %s (--allow-missing-path)\n",
		inputPath, outputDir)
	return nil
}
