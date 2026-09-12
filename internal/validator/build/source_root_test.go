package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

func kustomizationFrom(source, path string) *types.Kustomization {
	ks := &types.Kustomization{}
	ks.Namespace = "flux-system"
	ks.Name = "apps"
	ks.Spec.Path = path
	ks.Spec.SourceRef.Kind = "GitRepository"
	ks.Spec.SourceRef.Name = source
	return ks
}

// Without flux-system/ (flux-operator) the sync GitRepository lives only in
// the cluster, so a GitRepository no manifest declares is this checkout.
func TestResolveSourceRootOperatorUndeclaredSourceIsLocal(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "apps", "prod")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	builder, err := NewBuilder(context.Background(), BuildOptions{
		Path:     filepath.Join(root, "clusters", "prod"),
		RootPath: root,
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	got, err := builder.ResolveKustomizationBuildPath(context.Background(), kustomizationFrom("flux-system", "./apps/prod"))
	if err != nil {
		t.Fatalf("ResolveKustomizationBuildPath: %v", err)
	}
	if got != apps {
		t.Errorf("build path = %q, want %q", got, apps)
	}
}

// With flux-system/ the sync source is known. A Kustomization from a different
// GitRepository is never rendered from this checkout, even when the same
// relative path exists here: undeclared is an error, declared means a clone.
func TestResolveSourceRootBootstrapOtherSourceNeverLocal(t *testing.T) {
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	writeFixture(t, filepath.Join(cluster, "flux-system", "gotk-sync.yaml"), gotkSync("./clusters/prod"))
	if err := os.MkdirAll(filepath.Join(root, "apps", "prod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	builder, err := NewBuilder(context.Background(), BuildOptions{
		Path:     cluster,
		RootPath: root,
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	got, err := builder.ResolveKustomizationBuildPath(context.Background(), kustomizationFrom("flux-system", "./apps/prod"))
	if err != nil {
		t.Fatalf("sync source: %v", err)
	}
	if want := filepath.Join(root, "apps", "prod"); got != want {
		t.Errorf("build path = %q, want %q", got, want)
	}

	_, err = builder.ResolveKustomizationBuildPath(context.Background(), kustomizationFrom("shared", "./apps/prod"))
	if err == nil {
		t.Fatal("expected an error for an undeclared GitRepository")
	}
	if !strings.Contains(err.Error(), "neither the flux-system sync source") {
		t.Errorf("error = %q", err.Error())
	}

	// Declared with a URL: must be cloned. The runner is real here, so the
	// clone of a bogus URL fails, and that failure must surface instead of a
	// fallback to the local directory of the same name.
	builder.helmClient.RegisterGitRepository(&types.GitRepository{
		ObjectMeta: types.ObjectMeta{Name: "shared", Namespace: "flux-system"},
		Spec:       types.GitRepositorySpec{URL: "file:///nonexistent/shared.git", Reference: &types.GitRepositoryRef{Branch: "main"}},
	})
	_, err = builder.ResolveKustomizationBuildPath(context.Background(), kustomizationFrom("shared", "./apps/prod"))
	if err == nil {
		t.Fatal("expected the clone failure to surface")
	}
	if !strings.Contains(err.Error(), "clone") {
		t.Errorf("error = %q, want a clone failure", err.Error())
	}
}

func TestDetectSelfSource(t *testing.T) {
	root := t.TempDir()
	cluster := filepath.Join(root, "clusters", "prod")
	writeFixture(t, filepath.Join(cluster, "flux-system", "gotk-sync.yaml"), gotkSync("./clusters/prod"))

	got, err := DetectSelfSource(cluster)
	if err != nil {
		t.Fatalf("DetectSelfSource: %v", err)
	}
	if got != "flux-system/flux-system" {
		t.Errorf("self source = %q, want flux-system/flux-system", got)
	}

	got, err = DetectSelfSource(t.TempDir())
	if err != nil || got != "" {
		t.Errorf("without flux-system: %q, %v; want empty, nil", got, err)
	}
}
