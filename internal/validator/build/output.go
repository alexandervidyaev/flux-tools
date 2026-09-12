package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// OutputFormat represents the output format
type OutputFormat string

const (
	// OutputFormatYAML is the YAML output format
	OutputFormatYAML OutputFormat = "yaml"
	// OutputFormatJSON is the JSON output format
	OutputFormatJSON OutputFormat = "json"
)

// SerializeObjects serializes objects to the specified format.
// It is a thin wrapper over SerializeObjectsTo kept for callers that need
// the whole result in memory (for example, tests and diff helpers).
func SerializeObjects(objects []*unstructured.Unstructured, format OutputFormat) ([]byte, error) {
	var buf bytes.Buffer
	if err := SerializeObjectsTo(&buf, objects, format); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SerializeObjectsTo serializes objects to the specified format, writing
// the result to w object by object. This keeps memory usage flat: only one
// serialized object lives in memory at a time instead of the whole cluster.
// Objects are sorted before writing, so the output is byte-identical to the
// former in-memory serialization.
func SerializeObjectsTo(w io.Writer, objects []*unstructured.Unstructured, format OutputFormat) error {
	// Sort objects for stable output
	sortedObjects := sortObjects(objects)

	switch format {
	case OutputFormatYAML:
		return serializeToYAML(w, sortedObjects)
	case OutputFormatJSON:
		return serializeToJSON(w, sortedObjects)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// serializeToYAML serializes objects to YAML format with document separators
func serializeToYAML(w io.Writer, objects []*unstructured.Unstructured) error {
	for i, obj := range objects {
		// Add document separator (except for the first document)
		if i > 0 {
			if _, err := io.WriteString(w, "---\n"); err != nil {
				return fmt.Errorf("failed to write document separator: %w", err)
			}
		}

		// Convert to JSON first
		jsonData, err := obj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("failed to marshal object to JSON: %w", err)
		}

		// Convert JSON to YAML
		yamlData, err := yaml.JSONToYAML(jsonData)
		if err != nil {
			return fmt.Errorf("failed to convert JSON to YAML: %w", err)
		}

		if _, err := w.Write(yamlData); err != nil {
			return fmt.Errorf("failed to write YAML document: %w", err)
		}
	}

	return nil
}

// serializeToJSON serializes objects to JSON format (array of objects),
// pretty-printed with two-space indentation. Each object is marshaled
// separately and written immediately; the surrounding array punctuation is
// emitted manually so the output stays byte-identical to
// json.MarshalIndent over the whole slice.
func serializeToJSON(w io.Writer, objects []*unstructured.Unstructured) error {
	// json.MarshalIndent over a nil slice produces "null"; keep that
	// behavior for an empty object list.
	if len(objects) == 0 {
		if _, err := io.WriteString(w, "null"); err != nil {
			return fmt.Errorf("failed to write JSON output: %w", err)
		}
		return nil
	}

	if _, err := io.WriteString(w, "[\n  "); err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}

	for i, obj := range objects {
		if i > 0 {
			if _, err := io.WriteString(w, ",\n  "); err != nil {
				return fmt.Errorf("failed to write JSON output: %w", err)
			}
		}

		// The "  " prefix indents every line of the element to array
		// depth, matching json.MarshalIndent(list, "", "  ").
		data, err := json.MarshalIndent(obj.Object, "  ", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal object to JSON: %w", err)
		}

		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("failed to write JSON output: %w", err)
		}
	}

	if _, err := io.WriteString(w, "\n]"); err != nil {
		return fmt.Errorf("failed to write JSON output: %w", err)
	}

	return nil
}

// sortObjects sorts objects for stable output
// Sorting order: Namespace, APIVersion, Kind, Name
func sortObjects(objects []*unstructured.Unstructured) []*unstructured.Unstructured {
	// Create a copy to avoid modifying the original slice
	sorted := make([]*unstructured.Unstructured, len(objects))
	copy(sorted, objects)

	sort.Slice(sorted, func(i, j int) bool {
		// First, sort by namespace (cluster-scoped resources first)
		nsI := sorted[i].GetNamespace()
		nsJ := sorted[j].GetNamespace()

		if nsI == "" && nsJ != "" {
			return true
		}
		if nsI != "" && nsJ == "" {
			return false
		}
		if nsI != nsJ {
			return nsI < nsJ
		}

		// Then sort by kind priority
		priorityI := getKindPriority(sorted[i].GetKind())
		priorityJ := getKindPriority(sorted[j].GetKind())
		if priorityI != priorityJ {
			return priorityI < priorityJ
		}

		// Then by apiVersion
		if sorted[i].GetAPIVersion() != sorted[j].GetAPIVersion() {
			return sorted[i].GetAPIVersion() < sorted[j].GetAPIVersion()
		}

		// Then by kind
		if sorted[i].GetKind() != sorted[j].GetKind() {
			return sorted[i].GetKind() < sorted[j].GetKind()
		}

		// Finally by name
		return sorted[i].GetName() < sorted[j].GetName()
	})

	return sorted
}

// getKindPriority returns a priority value for sorting
// Lower values are sorted first
func getKindPriority(kind string) int {
	// Define priority order for common resources
	priorities := map[string]int{
		// Core resources
		"Namespace":                0,
		"CustomResourceDefinition": 1,
		"StorageClass":             2,
		"PersistentVolume":         3,
		"PersistentVolumeClaim":    4,
		"ConfigMap":                5,
		"Secret":                   6,
		"ServiceAccount":           7,
		"ClusterRole":              8,
		"ClusterRoleBinding":       9,
		"Role":                     10,
		"RoleBinding":              11,
		"Service":                  15,
		"Deployment":               20,
		"StatefulSet":              21,
		"DaemonSet":                22,
		"Job":                      23,
		"CronJob":                  24,
		"Ingress":                  30,
		"NetworkPolicy":            31,
		"HorizontalPodAutoscaler":  40,
		"PodDisruptionBudget":      41,
	}

	if priority, exists := priorities[kind]; exists {
		return priority
	}

	// Default priority for unknown kinds
	return 100
}
