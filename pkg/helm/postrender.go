package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alexandervidyaev/flux-tools/pkg/types"

	"sigs.k8s.io/yaml"
)

// postRenderKustomization is the on-disk kustomization.yaml we synthesize for
// each Flux PostRenderer entry. Only the fields we actually emit are listed.
type postRenderKustomization struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Resources  []string          `json:"resources,omitempty"`
	Patches    []postRenderPatch `json:"patches,omitempty"`
	Images     []types.Image     `json:"images,omitempty"`
}

// postRenderPatch mirrors kustomize's `patches[]` entry: an inline patch body
// plus an optional target selector. Strategic-merge, JSON6902 and the unified
// `patches` form from spec.postRenderers all collapse into this shape, matching
// helm-controller's KustomizePostRenderer.
type postRenderPatch struct {
	Patch  string          `json:"patch,omitempty"`
	Target *types.Selector `json:"target,omitempty"`
}

// applyPostRenderers iteratively applies each Flux PostRenderer to the rendered
// manifests, mirroring helm-controller's CombinedPostRenderer: one kustomize
// build per element of spec.postRenderers, with the output of step N feeding
// step N+1.
func (c *Client) applyPostRenderers(ctx context.Context, input []byte, prs []types.PostRenderer) ([]byte, error) {
	out := input
	for i, pr := range prs {
		if pr.Kustomize == nil {
			continue
		}
		if !kustomizeHasWork(pr.Kustomize) {
			continue
		}
		rendered, err := c.kustomizePostRender(ctx, out, pr.Kustomize)
		if err != nil {
			return nil, fmt.Errorf("postRenderers[%d]: %w", i, err)
		}
		out = rendered
	}
	return out, nil
}

// kustomizeHasWork reports whether a Flux Kustomize PostRenderer has any
// directives that would change the rendered output. Empty entries are skipped
// to avoid unnecessary kustomize round-trips that would only reserialize YAML.
func kustomizeHasWork(k *types.Kustomize) bool {
	return len(k.Patches) > 0 ||
		len(k.PatchesStrategicMerge) > 0 ||
		len(k.PatchesJSON6902) > 0 ||
		len(k.Images) > 0
}

// kustomizePostRender writes the current rendered manifests plus a synthesized
// kustomization.yaml to a temp dir, runs `kustomize build` on it, and returns
// the new YAML.
func (c *Client) kustomizePostRender(ctx context.Context, input []byte, k *types.Kustomize) ([]byte, error) {
	tempDir, err := os.MkdirTemp(c.cacheDir, "postrender-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := os.WriteFile(filepath.Join(tempDir, "all.yaml"), input, 0644); err != nil {
		return nil, fmt.Errorf("write resources: %w", err)
	}

	kus := postRenderKustomization{
		APIVersion: "kustomize.config.k8s.io/v1beta1",
		Kind:       "Kustomization",
		Resources:  []string{"all.yaml"},
		Images:     k.Images,
	}

	for _, p := range k.Patches {
		kus.Patches = append(kus.Patches, postRenderPatch{
			Patch:  p.Patch,
			Target: p.Target,
		})
	}

	for _, p := range k.PatchesStrategicMerge {
		kus.Patches = append(kus.Patches, postRenderPatch{Patch: p})
	}

	for _, p := range k.PatchesJSON6902 {
		patchBytes, err := json.Marshal(p.Patch)
		if err != nil {
			return nil, fmt.Errorf("marshal patchesJson6902 ops: %w", err)
		}
		target := p.Target
		kus.Patches = append(kus.Patches, postRenderPatch{
			Patch:  string(patchBytes),
			Target: &target,
		})
	}

	kusYAML, err := yaml.Marshal(kus)
	if err != nil {
		return nil, fmt.Errorf("marshal kustomization.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "kustomization.yaml"), kusYAML, 0644); err != nil {
		return nil, fmt.Errorf("write kustomization.yaml: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.timeout)*time.Second)
	defer cancel()

	out, err := c.runner.Run(ctx, "kustomize", "build", "--load-restrictor=LoadRestrictionsNone", tempDir)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("kustomize build timed out after %d seconds", c.timeout)
		}
		return nil, fmt.Errorf("kustomize build: %w", err)
	}
	return out, nil
}
