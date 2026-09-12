package kustomize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceIgnorePatterns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, SourceIgnoreFileName), `# comment
*.md
values/
/docs
charts/**/tests
!keep.md
clusters/*/notes.yaml
`)

	ig, err := LoadSourceIgnore(root)
	if err != nil {
		t.Fatalf("LoadSourceIgnore: %v", err)
	}

	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"README.md", false, true},
		{"deep/nested/README.md", false, true},
		{"keep.md", false, false},
		{"values", true, true},
		{"apps/values", true, true},
		{"values", false, false},
		{"docs", true, true},
		{"docs/index.yaml", false, true},
		{"apps/docs", true, false},
		{"charts/nginx/tests", true, true},
		{"charts/a/b/c/tests", true, true},
		{"charts/nginx/templates", true, false},
		{"clusters/prod/notes.yaml", false, true},
		{"clusters/prod/deep/notes.yaml", false, false},
		{"clusters/prod/apps.yaml", false, false},
	}
	for _, c := range cases {
		got := ig.Ignored(filepath.Join(root, filepath.FromSlash(c.rel)), c.isDir)
		if got != c.want {
			t.Errorf("Ignored(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}

	if ig.Ignored(filepath.Join(t.TempDir(), "README.md"), false) {
		t.Error("a path outside the root must never be ignored")
	}
}

func TestSourceIgnoreMissingFile(t *testing.T) {
	ig, err := LoadSourceIgnore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadSourceIgnore: %v", err)
	}
	if ig.Ignored(filepath.Join(ig.Root(), "x.yaml"), false) {
		t.Error("no file means nothing is ignored")
	}
	var nilIgnore *SourceIgnore
	if nilIgnore.Ignored("/anything", false) {
		t.Error("a nil matcher ignores nothing")
	}
}

func TestGenerateKustomizationHonoursSourceIgnore(t *testing.T) {
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	writeFile(t, filepath.Join(root, SourceIgnoreFileName), "values.yaml\nscratch/\n")
	writeFile(t, filepath.Join(cluster, "apps.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apps\n")
	writeFile(t, filepath.Join(cluster, "values.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: values\n")
	writeFile(t, filepath.Join(cluster, "scratch", "draft.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: draft\n")

	ig, err := LoadSourceIgnore(root)
	if err != nil {
		t.Fatalf("LoadSourceIgnore: %v", err)
	}
	genDir, cleanup, err := GenerateKustomization(cluster, ig)
	if err != nil {
		t.Fatalf("GenerateKustomization: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(genDir, "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); !strings.Contains(got, "apps.yaml") || strings.Contains(got, "values.yaml") || strings.Contains(got, "scratch") {
		t.Errorf("generated kustomization = %s", got)
	}
}
