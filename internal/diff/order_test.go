package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCompareClusterDeterministicOrder: changes come from map iteration, so
// without sorting the report order reshuffled between runs on the same input.
func TestCompareClusterDeterministicOrder(t *testing.T) {
	tmp := t.TempDir()
	current := filepath.Join(tmp, "current", "c1")
	incoming := filepath.Join(tmp, "incoming", "c1")

	// Several modified resources across namespaces/kinds to make random map
	// order visible.
	files := []string{
		"Namespace:zeta/Kind:deployment/Name:a.yaml",
		"Namespace:alpha/Kind:service/Name:b.yaml",
		"Namespace:alpha/Kind:configmap/Name:c.yaml",
		"Namespace:mid/Kind:daemonset/Name:d.yaml",
		"Namespace:mid/Kind:daemonset/Name:a.yaml",
	}
	for _, f := range files {
		writeOrderTestManifest(t, filepath.Join(current, f), "value: old\n")
		writeOrderTestManifest(t, filepath.Join(incoming, f), "value: new\n")
	}

	var prev []string
	for run := range 5 {
		d := CompareCluster(context.Background(), "c1", current, incoming, 3)
		if d.Error != nil {
			t.Fatalf("CompareCluster: %v", d.Error)
		}
		var order []string
		for _, ch := range d.Changes {
			order = append(order, ch.Resource.Namespace+"/"+ch.Resource.Kind+"/"+ch.Resource.Name)
		}
		if run > 0 {
			for i := range order {
				if order[i] != prev[i] {
					t.Fatalf("run %d order differs: %v vs %v", run, order, prev)
				}
			}
		}
		prev = order
	}

	// And the order is the documented one: namespace, kind, name.
	want := []string{
		"alpha/configmap/c", "alpha/service/b",
		"mid/daemonset/a", "mid/daemonset/d",
		"zeta/deployment/a",
	}
	for i, w := range want {
		if prev[i] != w {
			t.Fatalf("sorted order = %v, want %v", prev, want)
		}
	}
}

func writeOrderTestManifest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
