package build

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// realworld_test.go exercises BuildAll against vendored manifest fixtures
// that mirror the two real layouts the tool is invoked against in CI:
//
//   - a tree without flux-system/ (kargo-style, charts/<chart>/ +
//     releases/<env>/<cluster>/), built with an explicit --root;
//   - a Flux-bootstrapped tree (clusters/<env>/<cluster>/flux-system/), built
//     as `flux-tools build clusters/<env>/<cluster>` with the root derived
//     from the flux-system Kustomization.
//
// Tests skip when the kustomize binary is unavailable, since the builder
// shells out to it. Helm processing is intentionally disabled — the fixtures
// reference unreachable OCI charts.

const fixturesRel = "../../orchestrator/testdata"

func TestBuildSourceFixture(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	fixturePath := filepath.Join(fixturesRel, "source-repo", "releases", "stable", "cluster-a")

	// No flux-system/ here: the root is what a caller passes with --root.
	builder, err := NewBuilder(context.Background(), BuildOptions{
		Path:                  fixturePath,
		RootPath:              filepath.Join(fixturesRel, "source-repo"),
		EnableHelm:            false,
		CacheDir:              t.TempDir(),
		TolerateMissingValues: true,
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	objects, err := builder.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	// In source mode without inner Kustomization CRs, BuildAll deliberately
	// strips Flux source/release CRDs and keeps only "regular" Kubernetes
	// objects. We expect to see the Namespace from the chart wrapper.
	gotKinds := make([]string, 0, len(objects))
	for _, obj := range objects {
		gotKinds = append(gotKinds, obj.GetKind()+"/"+obj.GetName())
	}
	sort.Strings(gotKinds)

	want := []string{"Namespace/kargo"}
	if !equalStrings(gotKinds, want) {
		t.Errorf("kinds = %v, want %v", gotKinds, want)
	}
}

func TestBuildClusterFixture(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	fixturePath := filepath.Join(fixturesRel, "cluster-repo", "clusters", "dev", "cluster-a")

	builder, err := NewBuilder(context.Background(), BuildOptions{
		Path:           fixturePath,
		RootPath:       filepath.Join(fixturesRel, "cluster-repo"),
		EnableHelm:     false,
		CacheDir:       t.TempDir(),
		SkipFluxSystem: true,
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	objects, err := builder.BuildAll(context.Background())
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	// In cluster mode with Flux Kustomization CRs the builder follows
	// spec.path, runs kustomize on the referenced directory, and surfaces the
	// resulting resources. With the sample chart wired in we expect the
	// Namespace plus both Flux source/release CRs to come through (helm
	// templating is disabled).
	gotKeys := make(map[string]bool)
	for _, obj := range objects {
		gotKeys[obj.GetKind()+"/"+obj.GetName()] = true
	}

	for _, want := range []string{
		"Namespace/sample",
		"HelmRepository/sample",
		"HelmRelease/sample",
	} {
		if !gotKeys[want] {
			t.Errorf("expected %s not found in build output; got %v", want, keysOf(gotKeys))
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
