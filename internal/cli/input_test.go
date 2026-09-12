package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alexandervidyaev/flux-tools/internal/orchestrator"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
}

// Entries that carry no marker are taken as the caller listed them, which is
// how a flux-operator layout is built.
func TestResolveEntriesTakesUnmarkedPathsAsGiven(t *testing.T) {
	root := t.TempDir()
	prod := filepath.Join(root, "kubernetes", "prod")
	staging := filepath.Join(root, "kubernetes", "staging")
	mkdirs(t, prod, staging)

	mode, clusters, gotRoot, err := resolveEntries([]string{prod, staging}, root, "")
	if err != nil {
		t.Fatalf("resolveEntries: %v", err)
	}
	if mode != orchestrator.ModeMulti {
		t.Errorf("mode = %v, want ModeMulti", mode)
	}
	if len(clusters) != 2 || clusters[0] != prod || clusters[1] != staging {
		t.Errorf("clusters = %v", clusters)
	}
	if gotRoot != root {
		t.Errorf("root = %q, want %q", gotRoot, root)
	}
}

// A bootstrapped tree is still discovered by its flux-system/ markers, and the
// repository root no longer depends on any of that.
func TestResolveEntriesDiscoversBootstrappedClusters(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "clusters", "a")
	b := filepath.Join(root, "clusters", "b")
	mkdirs(t, filepath.Join(a, "flux-system"), filepath.Join(b, "flux-system"))

	mode, clusters, gotRoot, err := resolveEntries([]string{filepath.Join(root, "clusters")}, root, "")
	if err != nil {
		t.Fatalf("resolveEntries: %v", err)
	}
	if mode != orchestrator.ModeMulti || len(clusters) != 2 {
		t.Errorf("mode = %v, clusters = %v", mode, clusters)
	}
	if gotRoot != root {
		t.Errorf("root = %q, want %q", gotRoot, root)
	}
}

// The marker named on the command line is passed through, so an operator
// layout is discovered the same way a bootstrapped one is.
func TestResolveEntriesPassesTheClusterMarkerThrough(t *testing.T) {
	root := t.TempDir()
	set := filepath.Join(root, "operator", "clusters")
	for _, name := range []string{"dev", "qa"} {
		dir := filepath.Join(set, name)
		mkdirs(t, dir)
		if err := os.WriteFile(filepath.Join(dir, ".flux-cluster"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mode, clusters, _, err := resolveEntries([]string{set}, root, ".flux-cluster")
	if err != nil {
		t.Fatalf("resolveEntries: %v", err)
	}
	if mode != orchestrator.ModeMulti || len(clusters) != 2 {
		t.Errorf("mode = %v, clusters = %v", mode, clusters)
	}
}

// An empty flag value means the working directory, so running from the
// repository root needs no flag at all.
func TestResolveEntriesDefaultsToTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	mkdirs(t, filepath.Join(cluster, "flux-system"))

	t.Chdir(root)

	_, clusters, gotRoot, err := resolveEntries([]string{"clusters/prod"}, "", "")
	if err != nil {
		t.Fatalf("resolveEntries: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %v", clusters)
	}
	// t.TempDir can sit behind a symlink (/var -> /private/var on macOS), so
	// compare what the resolver produced against the same resolution.
	wantRoot, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot {
		t.Errorf("root = %q, want %q", gotRoot, wantRoot)
	}
}

// spec.path is resolved against the repository root, so an entry from another
// checkout cannot be built against this one.
func TestResolveEntriesRejectsEntryOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()

	_, _, _, err := resolveEntries([]string{other}, root, "")
	if err == nil || !strings.Contains(err.Error(), "outside --"+fluxWorkdirFlag) {
		t.Errorf("err = %v, want it to name --%s", err, fluxWorkdirFlag)
	}
}

// The flag names are the contract with every pipeline that calls this tool, so
// they are pinned: the root defaults to the working directory and the marker is
// opt-in.
func TestRegisterEntryFlags(t *testing.T) {
	var workdir, marker string
	cmd := &cobra.Command{Use: "x"}
	registerEntryFlags(cmd, &workdir, &marker)

	workdirFlag := cmd.Flags().Lookup(fluxWorkdirFlag)
	if workdirFlag == nil {
		t.Fatalf("--%s is not registered", fluxWorkdirFlag)
	}
	if workdirFlag.DefValue != "." {
		t.Errorf("--%s default = %q, want %q", fluxWorkdirFlag, workdirFlag.DefValue, ".")
	}

	if cmd.Flags().Lookup("cluster-marker") == nil {
		t.Error("--cluster-marker is not registered")
	}

	// Nothing legacy survives: --root was never in a public release.
	if cmd.Flags().Lookup("root") != nil {
		t.Error("--root must be gone, not deprecated")
	}
}
