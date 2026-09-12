package helm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// countingRunner is a CommandRunner that serves canned helm/kustomize output
// and counts `helm template` invocations, so tests can assert that a cache
// hit never shells out.
type countingRunner struct {
	mu               sync.Mutex
	templateCalls    int
	templateOutput   []byte
	helmVersion      string
	kustomizeVersion string
}

func (r *countingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case name == "helm" && len(args) > 0 && args[0] == "version":
		return []byte(r.helmVersion + "\n"), nil
	case name == "kustomize" && len(args) > 0 && args[0] == "version":
		return []byte(r.kustomizeVersion + "\n"), nil
	case name == "helm" && len(args) > 0 && args[0] == "template":
		r.templateCalls++
		return r.templateOutput, nil
	}
	return nil, nil
}

func (r *countingRunner) RunWithStdin(ctx context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	return r.Run(ctx, name, args...)
}

func (r *countingRunner) LookPath(string) error { return nil }

func (r *countingRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.templateCalls
}

const testChartYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: rendered
  namespace: demo
data:
  key: value
`

// newCacheTestClient builds a client whose chart cache already contains the
// .tgz for mychart-1.0.0, so templateChart takes the cacheable path.
func newCacheTestClient(t *testing.T, runner *countingRunner) *Client {
	t.Helper()
	dir := t.TempDir()
	chartsDir := filepath.Join(dir, "charts")
	if err := os.MkdirAll(chartsDir, 0755); err != nil {
		t.Fatalf("mkdir charts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartsDir, "mychart-1.0.0.tgz"), []byte("fake-tgz"), 0644); err != nil {
		t.Fatalf("write chart: %v", err)
	}

	c, err := NewClient(dir, 60)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.runner = runner
	c.SetTemplateCacheEnabled(true)
	return c
}

func baseTemplateOptions() TemplateOptions {
	return TemplateOptions{
		ReleaseName: "demo",
		Namespace:   "demo",
		SkipTests:   true,
		Values:      map[string]interface{}{"replicas": float64(1)},
	}
}

func TestTemplateCacheHitSkipsRunner(t *testing.T) {
	runner := &countingRunner{templateOutput: []byte(testChartYAML), helmVersion: "v3.14.0"}
	c := newCacheTestClient(t, runner)
	repo := newRepo("myrepo")

	ctx := context.Background()
	opts := baseTemplateOptions()

	first, err := c.templateChart(ctx, repo, "mychart", "1.0.0", opts)
	if err != nil {
		t.Fatalf("first templateChart: %v", err)
	}
	if runner.calls() != 1 {
		t.Fatalf("expected 1 helm template call after first render, got %d", runner.calls())
	}

	second, err := c.templateChart(ctx, repo, "mychart", "1.0.0", opts)
	if err != nil {
		t.Fatalf("second templateChart: %v", err)
	}
	if runner.calls() != 1 {
		t.Errorf("cache hit must not invoke helm template: got %d calls", runner.calls())
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 object from each render, got %d and %d", len(first), len(second))
	}
	if first[0].GetName() != second[0].GetName() {
		t.Errorf("cached render differs: %q vs %q", first[0].GetName(), second[0].GetName())
	}

	hits, misses := c.TemplateCacheStats()
	if hits != 1 || misses != 1 {
		t.Errorf("expected 1 hit / 1 miss, got %d / %d", hits, misses)
	}
}

func TestTemplateCacheDisabledAlwaysRuns(t *testing.T) {
	runner := &countingRunner{templateOutput: []byte(testChartYAML), helmVersion: "v3.14.0"}
	c := newCacheTestClient(t, runner)
	c.SetTemplateCacheEnabled(false)
	repo := newRepo("myrepo")

	ctx := context.Background()
	opts := baseTemplateOptions()
	for range 2 {
		if _, err := c.templateChart(ctx, repo, "mychart", "1.0.0", opts); err != nil {
			t.Fatalf("templateChart: %v", err)
		}
	}
	if runner.calls() != 2 {
		t.Errorf("disabled cache must render every time: got %d calls", runner.calls())
	}
	if entries, _ := os.ReadDir(filepath.Join(c.cacheDir, templateCacheDirName)); len(entries) > 0 {
		t.Errorf("disabled cache must not write entries, found %d", len(entries))
	}
}

func TestTemplateCacheCorruptEntryIsMiss(t *testing.T) {
	runner := &countingRunner{templateOutput: []byte(testChartYAML), helmVersion: "v3.14.0"}
	c := newCacheTestClient(t, runner)
	repo := newRepo("myrepo")

	ctx := context.Background()
	opts := baseTemplateOptions()

	if _, err := c.templateChart(ctx, repo, "mychart", "1.0.0", opts); err != nil {
		t.Fatalf("first templateChart: %v", err)
	}

	// Overwrite the single cache entry with garbage that parses to no objects.
	key, err := c.templateCacheKey(ctx, "mychart", "1.0.0", opts)
	if err != nil {
		t.Fatalf("templateCacheKey: %v", err)
	}
	if err := os.WriteFile(c.templateCachePath(key), []byte("!!! not yaml {{{"), 0644); err != nil {
		t.Fatalf("corrupt cache entry: %v", err)
	}

	objects, err := c.templateChart(ctx, repo, "mychart", "1.0.0", opts)
	if err != nil {
		t.Fatalf("templateChart with corrupt cache: %v", err)
	}
	if runner.calls() != 2 {
		t.Errorf("corrupt entry must fall back to a real render: got %d calls", runner.calls())
	}
	if len(objects) != 1 {
		t.Errorf("expected 1 object after re-render, got %d", len(objects))
	}
}

func TestTemplateCacheKeySensitivity(t *testing.T) {
	runner := &countingRunner{helmVersion: "v3.14.0", kustomizeVersion: "v5.4.0"}
	c := newCacheTestClient(t, runner)
	ctx := context.Background()

	valuesFile := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesFile, []byte("a: 1\n"), 0644); err != nil {
		t.Fatalf("write values file: %v", err)
	}

	base := baseTemplateOptions()
	base.ValuesFiles = []string{valuesFile}

	baseKey, err := c.templateCacheKey(ctx, "mychart", "1.0.0", base)
	if err != nil {
		t.Fatalf("base key: %v", err)
	}

	mutations := map[string]func() (string, error){
		"chart name": func() (string, error) {
			return c.templateCacheKey(ctx, "otherchart", "1.0.0", base)
		},
		"chart version": func() (string, error) {
			return c.templateCacheKey(ctx, "mychart", "1.0.1", base)
		},
		"release name": func() (string, error) {
			o := base
			o.ReleaseName = "other"
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"namespace": func() (string, error) {
			o := base
			o.Namespace = "other"
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"skipTests flag": func() (string, error) {
			o := base
			o.SkipTests = !o.SkipTests
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"includeCRDs flag": func() (string, error) {
			o := base
			o.IncludeCRDs = !o.IncludeCRDs
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"disableSchemaValidation flag": func() (string, error) {
			o := base
			o.DisableSchemaValidation = !o.DisableSchemaValidation
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"inline values": func() (string, error) {
			o := base
			o.Values = map[string]interface{}{"replicas": float64(2)}
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
		"values file content": func() (string, error) {
			if err := os.WriteFile(valuesFile, []byte("a: 2\n"), 0644); err != nil {
				return "", err
			}
			defer os.WriteFile(valuesFile, []byte("a: 1\n"), 0644)
			return c.templateCacheKey(ctx, "mychart", "1.0.0", base)
		},
		"postRenderers": func() (string, error) {
			o := base
			o.PostRenderers = []types.PostRenderer{{
				Kustomize: &types.Kustomize{
					Images: []types.Image{{Name: "nginx", NewTag: "1.25"}},
				},
			}}
			return c.templateCacheKey(ctx, "mychart", "1.0.0", o)
		},
	}

	for name, mutate := range mutations {
		key, err := mutate()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if key == baseKey {
			t.Errorf("changing %s must change the cache key", name)
		}
	}

	// helm version sensitivity: a client whose helm reports another version
	// must produce a different key for identical options.
	otherRunner := &countingRunner{helmVersion: "v3.15.0"}
	otherClient := newCacheTestClient(t, otherRunner)
	otherKey, err := otherClient.templateCacheKey(ctx, "mychart", "1.0.0", base)
	if err != nil {
		t.Fatalf("other helm version key: %v", err)
	}
	if otherKey == baseKey {
		t.Errorf("changing helm version must change the cache key")
	}

	// Same content at a different path must produce the same key.
	sameContentFile := filepath.Join(t.TempDir(), "renamed.yaml")
	if err := os.WriteFile(sameContentFile, []byte("a: 1\n"), 0644); err != nil {
		t.Fatalf("write renamed values file: %v", err)
	}
	moved := base
	moved.ValuesFiles = []string{sameContentFile}
	movedKey, err := c.templateCacheKey(ctx, "mychart", "1.0.0", moved)
	if err != nil {
		t.Fatalf("moved values file key: %v", err)
	}
	if movedKey != baseKey {
		t.Errorf("values file path must not influence the key (only its content)")
	}
}

func TestTemplateCacheKeyNestedValuesDeterministic(t *testing.T) {
	runner := &countingRunner{helmVersion: "v3.14.0"}
	c := newCacheTestClient(t, runner)
	ctx := context.Background()

	// Semantically equal nested maps built in different insertion order:
	// encoding/json sorts map keys at every nesting level, so the canonical
	// encoding — and therefore the key — must match.
	a := baseTemplateOptions()
	a.Values = map[string]interface{}{
		"z": map[string]interface{}{"b": float64(2), "a": float64(1)},
		"a": "x",
	}
	b := baseTemplateOptions()
	b.Values = map[string]interface{}{}
	b.Values["a"] = "x"
	inner := map[string]interface{}{}
	inner["a"] = float64(1)
	inner["b"] = float64(2)
	b.Values["z"] = inner

	keyA, err := c.templateCacheKey(ctx, "mychart", "1.0.0", a)
	if err != nil {
		t.Fatalf("key A: %v", err)
	}
	keyB, err := c.templateCacheKey(ctx, "mychart", "1.0.0", b)
	if err != nil {
		t.Fatalf("key B: %v", err)
	}
	if keyA != keyB {
		t.Errorf("semantically equal nested values must produce identical keys: %s vs %s", keyA, keyB)
	}
}
