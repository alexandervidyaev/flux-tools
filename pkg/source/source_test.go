package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPickVersion(t *testing.T) {
	got, err := pickVersion([]string{"1.2.0", "v1.3.0", "1.9.0-rc.1", "2.0.0", "garbage"}, "^1.2.0", "")
	if err != nil || got != "v1.3.0" {
		t.Fatalf("got %q: %v", got, err)
	}
	if _, err := pickVersion([]string{"1.2.0"}, "~2.0.0", ""); err == nil {
		t.Fatal("expected missing version error")
	}
}
func TestExtractRejectsTraversalAndLinks(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "../escaped", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp/escaped"}} {
		var data bytes.Buffer
		gz := gzip.NewWriter(&data)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Close()
		gz.Close()
		if err := extract(&data, t.TempDir()); err == nil {
			t.Fatalf("accepted unsafe entry %+v", h)
		}
	}
}
func TestExternalArtifactMergesLocalSources(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"base": "image: nginx\nsettings:\n  replicas: 1\n  enabled: true\n", "override": "settings:\n  replicas: 3\n"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "values.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := New(context.Background(), root, t.TempDir(), map[string]string{"base": "base", "override": "override"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, name := range []string{"base", "override"} {
		r.Register(Object{"kind": "OCIRepository", "metadata": Object{"namespace": "flux-system", "name": name}, "spec": Object{"url": "oci://registry.invalid/" + name}})
	}
	r.Register(Object{"kind": "ArtifactGenerator", "metadata": Object{"namespace": "flux-system", "name": "combined"}, "spec": Object{
		"sources":   []any{Object{"alias": "base", "kind": "OCIRepository", "name": "base"}, Object{"alias": "override", "kind": "OCIRepository", "name": "override"}},
		"artifacts": []any{Object{"name": "combined", "copy": []any{Object{"from": "@base/*.yaml", "to": "@artifact/"}, Object{"from": "@override/*.yaml", "to": "@artifact/", "strategy": "Merge"}}}},
	}})
	dir, err := r.Resolve("ExternalArtifact", "flux-system", "combined")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	docs, err := documents(data)
	if err != nil {
		t.Fatal(err)
	}
	values := object(docs[0], "settings")
	if text(docs[0], "image") != "nginx" || values["replicas"] != 3 || values["enabled"] != true {
		t.Fatalf("incorrect merge: %s", data)
	}
}
