package diff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnreadableManifestFailsDiff(t *testing.T) {
	current, incoming := t.TempDir(), t.TempDir()
	for _, dir := range []string{current, incoming} {
		if err := os.Mkdir(filepath.Join(dir, "cluster"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "cluster", "resource.yaml"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(incoming, "absent"), filepath.Join(incoming, "cluster", "resource.yaml")); err != nil {
		t.Fatal(err)
	}
	result := CompareCluster(context.Background(), "cluster", current, incoming, 3)
	if result.Error == nil {
		t.Fatal("read failure was reported as successful diff")
	}
}
