package diff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClusterDiffJSONRoundTrip(t *testing.T) {
	original := &ClusterDiff{
		ClusterName: "kube-dev",
		Changes: []*FileChange{
			{
				Resource: ResourceInfo{Namespace: "monitoring", Kind: "daemonset", Name: "node-exporter"},
				Status:   StatusModified,
				Diff:     " context\n-old\n+new\n",
			},
			{
				Resource: ResourceInfo{Kind: "namespace", Name: "kargo"},
				Status:   StatusAdded,
				Diff:     "apiVersion: v1\nkind: Namespace\n",
			},
		},
	}

	data, err := MarshalClusterDiff(original)
	if err != nil {
		t.Fatalf("MarshalClusterDiff: %v", err)
	}

	path := filepath.Join(t.TempDir(), "kube-dev-diff.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadClusterDiffFile(path)
	if err != nil {
		t.Fatalf("LoadClusterDiffFile: %v", err)
	}

	if loaded.ClusterName != original.ClusterName {
		t.Errorf("ClusterName = %q, want %q", loaded.ClusterName, original.ClusterName)
	}
	if len(loaded.Changes) != len(original.Changes) {
		t.Fatalf("got %d changes, want %d", len(loaded.Changes), len(original.Changes))
	}
	for i, want := range original.Changes {
		got := loaded.Changes[i]
		if *got != *want {
			t.Errorf("change %d = %+v, want %+v", i, got, want)
		}
	}
	if loaded.Error != nil {
		t.Errorf("Error = %v, want nil", loaded.Error)
	}
}

func TestClusterDiffJSONError(t *testing.T) {
	original := &ClusterDiff{
		ClusterName: "broken",
		Changes:     []*FileChange{},
		Error:       errors.New("kustomize build failed"),
	}

	data, err := MarshalClusterDiff(original)
	if err != nil {
		t.Fatalf("MarshalClusterDiff: %v", err)
	}
	path := filepath.Join(t.TempDir(), "broken-diff.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadClusterDiffFile(path)
	if err != nil {
		t.Fatalf("LoadClusterDiffFile: %v", err)
	}
	if loaded.Error == nil || loaded.Error.Error() != "kustomize build failed" {
		t.Errorf("Error = %v, want 'kustomize build failed'", loaded.Error)
	}
}

func TestLoadClusterDiffFileMissing(t *testing.T) {
	if _, err := LoadClusterDiffFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("expected error for missing file")
	}
}

// TestSaveDiffsToFilesArtifacts checks that SaveDiffsToFiles writes the full
// artifact set: per-cluster markdown, per-cluster JSON and index.md.
func TestSaveDiffsToFilesArtifacts(t *testing.T) {
	dir := t.TempDir()
	diffs := []*ClusterDiff{
		{
			ClusterName: "cluster-b",
			Changes: []*FileChange{
				{
					Resource: ResourceInfo{Namespace: "ns", Kind: "deployment", Name: "app"},
					Status:   StatusModified,
					Diff:     "-a\n+b\n",
				},
			},
		},
		{ClusterName: "cluster-a", Changes: []*FileChange{}},
	}

	if err := SaveDiffsToFiles(diffs, dir); err != nil {
		t.Fatalf("SaveDiffsToFiles: %v", err)
	}

	for _, name := range []string{
		"cluster-a-diff.md", "cluster-a-diff.json",
		"cluster-b-diff.md", "cluster-b-diff.json",
		"index.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected artifact %s: %v", name, err)
		}
	}

	loaded, err := LoadClusterDiffFile(filepath.Join(dir, "cluster-b-diff.json"))
	if err != nil {
		t.Fatalf("LoadClusterDiffFile: %v", err)
	}
	if len(loaded.Changes) != 1 || loaded.Changes[0].Status != StatusModified {
		t.Errorf("unexpected changes in JSON artifact: %+v", loaded.Changes)
	}

	index, err := os.ReadFile(filepath.Join(dir, "index.md"))
	if err != nil {
		t.Fatalf("read index.md: %v", err)
	}
	for _, want := range []string{"cluster-a", "cluster-b", "Changes Summary"} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index.md does not mention %q", want)
		}
	}
}
