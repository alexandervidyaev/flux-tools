package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/output"
)

func quietPrinter() *output.Printer {
	return output.NewWithWriter(&bytes.Buffer{}, false)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const twoObjects = `apiVersion: v1
kind: ConfigMap
metadata:
  name: first
  namespace: apps
---
apiVersion: v1
kind: Secret
metadata:
  name: second
  namespace: apps
`

// A directory that already holds one subdirectory per cluster is what the
// comparison wants, so it is used untouched and nothing is created.
func TestPrepareDiffInputLeavesASlicedTreeAlone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod", "Namespace:apps", "Kind:configmap", "Name:first.yaml"), twoObjects)

	got, cleanup, err := prepareDiffInput(dir, quietPrinter())
	if err != nil {
		t.Fatalf("prepareDiffInput: %v", err)
	}
	defer cleanup()

	if got != dir {
		t.Errorf("got %q, want the directory unchanged (%q)", got, dir)
	}
}

// build's default output is one <cluster>.yaml per cluster. It is sliced into
// one directory per cluster so a plain build result can be diffed directly.
func TestPrepareDiffInputSlicesBuildOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod.yaml"), twoObjects)
	writeFile(t, filepath.Join(dir, "staging.yaml"), twoObjects)

	tree, cleanup, err := prepareDiffInput(dir, quietPrinter())
	if err != nil {
		t.Fatalf("prepareDiffInput: %v", err)
	}

	if tree == dir {
		t.Fatal("flat output must be sliced into a separate directory")
	}

	// The cluster name comes from the file name, and every object gets its own
	// file under the default template.
	var clusters []string
	entries, err := os.ReadDir(tree)
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			clusters = append(clusters, e.Name())
		}
	}
	sort.Strings(clusters)
	if want := []string{"prod", "staging"}; strings.Join(clusters, ",") != strings.Join(want, ",") {
		t.Errorf("clusters = %v, want %v", clusters, want)
	}

	var files int
	err = filepath.Walk(filepath.Join(tree, "prod"), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			files++
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if files != 2 {
		t.Errorf("prod holds %d files, want one per object (2)", files)
	}

	cleanup()
	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Error("cleanup must remove what it created")
	}
}

// An empty or unrecognised directory is handed back as it is, so the caller
// reports it against the path the user actually typed.
func TestPrepareDiffInputPassesAnEmptyDirectoryThrough(t *testing.T) {
	dir := t.TempDir()

	got, cleanup, err := prepareDiffInput(dir, quietPrinter())
	if err != nil {
		t.Fatalf("prepareDiffInput: %v", err)
	}
	defer cleanup()

	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

// A file that does not parse fails the command rather than producing a diff
// against a partially sliced tree, and leaves nothing behind.
func TestPrepareDiffInputRejectsUnparseableYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "prod.yaml"), "this: is: not: valid: yaml:\n\t- broken\n")

	_, cleanup, err := prepareDiffInput(dir, quietPrinter())
	defer cleanup()

	if err == nil {
		t.Fatal("want an error for a file that cannot be parsed")
	}
	if !strings.Contains(err.Error(), "prod.yaml") {
		t.Errorf("err = %q, want it to name the file", err)
	}
}
