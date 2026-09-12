package build

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBuildAndParseCachedReusesRender verifies that a repeated render of the
// same path is served from the in-memory cache instead of invoking kustomize
// a second time: after two calls the cache holds exactly one entry and
// reports exactly one hit (I-1).
func TestBuildAndParseCachedReusesRender(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}

	fixturePath := filepath.Join(fixturesRel, "cluster-repo", "clusters", "dev", "cluster-a")

	builder, err := NewBuilder(context.Background(), BuildOptions{
		Path:     fixturePath,
		RootPath: filepath.Join(fixturesRel, "cluster-repo"),
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	first, err := builder.buildAndParseCached(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("first buildAndParseCached: %v", err)
	}
	second, err := builder.buildAndParseCached(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("second buildAndParseCached: %v", err)
	}

	entries, hits := builder.renderCacheStats()
	if entries != 1 {
		t.Errorf("cache entries = %d, want 1 (single kustomize exec expected)", entries)
	}
	if hits != 1 {
		t.Errorf("cache hits = %d, want 1 (second call must be a hit)", hits)
	}

	if len(first) == 0 || len(first) != len(second) {
		t.Errorf("object counts differ: first=%d, second=%d", len(first), len(second))
	}
	// Each call must re-parse the cached bytes: post-build substitutions
	// mutate objects in place, so cached calls must never alias objects.
	if len(first) > 0 && first[0] == second[0] {
		t.Error("cached calls returned aliased objects; expected a fresh parse per call")
	}
}
