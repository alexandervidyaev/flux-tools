package build

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func obj(apiVersion, kind, namespace, name string, extra map[string]any) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": namespace},
	}
	for k, v := range extra {
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

// testBuilder is the minimal Builder extractHelmResourcesFromManifests needs:
// it reads only the printer and the options.
func testBuilder(verbose bool, opts BuildOptions) (*Builder, *bytes.Buffer) {
	var buf bytes.Buffer
	return &Builder{options: opts, printer: output.NewWithWriter(&buf, verbose)}, &buf
}

// Rendered manifests can carry the very source resources the build still needs
// (a HelmRepository declared inside an app Kustomization, say). They are folded
// into the collection, but never over an entry that was already discovered.
func TestExtractHelmResourcesFromManifestsCollectsSources(t *testing.T) {
	b, buf := testBuilder(true, BuildOptions{})
	collection := manifest.NewManifestCollection()

	objects := []*unstructured.Unstructured{
		obj("source.toolkit.fluxcd.io/v1", "HelmRepository", "apps", "charts",
			map[string]any{"spec": map[string]any{"url": "https://charts.example.com"}}),
		obj("source.toolkit.fluxcd.io/v1", "GitRepository", "apps", "src",
			map[string]any{"spec": map[string]any{"url": "ssh://git@example.com/org/repo"}}),
		obj("source.extensions.fluxcd.io/v1alpha1", "ArtifactGenerator", "apps", "gen", nil),
		// Wrong API group: a look-alike from another project must be ignored.
		obj("example.com/v1", "HelmRepository", "apps", "impostor", nil),
		// Not a source at all.
		obj("apps/v1", "Deployment", "apps", "web", nil),
	}

	secrets := b.extractHelmResourcesFromManifests(collection, objects)
	if len(secrets) != 0 {
		t.Errorf("secrets = %v, want none", secrets)
	}

	if _, ok := collection.HelmRepositories["apps/charts"]; !ok {
		t.Error("HelmRepository was not collected")
	}
	if _, ok := collection.HelmRepositories["apps/impostor"]; ok {
		t.Error("a HelmRepository from a foreign API group must be ignored")
	}
	if _, ok := collection.GitRepositories["apps/src"]; !ok {
		t.Error("GitRepository was not collected")
	}
	if _, ok := collection.ArtifactGenerators["apps/gen"]; !ok {
		t.Error("ArtifactGenerator was not collected")
	}

	got := buf.String()
	for _, want := range []string{
		"Found HelmRepository in manifests: apps/charts\n",
		"Found GitRepository in manifests: apps/src\n",
		"Found ArtifactGenerator in manifests: apps/gen\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in verbose output:\n%s", want, got)
		}
	}
}

// Discovery already ran when this is called, so an entry it found wins: the
// manifests only fill gaps.
func TestExtractHelmResourcesFromManifestsKeepsTheFirstEntry(t *testing.T) {
	b, _ := testBuilder(false, BuildOptions{})
	collection := manifest.NewManifestCollection()

	first := obj("source.toolkit.fluxcd.io/v1", "HelmRepository", "apps", "charts",
		map[string]any{"spec": map[string]any{"url": "https://first.example.com"}})
	second := obj("source.toolkit.fluxcd.io/v1", "HelmRepository", "apps", "charts",
		map[string]any{"spec": map[string]any{"url": "https://second.example.com"}})

	b.extractHelmResourcesFromManifests(collection, []*unstructured.Unstructured{first, second})

	repo, ok := collection.HelmRepositories["apps/charts"]
	if !ok {
		t.Fatal("HelmRepository was not collected")
	}
	if repo.Spec.URL != "https://first.example.com" {
		t.Errorf("URL = %q, want the first occurrence to win", repo.Spec.URL)
	}
}

func TestExtractHelmResourcesFromManifestsSecrets(t *testing.T) {
	secretObj := obj("v1", "Secret", "apps", "creds", map[string]any{
		"data": map[string]any{
			"username": "dXNlcg==",
			"password": "cGFzcw==",
			// A non-string value cannot be a Secret entry and is dropped.
			"bogus": 42,
		},
	})

	t.Run("collected by namespace/name", func(t *testing.T) {
		b, buf := testBuilder(true, BuildOptions{})
		secrets := b.extractHelmResourcesFromManifests(manifest.NewManifestCollection(),
			[]*unstructured.Unstructured{secretObj})

		got, ok := secrets["apps/creds"]
		if !ok {
			t.Fatalf("secrets = %v, want a apps/creds entry", secrets)
		}
		if got["username"] != "dXNlcg==" || got["password"] != "cGFzcw==" {
			t.Errorf("secret data = %v", got)
		}
		if _, ok := got["bogus"]; ok {
			t.Errorf("a non-string value must not reach the secret data: %v", got)
		}
		if !strings.Contains(buf.String(), "Found Secret in manifests: apps/creds\n") {
			t.Errorf("verbose output = %q", buf.String())
		}
	})

	t.Run("skipped when SkipSecrets is set", func(t *testing.T) {
		b, _ := testBuilder(false, BuildOptions{SkipSecrets: true})
		secrets := b.extractHelmResourcesFromManifests(manifest.NewManifestCollection(),
			[]*unstructured.Unstructured{secretObj})

		if len(secrets) != 0 {
			t.Errorf("secrets = %v, want none when SkipSecrets is set", secrets)
		}
	})

	t.Run("a Secret without a data map yields nothing", func(t *testing.T) {
		b, _ := testBuilder(false, BuildOptions{})
		secrets := b.extractHelmResourcesFromManifests(manifest.NewManifestCollection(),
			[]*unstructured.Unstructured{obj("v1", "Secret", "apps", "empty", nil)})

		if len(secrets) != 0 {
			t.Errorf("secrets = %v, want none", secrets)
		}
	})
}
