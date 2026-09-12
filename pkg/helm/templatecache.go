package helm

// Persistent helm template cache (I-3).
//
// `helm template` is deterministic for a fixed set of inputs, so rendered YAML
// can be reused across runs (the cache directory is preserved between CI
// pipelines via FLUX_TOOLS_CACHE_DIR). Cached entries live in
// {cacheDir}/template-cache/{sha256}.yaml, where the key is a sha256 over a
// canonical JSON encoding of every input that influences the output:
//
//   - chart name and chart version;
//   - canonicalized inline values (encoding/json sorts map keys at every
//     nesting level, so semantically equal maps produce identical JSON);
//   - sha256 of the CONTENT of each values file (paths are irrelevant);
//   - release name, namespace;
//   - flags: SkipTests, IncludeCRDs, DisableSchemaValidation;
//   - postRenderers spec (canonical JSON);
//   - helm version (fetched once per client via `helm version --short`);
//   - kustomize version, but only when postRenderers are present (they are
//     executed via `kustomize build`).
//
// What is cached: only templateChart renders that resolve the chart from the
// local archive {cacheDir}/charts/<name>-<version>.tgz — the archive content
// is uniquely identified by chart name + version, so the key is sound.
//
// What is deliberately NOT cached:
//   - templateChart renders by repository reference (no .tgz in the chart
//     cache yet): the source is not pinned to a concrete artifact, so the same
//     name+version could resolve differently between runs;
//   - templateLocalChart (charts from git clones or local paths): the
//     directory content is not versioned, and hashing the whole chart tree
//     would be expensive and fragile.
//
// The cached value is the raw YAML output of helm template AFTER
// postRenderers have been applied (the key includes the postRenderers spec).
// A hit parses the cached YAML and skips the helm exec entirely. Writes are
// atomic (temp file + rename); a corrupt or unreadable entry is treated as a
// miss, never as an error. No TTL is needed — the key covers all inputs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// templateCacheDirName is the subdirectory of cacheDir holding cached renders.
const templateCacheDirName = "template-cache"

// templateCacheKeyInputs aggregates every input that influences the rendered
// output. The struct is serialized with encoding/json (which sorts map keys
// deterministically at every nesting level) and hashed with sha256.
type templateCacheKeyInputs struct {
	ChartName               string                 `json:"chartName"`
	ChartVersion            string                 `json:"chartVersion"`
	ReleaseName             string                 `json:"releaseName"`
	Namespace               string                 `json:"namespace"`
	SkipTests               bool                   `json:"skipTests"`
	IncludeCRDs             bool                   `json:"includeCRDs"`
	DisableSchemaValidation bool                   `json:"disableSchemaValidation"`
	Values                  map[string]interface{} `json:"values"`
	// ValuesFileHashes holds the sha256 of the CONTENT of each values file,
	// in --values order. File paths are deliberately excluded: the same
	// content at a different path must produce the same key, and the same
	// path with different content must not.
	ValuesFileHashes []string             `json:"valuesFileHashes"`
	PostRenderers    []types.PostRenderer `json:"postRenderers"`
	HelmVersion      string               `json:"helmVersion"`
	// KustomizeVersion is set only when postRenderers are present, because
	// only then does kustomize participate in producing the cached value.
	KustomizeVersion string `json:"kustomizeVersion,omitempty"`
}

// SetTemplateCacheEnabled toggles the persistent template cache. Disabled by
// default; enabled by callers that actually render (builder, test case)
// unless --no-template-cache is passed.
func (c *Client) SetTemplateCacheEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.templateCacheEnabled = enabled
}

// templateCacheIsEnabled reports whether the persistent cache is active.
func (c *Client) templateCacheIsEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.templateCacheEnabled
}

// TemplateCacheStats returns the number of cache hits and misses accumulated
// by this client. Reported in verbose output as
// "template cache: N hits, M misses".
func (c *Client) TemplateCacheStats() (hits, misses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.templateCacheHits, c.templateCacheMisses
}

func (c *Client) countTemplateCacheHit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.templateCacheHits++
}

func (c *Client) countTemplateCacheMiss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.templateCacheMisses++
}

// helmVersion returns the output of `helm version --short`, fetched at most
// once per client (the binary does not change within a run).
func (c *Client) helmVersion(ctx context.Context) (string, error) {
	c.helmVersionOnce.Do(func() {
		out, err := c.runner.Run(ctx, c.helmBin, "version", "--short")
		if err != nil {
			c.helmVersionErr = fmt.Errorf("helm version: %w", err)
			return
		}
		c.helmVersionVal = strings.TrimSpace(string(out))
	})
	return c.helmVersionVal, c.helmVersionErr
}

// kustomizeVersion returns the output of `kustomize version`, fetched at most
// once per client. Needed in the cache key only when postRenderers run.
func (c *Client) kustomizeVersion(ctx context.Context) (string, error) {
	c.kustomizeVersionOnce.Do(func() {
		out, err := c.runner.Run(ctx, "kustomize", "version")
		if err != nil {
			c.kustomizeVersionErr = fmt.Errorf("kustomize version: %w", err)
			return
		}
		c.kustomizeVersionVal = strings.TrimSpace(string(out))
	})
	return c.kustomizeVersionVal, c.kustomizeVersionErr
}

// templateCacheKey computes the cache key for a templateChart render. Any
// error (unreadable values file, tool version lookup failure, values that
// cannot be serialized) means the render simply is not cached.
func (c *Client) templateCacheKey(ctx context.Context, chartName, version string, options TemplateOptions) (string, error) {
	helmVer, err := c.helmVersion(ctx)
	if err != nil {
		return "", err
	}

	inputs := templateCacheKeyInputs{
		ChartName:               chartName,
		ChartVersion:            version,
		ReleaseName:             options.ReleaseName,
		Namespace:               options.Namespace,
		SkipTests:               options.SkipTests,
		IncludeCRDs:             options.IncludeCRDs,
		DisableSchemaValidation: options.DisableSchemaValidation,
		Values:                  options.Values,
		PostRenderers:           options.PostRenderers,
		HelmVersion:             helmVer,
	}

	for _, vf := range options.ValuesFiles {
		content, err := os.ReadFile(vf)
		if err != nil {
			return "", fmt.Errorf("read values file %s: %w", vf, err)
		}
		sum := sha256.Sum256(content)
		inputs.ValuesFileHashes = append(inputs.ValuesFileHashes, hex.EncodeToString(sum[:]))
	}

	if len(options.PostRenderers) > 0 {
		kustomizeVer, err := c.kustomizeVersion(ctx)
		if err != nil {
			return "", err
		}
		inputs.KustomizeVersion = kustomizeVer
	}

	encoded, err := json.Marshal(inputs)
	if err != nil {
		return "", fmt.Errorf("marshal template cache key: %w", err)
	}

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// templateCachePath returns the on-disk location for a cache key.
func (c *Client) templateCachePath(key string) string {
	return filepath.Join(c.cacheDir, templateCacheDirName, key+".yaml")
}

// templateCacheLookup reads a cached render. A missing or unreadable file is
// a miss, never an error.
func (c *Client) templateCacheLookup(key string) ([]byte, bool) {
	data, err := os.ReadFile(c.templateCachePath(key))
	if err != nil {
		return nil, false
	}
	return data, true
}

// templateCacheStore writes a rendered output atomically (temp file in the
// same directory + rename), so concurrent readers never observe a partial
// file. Failures are silently ignored — the cache is best-effort.
func (c *Client) templateCacheStore(key string, data []byte) {
	dir := filepath.Join(c.cacheDir, templateCacheDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, c.templateCachePath(key)); err != nil {
		os.Remove(tmpName)
	}
}
