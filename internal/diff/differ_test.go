package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCompareClusterSurfacesReadError verifies that a file which is listed by
// FindManifests but cannot be read no longer produces a silently empty diff:
// the read error is surfaced on ClusterDiff.Error (rendered by every formatter)
// instead of being swallowed by `content, _ := os.ReadFile(...)`.
func TestCompareClusterSurfacesReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses file permission bits")
	}

	tmp := t.TempDir()
	current := filepath.Join(tmp, "current") // empty side
	incoming := filepath.Join(tmp, "incoming")

	// An "added" resource: present only on the incoming side.
	unreadable := filepath.Join(incoming, "Namespace:kargo/Kind:namespace/Name:kargo.yaml")
	writeManifest(t, unreadable, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: kargo\n")

	// Make it unreadable but still discoverable (dir stays traversable, so
	// FindManifests lists it, but os.ReadFile fails).
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) }) // let TempDir clean up

	// Sanity: confirm the file is genuinely unreadable in this environment.
	if _, err := os.ReadFile(unreadable); err == nil {
		t.Skip("file remained readable; cannot exercise read-error path")
	}

	result := CompareCluster(context.Background(), "test-cluster", current, incoming, 3)

	if result.Error == nil {
		t.Fatalf("expected ClusterDiff.Error to be set for unreadable added file; got changes=%v", result.Changes)
	}
}
