package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
)

const (
	fluxWorkdirFlag = "flux-workdir"

	fluxWorkdirUsage = "Repository root: the directory that spec.path values in Kustomizations resolve against. " +
		"This is the repository checkout, not the flux-system/ directory"

	clusterMarkerUsage = "Name of a file or directory marking a cluster entry, in addition to flux-system/. " +
		"Give one to have flux-operator layouts discovered the way bootstrapped ones are"
)

// resolveEntries turns the positional paths of test/build/helm-pull into the
// cluster directories to process and the repository root their spec.path values
// resolve against.
//
// The root is always explicit: --flux-workdir, which defaults to the working
// directory. Which paths are clusters is decided by orchestrator.ResolveInput
// and does not depend on that flag.
func resolveEntries(args []string, fluxWorkdir, clusterMarker string) (orchestrator.Mode, []string, string, error) {
	if fluxWorkdir == "" {
		fluxWorkdir = "."
	}

	rootAbs, err := filepath.Abs(fluxWorkdir)
	if err != nil {
		return orchestrator.ModeInvalid, nil, "", fmt.Errorf("invalid --%s %q: %w", fluxWorkdirFlag, fluxWorkdir, err)
	}
	if info, err := os.Stat(rootAbs); err != nil || !info.IsDir() {
		return orchestrator.ModeInvalid, nil, "", fmt.Errorf("--%s %s is not a directory", fluxWorkdirFlag, rootAbs)
	}

	markers := orchestrator.DefaultMarkers().With(clusterMarker)

	var clusters []string
	for _, arg := range args {
		_, found, err := orchestrator.ResolveInput(arg, markers)
		if err != nil {
			return orchestrator.ModeInvalid, nil, "", err
		}
		for _, cluster := range found {
			if !within(rootAbs, cluster) {
				return orchestrator.ModeInvalid, nil, "", fmt.Errorf(
					"cluster %s is outside --%s %s; spec.path is resolved against the repository root, so both have to be the same checkout",
					cluster, fluxWorkdirFlag, rootAbs)
			}
		}
		clusters = append(clusters, found...)
	}

	return modeFor(clusters), clusters, rootAbs, nil
}

// within reports whether path is root itself or sits below it.
func within(root, path string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

func modeFor(clusters []string) orchestrator.Mode {
	if len(clusters) == 1 {
		return orchestrator.ModeSingle
	}
	return orchestrator.ModeMulti
}

// registerEntryFlags adds the two flags every path-taking command shares: the
// repository root and the extra cluster marker.
func registerEntryFlags(cmd *cobra.Command, fluxWorkdir, clusterMarker *string) {
	cmd.Flags().StringVar(fluxWorkdir, fluxWorkdirFlag, ".", fluxWorkdirUsage)
	cmd.Flags().StringVar(clusterMarker, "cluster-marker", "", clusterMarkerUsage)
}
