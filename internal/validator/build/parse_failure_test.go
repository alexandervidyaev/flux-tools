package build

import (
	"context"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNativeMalformedFluxResourceFailsCollection(t *testing.T) {
	root := t.TempDir()
	b, err := NewBuilder(context.Background(), BuildOptions{Path: root, RootPath: root, CacheDir: t.TempDir(), NativeFlux: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "metadata": map[string]interface{}{"name": "invalid"}, "spec": map[string]interface{}{"path": true}}}
	if err := b.parseFluxObjects([]*unstructured.Unstructured{obj}, manifest.NewManifestCollection()); err == nil {
		t.Fatal("invalid Flux resource was silently skipped")
	}
}
