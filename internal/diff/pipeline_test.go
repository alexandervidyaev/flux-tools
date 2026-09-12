package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// pipeline_test.go reproduces the layout-migration scenario diff hits in CI:
// the cluster directory name on the current (target) side differs from the
// incoming (feature) side because the source repository renamed or
// restructured a cluster path. Without FindClustersUnion the diff command
// silently dropped clusters that only exist on the incoming side.

func TestDiffPipelineLayoutRename(t *testing.T) {
	tmp := t.TempDir()
	current := filepath.Join(tmp, "current")
	incoming := filepath.Join(tmp, "incoming")

	const sliceRel = "Namespace:kargo/Kind:namespace/Name:kargo.yaml"
	writeManifest(t, filepath.Join(current, "stable", sliceRel), `apiVersion: v1
kind: Namespace
metadata:
  name: kargo
  labels:
    product: platform
    version: legacy
`)
	writeManifest(t, filepath.Join(incoming, "ru-infra-yc-kube-common1", sliceRel), `apiVersion: v1
kind: Namespace
metadata:
  name: kargo
  labels:
    product: platform
`)

	clusters, err := FindClustersUnion(current, incoming)
	if err != nil {
		t.Fatalf("FindClustersUnion: %v", err)
	}
	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2: %v", len(clusters), clusters)
	}

	diffs := CompareClustersParallel(context.Background(), clusters, current, incoming, 3, 2)

	statusByCluster := make(map[string][]ChangeStatus)
	for _, d := range diffs {
		for _, ch := range d.Changes {
			statusByCluster[d.ClusterName] = append(statusByCluster[d.ClusterName], ch.Status)
		}
	}

	if got := statusByCluster["stable"]; len(got) != 1 || got[0] != StatusDeleted {
		t.Errorf("stable diff statuses = %v, want [Deleted]", got)
	}
	if got := statusByCluster["ru-infra-yc-kube-common1"]; len(got) != 1 || got[0] != StatusAdded {
		t.Errorf("incoming-only diff statuses = %v, want [Added]", got)
	}
}

func writeManifest(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
