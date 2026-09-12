package diff

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindClustersUnion(t *testing.T) {
	tmp := t.TempDir()

	current := filepath.Join(tmp, "current")
	incoming := filepath.Join(tmp, "incoming")
	for _, dir := range []string{
		filepath.Join(current, "stable"),
		filepath.Join(current, "shared"),
		filepath.Join(incoming, "shared"),
		filepath.Join(incoming, "ru-infra-yc-kube-common1"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	t.Run("union of clusters from both dirs", func(t *testing.T) {
		got, err := FindClustersUnion(current, incoming)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"ru-infra-yc-kube-common1", "shared", "stable"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("missing directory contributes nothing", func(t *testing.T) {
		got, err := FindClustersUnion(current, filepath.Join(tmp, "does-not-exist"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"shared", "stable"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("all missing dirs returns empty", func(t *testing.T) {
		got, err := FindClustersUnion(filepath.Join(tmp, "a"), filepath.Join(tmp, "b"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}
