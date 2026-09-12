package kustomize

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"sigs.k8s.io/yaml"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// bootstrapLayout mirrors `flux bootstrap`: a cluster directory without a
// kustomization.yaml, holding flux-system/ (with its own kustomization) and
// loose Flux Kustomization manifests.
func bootstrapLayout(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flux-system", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - gotk-sync.yaml\n")
	writeFile(t, filepath.Join(dir, "flux-system", "gotk-sync.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sync\n")
	writeFile(t, filepath.Join(dir, "flux-system", "ignored-inside.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: not-listed\n")
	writeFile(t, filepath.Join(dir, "apps.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apps\n")
	writeFile(t, filepath.Join(dir, "nested", "deep", "infra.yml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: infra\n")
	writeFile(t, filepath.Join(dir, "values.yaml"), "replicas: 3\nimage:\n  tag: v1\n")
	writeFile(t, filepath.Join(dir, "README.md"), "apiVersion: v1\nkind: ConfigMap\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "kind: ConfigMap\n")
	return dir
}

func TestGenerateKustomizationListsResourcesLikeTheController(t *testing.T) {
	dir := bootstrapLayout(t)

	genDir, cleanup, err := GenerateKustomization(dir, nil)
	if err != nil {
		t.Fatalf("GenerateKustomization: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(genDir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	var doc struct {
		Resources []string `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Resources are relative to the real path of genDir (see realPath), so
	// compare against the real path of the source directory as well.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(doc.Resources))
	for _, r := range doc.Resources {
		abs, err := filepath.Abs(filepath.Join(genDir, r))
		if err != nil {
			t.Fatalf("abs %s: %v", r, err)
		}
		rel, err := filepath.Rel(realDir, abs)
		if err != nil {
			t.Fatalf("rel %s: %v", abs, err)
		}
		got = append(got, rel)
	}
	sort.Strings(got)

	want := []string{"apps.yaml", "flux-system", filepath.Join("nested", "deep", "infra.yml")}
	if len(got) != len(want) {
		t.Fatalf("resources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resources[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "kustomization.yaml")); !os.IsNotExist(err) {
		t.Error("the source directory must not receive a kustomization.yaml")
	}

	cleanup()
	if _, err := os.Stat(genDir); !os.IsNotExist(err) {
		t.Error("cleanup must remove the generated directory")
	}
}

func TestGenerateKustomizationEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "values.yaml"), "a: 1\n")

	genDir, cleanup, err := GenerateKustomization(dir, nil)
	if err != nil {
		t.Fatalf("GenerateKustomization: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(genDir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Resources []string `json:"resources"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Resources) != 0 {
		t.Errorf("resources = %v, want none", doc.Resources)
	}
}

func TestBuildDirGeneratesWhenKustomizationIsMissing(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
	dir := bootstrapLayout(t)

	objects, err := NewBuilder().BuildDirAndParse(context.Background(), dir)
	if err != nil {
		t.Fatalf("BuildDirAndParse: %v", err)
	}

	names := make([]string, 0, len(objects))
	for _, o := range objects {
		names = append(names, o.GetName())
	}
	sort.Strings(names)
	want := []string{"apps", "infra", "sync"}
	if len(names) != len(want) {
		t.Fatalf("objects = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("objects[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestBuildDirUsesExistingKustomization(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - a.yaml\n")
	writeFile(t, filepath.Join(dir, "a.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: b\n")

	objects, err := NewBuilder().BuildDirAndParse(context.Background(), dir)
	if err != nil {
		t.Fatalf("BuildDirAndParse: %v", err)
	}
	if len(objects) != 1 || objects[0].GetName() != "a" {
		t.Errorf("objects = %d, want only the one listed in kustomization.yaml", len(objects))
	}
}

// The temporary directory can be reached through a symlink (macOS keeps
// TMPDIR under /var, a link to /private/var). kustomize resolves that link
// before following relative resource paths, so a relative path computed
// from the unresolved location points at a wrong ancestor. The link here
// sits at a different depth than its target to make the mismatch visible.
func TestBuildDirGeneratedPathsSurviveSymlinkedTempDir(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realTmp := filepath.Join(base, "tmpreal")
	link := filepath.Join(base, "a", "b", "tmplink")
	if err := os.MkdirAll(realTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTmp, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
	t.Setenv("TMPDIR", link)

	src := filepath.Join(base, "src")
	writeFile(t, filepath.Join(src, "cm.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")

	objects, err := NewBuilder().BuildDirAndParse(context.Background(), src)
	if err != nil {
		t.Fatalf("BuildDirAndParse through a symlinked TMPDIR: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("objects = %d, want 1", len(objects))
	}
}
