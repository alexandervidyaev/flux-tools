package orchestrator

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func mk(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
}

// Only flux-system/ makes a cluster. Directory names carry no meaning, hidden
// directories are skipped, and nothing else is.
func TestResolveInputFindsClustersByMarkerOnly(t *testing.T) {
	root := t.TempDir()
	prod := filepath.Join(root, "clusters", "prod")
	staging := filepath.Join(root, "clusters", "eu", "staging")
	mk(t,
		filepath.Join(prod, "flux-system"),
		filepath.Join(staging, "flux-system"),
		filepath.Join(root, "clusters", "base", "apps"),
		filepath.Join(root, "clusters", "helm-charts", "nginx"),
		filepath.Join(root, "clusters", ".github", "flux-system"),
		filepath.Join(root, "clusters", "infrastructure-components", "flux-system-notes"),
	)

	mode, clusters, err := ResolveInput(filepath.Join(root, "clusters"), DefaultMarkers())
	if err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	if mode != ModeMulti {
		t.Errorf("mode = %v, want ModeMulti", mode)
	}
	sort.Strings(clusters)
	want := []string{staging, prod}
	sort.Strings(want)
	if strings.Join(clusters, ",") != strings.Join(want, ",") {
		t.Errorf("clusters = %v, want %v", clusters, want)
	}
}

func TestResolveInputSingleCluster(t *testing.T) {
	root := t.TempDir()
	prod := filepath.Join(root, "anything", "at-all")
	mk(t, filepath.Join(prod, "flux-system"))

	mode, clusters, err := ResolveInput(prod, DefaultMarkers())
	if err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	if mode != ModeSingle || len(clusters) != 1 || clusters[0] != prod {
		t.Errorf("mode = %v, clusters = %v", mode, clusters)
	}
}

// A path with no marker anywhere below it is taken as the caller gave it. That
// is what lets a flux-operator layout, which keeps the sync source in the
// cluster and so has no flux-system/ in git, be built by naming its entries.
func TestResolveInputWithoutMarkerTakesThePathAsGiven(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "operator", "clusters", "dev")
	mk(t, entry)
	writeYAML(t, filepath.Join(entry, "apps.yaml"))

	mode, clusters, err := ResolveInput(entry, DefaultMarkers())
	if err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	if mode != ModeSingle || len(clusters) != 1 || clusters[0] != entry {
		t.Errorf("mode = %v, clusters = %v", mode, clusters)
	}
}

// Taking the path as given is refused when the path plainly holds clusters
// rather than manifests of its own: rendering it as one entry names the result
// after the wrong directory and merges the clusters into it.
func TestResolveInputRefusesAnUnmarkedClusterSet(t *testing.T) {
	set := filepath.Join(t.TempDir(), "operator", "clusters")
	for _, name := range []string{"dev", "qa"} {
		dir := filepath.Join(set, name)
		mk(t, dir)
		writeYAML(t, filepath.Join(dir, "apps.yaml"))
	}

	_, _, err := ResolveInput(set, DefaultMarkers())
	if err == nil {
		t.Fatal("want a refusal for a directory of unmarked clusters")
	}
	for _, want := range []string{"looks like a set of clusters", "--cluster-marker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// A custom marker makes a flux-operator layout discoverable exactly like a
// bootstrapped one. It is added to flux-system/, not a replacement for it.
func TestResolveInputCustomMarkerDiscoversClusters(t *testing.T) {
	set := filepath.Join(t.TempDir(), "operator", "clusters")
	var want []string
	for _, name := range []string{"dev", "qa"} {
		dir := filepath.Join(set, name)
		mk(t, dir)
		if err := os.WriteFile(filepath.Join(dir, ".flux-cluster"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, dir)
	}

	mode, clusters, err := ResolveInput(set, DefaultMarkers().With(".flux-cluster"))
	if err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	if mode != ModeMulti {
		t.Errorf("mode = %v, want ModeMulti", mode)
	}
	sort.Strings(clusters)
	sort.Strings(want)
	if strings.Join(clusters, ",") != strings.Join(want, ",") {
		t.Errorf("clusters = %v, want %v", clusters, want)
	}
}

func writeYAML(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: ConfigMap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveInputClusterInfoIsNotAMarker(t *testing.T) {
	root := t.TempDir()
	prod := filepath.Join(root, "clusters", "prod")
	mk(t, prod)
	if err := os.WriteFile(filepath.Join(prod, "cluster-info.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if DefaultMarkers().IsCluster(prod) {
		t.Error("a file named cluster-info.yaml must not make a directory a cluster")
	}
}

func TestMarkersWith(t *testing.T) {
	base := DefaultMarkers()

	if got := base.With(""); len(got) != len(base) {
		t.Errorf("an empty name must add nothing, got %v", got)
	}

	extended := base.With(".flux-cluster")
	if len(extended) != len(base)+1 {
		t.Fatalf("markers = %v, want one more than %v", extended, base)
	}
	// The default set is not mutated: two commands may use different markers.
	if len(DefaultMarkers()) != len(base) {
		t.Error("With must not extend the default set in place")
	}
	if extended[0] != FluxSystemMarker {
		t.Errorf("markers = %v, want flux-system/ kept first", extended)
	}
}
