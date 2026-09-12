package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeLayoutFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const layoutGotkSync = `apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: flux-system
  namespace: flux-system
spec:
  interval: 1m
  url: ssh://git@example.com/org/fleet
  ref:
    branch: main
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: flux-system
  namespace: flux-system
spec:
  interval: 10m
  path: ./clusters/prod
  prune: true
  sourceRef:
    kind: GitRepository
    name: flux-system
`

func layoutKustomization(name, path, source string) string {
	return `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: ` + name + `
  namespace: flux-system
spec:
  interval: 10m
  path: ` + path + `
  prune: true
  sourceRef:
    kind: GitRepository
    name: ` + source + `
`
}

// The test runner walks the same roots as the builder: a bootstrap layout
// without kustomization.yaml in the cluster directory yields one passing test
// per Kustomization, and a Kustomization from an undeclared GitRepository
// fails its test rather than being read from a local directory.
func TestRunnerOnBootstrapLayout(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	writeLayoutFile(t, filepath.Join(cluster, "flux-system", "gotk-sync.yaml"), layoutGotkSync)
	writeLayoutFile(t, filepath.Join(cluster, "flux-system", "kustomization.yaml"),
		"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - gotk-sync.yaml\n")
	writeLayoutFile(t, filepath.Join(cluster, "apps.yaml"), layoutKustomization("apps", "./apps", "flux-system"))
	writeLayoutFile(t, filepath.Join(cluster, "foreign.yaml"), layoutKustomization("foreign", "./apps", "someone-else"))
	writeLayoutFile(t, filepath.Join(root, "apps", "cm.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")

	runner, err := NewTestRunner(context.Background(), TestOptions{Path: cluster, RootPath: root, CacheDir: t.TempDir(), Output: os.Stderr})
	if err != nil {
		t.Fatalf("NewTestRunner: %v", err)
	}
	results, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	byName := map[string]TestResult{}
	for _, r := range results.Tests {
		byName[r.Name] = r
	}
	if got := byName["flux-system/apps"]; got.Status != TestPassed {
		t.Errorf("apps: status %s, err %v", got.Status, got.Error)
	}
	if got := byName["flux-system/flux-system"]; got.Status != TestPassed {
		t.Errorf("flux-system: status %s, err %v", got.Status, got.Error)
	}
	if got := byName["flux-system/foreign"]; got.Status != TestFailed {
		t.Errorf("foreign: status %s, want FAILED (undeclared GitRepository)", got.Status)
	}
}

func TestRunnerOnOperatorLayoutWithRoot(t *testing.T) {
	if _, err := exec.LookPath("kustomize"); err != nil {
		t.Skip("kustomize not in PATH")
	}
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	writeLayoutFile(t, filepath.Join(cluster, "apps.yaml"), layoutKustomization("apps", "./apps", "flux-system"))
	writeLayoutFile(t, filepath.Join(root, "apps", "cm.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n")

	// The repository root is never guessed: without one the runner refuses
	// rather than resolve spec.path against something arbitrary.
	if _, err := NewTestRunner(context.Background(), TestOptions{Path: cluster, CacheDir: t.TempDir()}); err == nil {
		t.Error("without RootPath the runner must refuse")
	}

	runner, err := NewTestRunner(context.Background(), TestOptions{Path: cluster, RootPath: root, CacheDir: t.TempDir(), Output: os.Stderr})
	if err != nil {
		t.Fatalf("NewTestRunner: %v", err)
	}
	results, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if results.Failed != 0 || results.Passed != 1 {
		t.Errorf("passed %d failed %d, want 1/0: %+v", results.Passed, results.Failed, results.Tests)
	}
}
