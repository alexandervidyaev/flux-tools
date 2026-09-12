package helm

import (
	"fmt"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"sigs.k8s.io/yaml"
)

// ValuesBuilder builds the final values map for a HelmRelease
type ValuesBuilder struct {
	// ConfigMaps and Secrets for value references
	configMaps map[string]map[string]string
	secrets    map[string]map[string]string
	// tolerateMissing: when true, missing ConfigMap/Secret references do not fail the
	// build; they are treated as empty (similar to valuesRef.Optional=true). Useful for
	// CI tests where cluster secrets are not available in the working directory.
	tolerateMissing bool
}

// NewValuesBuilder creates a new ValuesBuilder
func NewValuesBuilder() *ValuesBuilder {
	return &ValuesBuilder{
		configMaps: make(map[string]map[string]string),
		secrets:    make(map[string]map[string]string),
	}
}

// SetTolerateMissing enables tolerance for missing valuesFrom references.
// Used in test mode where cluster secrets are not available.
func (vb *ValuesBuilder) SetTolerateMissing(tolerate bool) {
	vb.tolerateMissing = tolerate
}

// BuildValues builds the final values map for a HelmRelease
func (vb *ValuesBuilder) BuildValues(hr *types.HelmRelease) (map[string]interface{}, error) {
	// Start with an empty map
	result := make(map[string]interface{})

	// Process ValuesFrom references first (in order)
	for _, valuesRef := range hr.Spec.ValuesFrom {
		values, err := vb.getValuesFromReference(hr.Namespace, valuesRef)
		if err != nil {
			if valuesRef.Optional {
				// Skip optional references that fail
				continue
			}
			if vb.tolerateMissing {
				// In test mode we inject a placeholder so that helm template does not
				// fail on checks like `.Values.foo | b64enc` (which expect a string,
				// not nil). If the ref has a targetPath, we place the placeholder there;
				// otherwise we cannot infer where the value is consumed, so we skip.
				if valuesRef.TargetPath != "" {
					placeholder := scalarAtPath(valuesRef.TargetPath, "flux-tools-test-missing")
					if err := mergeValues(result, placeholder); err != nil {
						return nil, fmt.Errorf("failed to merge placeholder for missing %s/%s: %w", valuesRef.Kind, valuesRef.Name, err)
					}
				}
				continue
			}
			return nil, err
		}

		// Merge values
		if err := mergeValues(result, values); err != nil {
			return nil, fmt.Errorf("failed to merge values from %s/%s: %w", valuesRef.Kind, valuesRef.Name, err)
		}
	}

	// Process inline Values last (they override ValuesFrom)
	if hr.Spec.Values != nil && hr.Spec.Values.Raw != nil {
		var inlineValues map[string]interface{}
		if err := yaml.Unmarshal(hr.Spec.Values.Raw, &inlineValues); err != nil {
			return nil, fmt.Errorf("failed to unmarshal inline values: %w", err)
		}

		// Merge inline values
		if err := mergeValues(result, inlineValues); err != nil {
			return nil, fmt.Errorf("failed to merge inline values: %w", err)
		}
	}

	return result, nil
}

// getValuesFromReference gets values from a ConfigMap or Secret reference
func (vb *ValuesBuilder) getValuesFromReference(namespace string, ref types.ValuesReference) (map[string]interface{}, error) {
	var data map[string]string
	var exists bool

	key := types.GetObjectKey(namespace, ref.Name)

	switch ref.Kind {
	case "ConfigMap":
		data, exists = vb.configMaps[key]
		if !exists {
			return nil, fmt.Errorf("ConfigMap %s not found", key)
		}

	case "Secret":
		data, exists = vb.secrets[key]
		if !exists {
			return nil, fmt.Errorf("secret %s not found", key)
		}

	default:
		return nil, fmt.Errorf("unsupported values reference kind: %s", ref.Kind)
	}

	// Get the specific key if specified
	valuesKey := ref.ValuesKey
	if valuesKey == "" {
		valuesKey = "values.yaml"
	}

	valuesData, exists := data[valuesKey]
	if !exists {
		return nil, fmt.Errorf("key %s not found in %s %s", valuesKey, ref.Kind, key)
	}

	// Parse YAML values
	var values map[string]interface{}
	if err := yaml.Unmarshal([]byte(valuesData), &values); err != nil {
		return nil, fmt.Errorf("failed to unmarshal values from %s/%s: %w", ref.Kind, key, err)
	}

	// If TargetPath is specified, wrap the values
	if ref.TargetPath != "" {
		values = wrapValuesAtPath(values, ref.TargetPath)
	}

	return values, nil
}

// mergeValues merges src into dst
// Values in src take precedence over values in dst
func mergeValues(dst, src map[string]interface{}) error {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			// Both dst and src have this key
			// If both are maps, merge recursively
			srcMap, srcIsMap := srcVal.(map[string]interface{})
			dstMap, dstIsMap := dstVal.(map[string]interface{})

			if srcIsMap && dstIsMap {
				if err := mergeValues(dstMap, srcMap); err != nil {
					return err
				}
				continue
			}
		}

		// Otherwise, just set the value (src overwrites dst)
		dst[key] = srcVal
	}

	return nil
}

// scalarAtPath builds nested maps following the dotted path and places a scalar
// value at the leaf. For example, scalarAtPath("foo.bar.baz", "x") returns
// {"foo": {"bar": {"baz": "x"}}}.
func scalarAtPath(path string, value interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	current := result
	parts := splitPath(path)
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
		} else {
			nested := make(map[string]interface{})
			current[part] = nested
			current = nested
		}
	}
	return result
}

// wrapValuesAtPath wraps values at a specific path (e.g., "foo.bar.baz")
func wrapValuesAtPath(values map[string]interface{}, path string) map[string]interface{} {
	// Parse path (simple implementation, doesn't handle array indices)
	// For example: "foo.bar.baz" becomes ["foo", "bar", "baz"]
	// We'll build nested maps from the end

	// For simplicity, we'll use a JSON-based approach
	// Convert path to nested structure
	result := make(map[string]interface{})
	current := result

	// Split path by dots
	parts := splitPath(path)
	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part - assign the values
			current[part] = values
		} else {
			// Intermediate part - create nested map
			nested := make(map[string]interface{})
			current[part] = nested
			current = nested
		}
	}

	return result
}

// splitPath splits a path by dots
func splitPath(path string) []string {
	// Simple split by dots (doesn't handle escaped dots)
	var parts []string
	for part := range strings.SplitSeq(path, ".") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
