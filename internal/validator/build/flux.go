package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/exec"
	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func (b *Builder) processNativeKustomization(ctx context.Context, ks *types.Kustomization, vars map[string]string) ([]*unstructured.Unstructured, error) {
	if b.options.SkipOCI && ks.Spec.SourceRef.Kind == "OCIRepository" {
		return nil, nil
	}
	dir, err := b.ResolveKustomizationBuildPath(ctx, ks)
	if err != nil {
		return nil, err
	}
	return b.fluxBuild(ctx, ks, dir, vars)
}

func (b *Builder) fluxBuild(ctx context.Context, ks *types.Kustomization, dir string, vars map[string]string) ([]*unstructured.Unstructured, error) {
	var raw []byte
	var err error
	if ks.RawObject != nil {
		raw, err = json.Marshal(ks.RawObject)
	} else {
		raw, err = json.Marshal(ks)
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	spec, ok := doc["spec"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("kustomization %s has no spec", ks.GetKey())
	}
	delete(spec, "postBuild")
	if vars != nil {
		spec["postBuild"] = map[string]interface{}{"substitute": vars}
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "flux-kustomization-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	args := []string{"build", "kustomization", ks.Name, "--namespace", ks.Namespace, "--dry-run", "--in-memory-build", "--kustomization-file", file.Name(), "--path", abs}
	if b.options.Strict && vars != nil {
		args = append(args, "--strict-substitute")
	}
	buildRoot := b.rootPath
	if rel, err := filepath.Rel(buildRoot, abs); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		buildRoot = b.options.CacheDir
	}
	out, err := (&exec.RealRunner{Dir: buildRoot}).Run(ctx, "flux", args...)
	if err != nil {
		return nil, fmt.Errorf("flux build %s: %w", ks.GetKey(), err)
	}
	return kustomize.ParseKustomizeOutput(out)
}

func (b *Builder) substitutionVars(ctx context.Context, ks *types.Kustomization, configMaps, secrets map[string]map[string]string) (map[string]string, error) {
	if ks.Spec.PostBuild == nil {
		return nil, nil
	}
	load := func() (map[string]string, error) {
		vars := map[string]string{"var_substitution_enabled": "true"}
		for _, ref := range ks.Spec.PostBuild.SubstituteFrom {
			key := types.GetObjectKey(ks.Namespace, ref.Name)
			var data map[string]string
			switch ref.Kind {
			case "ConfigMap":
				data = configMaps[key]
			case "Secret":
				data = secrets[key]
			default:
				return nil, fmt.Errorf("unsupported substituteFrom kind %s", ref.Kind)
			}
			if data == nil && !ref.Optional {
				return nil, fmt.Errorf("kustomization %s: required substituteFrom %s/%s missing", ks.GetKey(), ref.Kind, ref.Name)
			}
			for k, v := range data {
				if strings.HasPrefix(v, "ENC[") {
					return nil, fmt.Errorf("kustomization %s: substituteFrom %s/%s requires SOPS decryption", ks.GetKey(), ref.Kind, ref.Name)
				}
				vars[k] = v
			}
		}
		for k, v := range ks.Spec.PostBuild.Substitute {
			vars[k] = v
		}
		return vars, nil
	}
	vars, err := load()
	if err == nil {
		return vars, nil
	}
	dir, resolveErr := b.ResolveKustomizationBuildPath(ctx, ks)
	if resolveErr != nil {
		return nil, resolveErr
	}
	initial, buildErr := b.fluxBuild(ctx, ks, dir, nil)
	if buildErr != nil {
		return nil, buildErr
	}
	for key, values := range extractConfigMaps(initial) {
		configMaps[key] = values
	}
	for key, values := range extractSecrets(initial) {
		secrets[key] = values
	}
	return load()
}
