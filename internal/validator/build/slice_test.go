package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// makeObj builds a minimal unstructured object for slicing tests.
// Empty namespace means the metadata.namespace field is absent, as for
// cluster-scoped resources.
func makeObj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	metadata := map[string]interface{}{"name": name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   metadata,
	}}
}

func TestSlicerFileName(t *testing.T) {
	tests := []struct {
		name     string
		template string
		obj      *unstructured.Unstructured
		want     string
	}{
		{
			name:     "default template",
			template: DefaultSliceTemplate,
			obj:      makeObj("v1", "ConfigMap", "my-ns", "my-config"),
			want:     "Namespace:my-ns/Kind:configmap/Name:my-config.yaml",
		},
		{
			name:     "lower on mixed case namespace and kind",
			template: DefaultSliceTemplate,
			obj:      makeObj("apps/v1", "Deployment", "MixedCase", "app"),
			want:     "Namespace:mixedcase/Kind:deployment/Name:app.yaml",
		},
		{
			name:     "dottodash replaces every dot in the name",
			template: DefaultSliceTemplate,
			obj:      makeObj("v1", "ConfigMap", "ns", "my.dotted.name"),
			want:     "Namespace:ns/Kind:configmap/Name:my-dotted-name.yaml",
		},
		{
			name:     "cluster-scoped object renders empty namespace",
			template: DefaultSliceTemplate,
			obj:      makeObj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "some-role"),
			want:     "Namespace:/Kind:clusterrole/Name:some-role.yaml",
		},
		{
			name:     "unicode is lowered like strings.ToLower",
			template: DefaultSliceTemplate,
			obj:      makeObj("v1", "ConfigMap", "Тест", "приложение.тест"),
			want:     "Namespace:тест/Kind:configmap/Name:приложение-тест.yaml",
		},
		{
			name:     "pipe with spaces parses the same as without",
			template: `{{.metadata.namespace | lower}}/{{.kind | lower}}.yaml`,
			obj:      makeObj("v1", "Namespace", "UpperNS", "n"),
			want:     "upperns/namespace.yaml",
		},
		{
			name:     "apiVersion field is available",
			template: `{{.apiVersion|dottodash}}/{{.metadata.name}}.yaml`,
			obj:      makeObj("helm.toolkit.fluxcd.io/v2", "HelmRelease", "ns", "app"),
			want:     "helm-toolkit-fluxcd-io/v2/app.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slicer, err := NewSlicer(tt.template)
			if err != nil {
				t.Fatalf("NewSlicer(%q): %v", tt.template, err)
			}
			got, err := slicer.FileName(tt.obj)
			if err != nil {
				t.Fatalf("FileName: %v", err)
			}
			if got != tt.want {
				t.Errorf("FileName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewSlicerInvalidTemplate(t *testing.T) {
	if _, err := NewSlicer(`{{.kind`); err == nil {
		t.Fatal("expected parse error for unterminated template")
	}
	// Unknown functions are a parse-time error in text/template.
	if _, err := NewSlicer(`{{.kind|nosuchfunc}}.yaml`); err == nil {
		t.Fatal("expected parse error for unknown template function")
	}
}

func TestWriteSlicedObjectsLayout(t *testing.T) {
	dir := t.TempDir()
	slicer, err := NewSlicer(DefaultSliceTemplate)
	if err != nil {
		t.Fatalf("NewSlicer: %v", err)
	}

	ns := makeObj("v1", "Namespace", "", "sample")
	ns.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"product": "platform"}
	objects := []*unstructured.Unstructured{
		makeObj("source.toolkit.fluxcd.io/v1", "HelmRepository", "sample", "sample"),
		ns,
	}

	if err := WriteSlicedObjects(dir, objects, slicer); err != nil {
		t.Fatalf("WriteSlicedObjects: %v", err)
	}

	wantFiles := map[string]string{
		"Namespace:/Kind:namespace/Name:sample.yaml": "apiVersion: v1\n" +
			"kind: Namespace\n" +
			"metadata:\n" +
			"  labels:\n" +
			"    product: platform\n" +
			"  name: sample\n",
		"Namespace:sample/Kind:helmrepository/Name:sample.yaml": "apiVersion: source.toolkit.fluxcd.io/v1\n" +
			"kind: HelmRepository\n" +
			"metadata:\n" +
			"  name: sample\n" +
			"  namespace: sample\n",
	}

	for rel, wantContent := range wantFiles {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("expected file %s: %v", rel, err)
			continue
		}
		if string(data) != wantContent {
			t.Errorf("content of %s = %q, want %q", rel, data, wantContent)
		}
	}

	// No extra files beyond the expected ones.
	var got []string
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(got) != len(wantFiles) {
		t.Errorf("got %d files (%v), want %d", len(got), got, len(wantFiles))
	}
}

func TestWriteSlicedObjectsCollision(t *testing.T) {
	dir := t.TempDir()
	slicer, err := NewSlicer(`Kind:{{.kind|lower}}.yaml`)
	if err != nil {
		t.Fatalf("NewSlicer: %v", err)
	}

	objects := []*unstructured.Unstructured{
		makeObj("v1", "ConfigMap", "ns1", "first"),
		makeObj("v1", "ConfigMap", "ns2", "second"),
	}

	err = WriteSlicedObjects(dir, objects, slicer)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "collision") || !strings.Contains(err.Error(), "Kind:configmap.yaml") {
		t.Errorf("collision error should name the conflicting path, got: %v", err)
	}
}
