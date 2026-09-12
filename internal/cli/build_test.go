package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/internal/validator/build"
)

// fixtureRoot is the repository root of the shared cluster fixture: the
// spec.path values inside it are relative to this directory.
var fixtureRoot = filepath.Join("..", "orchestrator", "testdata", "cluster-repo")

// TestRunBuildMultiConcurrentNoRace builds two clusters in parallel. Its purpose
// is to be run under `go test -race`: before the fix each worker mutated a
// shared opts.Path, a write-write data race that could also build a cluster
// against another cluster's path. Each worker now copies opts, so the build is
// both race-free and correct.
//
// Skips when kustomize is unavailable, since the builder shells out to it.
func TestRunBuildMultiConcurrentNoRace(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	base := filepath.Join("..", "orchestrator", "testdata", "cluster-repo", "clusters", "dev")
	clusters := []string{
		filepath.Join(base, "cluster-a"),
		filepath.Join(base, "cluster-b"),
	}

	outputDir := t.TempDir()
	opts := build.BuildOptions{
		RootPath:       fixtureRoot,
		EnableHelm:     false,
		SkipFluxSystem: true,
		OutputFormat:   "yaml",
		CacheDir:       t.TempDir(),
	}

	if err := runBuildMulti(context.Background(), clusters, opts, outputDir, 2); err != nil {
		t.Fatalf("runBuildMulti: %v", err)
	}

	for _, c := range clusters {
		name := filepath.Base(c)
		info, err := os.Stat(filepath.Join(outputDir, name+".yaml"))
		if err != nil {
			t.Errorf("output for %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("output for %s is empty", name)
		}
	}
}

// TestRunBuildMultiSkipKind builds a testdata cluster with --skip-kind
// HelmRelease semantics and checks that HelmRelease objects disappear from
// the output while everything else stays.
func TestRunBuildMultiSkipKind(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	cluster := filepath.Join("..", "orchestrator", "testdata", "cluster-repo", "clusters", "dev", "cluster-a")

	outputDir := t.TempDir()
	opts := build.BuildOptions{
		RootPath:       fixtureRoot,
		SkipFluxSystem: true,
		// Case differs from the manifest's `kind: HelmRelease` on purpose:
		// matching must be case-insensitive.
		SkipKinds:    []string{"helmrelease"},
		OutputFormat: "yaml",
		CacheDir:     t.TempDir(),
	}

	if err := runBuildMulti(context.Background(), []string{cluster}, opts, outputDir, 1); err != nil {
		t.Fatalf("runBuildMulti: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "cluster-a.yaml"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	out := string(data)

	if strings.Contains(out, "kind: HelmRelease") {
		t.Errorf("HelmRelease should be filtered out by SkipKinds, output:\n%s", out)
	}
	// The rest of the cluster must still be there.
	for _, want := range []string{"kind: Namespace", "kind: HelmRepository"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q to survive SkipKinds filtering, output:\n%s", want, out)
		}
	}
}

// TestRunBuildMultiSliced builds a testdata cluster in sliced output mode and
// checks the resulting file tree: one file per object at the path rendered
// from the default kubectl-slice compatible template.
func TestRunBuildMultiSliced(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	cluster := filepath.Join("..", "orchestrator", "testdata", "cluster-repo", "clusters", "dev", "cluster-a")

	outputDir := t.TempDir()
	opts := build.BuildOptions{
		RootPath:       fixtureRoot,
		SkipFluxSystem: true,
		OutputFormat:   string(build.OutputFormatSliced),
		SliceTemplate:  build.DefaultSliceTemplate,
		CacheDir:       t.TempDir(),
	}

	if err := runBuildMulti(context.Background(), []string{cluster}, opts, outputDir, 1); err != nil {
		t.Fatalf("runBuildMulti: %v", err)
	}

	clusterDir := filepath.Join(outputDir, "cluster-a")
	var got []string
	err := filepath.Walk(clusterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(clusterDir, path)
			if relErr != nil {
				return relErr
			}
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", clusterDir, err)
	}
	sort.Strings(got)

	want := []string{
		"Namespace:/Kind:namespace/Name:sample.yaml",
		"Namespace:sample/Kind:helmrelease/Name:sample.yaml",
		"Namespace:sample/Kind:helmrepository/Name:sample.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("sliced tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sliced tree = %v, want %v", got, want)
		}
	}

	// Each file is a single YAML document: no leading separator, exactly one
	// trailing newline — byte-compatible with kubectl-slice output.
	data, err := os.ReadFile(filepath.Join(clusterDir, filepath.FromSlash(want[0])))
	if err != nil {
		t.Fatalf("read sliced file: %v", err)
	}
	content := string(data)
	if strings.HasPrefix(content, "---") {
		t.Errorf("sliced file should not start with a document separator:\n%s", content)
	}
	if !strings.HasSuffix(content, "\n") || strings.HasSuffix(content, "\n\n") {
		t.Errorf("sliced file should end with exactly one newline, got %q", content)
	}
}
