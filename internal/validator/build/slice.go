package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OutputFormatSliced is the sliced output format: instead of one file per
// cluster, every object is written to its own file whose relative path is
// rendered from a kubectl-slice compatible filename template.
const OutputFormatSliced OutputFormat = "sliced"

// DefaultSliceTemplate is the default filename template for the sliced
// output format. It matches the template conventionally passed to
// kubectl-slice in CI pipelines, so the resulting file tree is
// byte-identical to the yq + kubectl-slice post-processing it replaces.
const DefaultSliceTemplate = `Namespace:{{.metadata.namespace|lower}}/Kind:{{.kind|lower}}/Name:{{.metadata.name|dottodash}}.yaml`

// sliceFuncs are the template filter functions supported in slice templates,
// mirroring the kubectl-slice functions of the same names.
var sliceFuncs = template.FuncMap{
	"lower":     strings.ToLower,
	"dottodash": func(s string) string { return strings.ReplaceAll(s, ".", "-") },
}

// Slicer renders per-object file names from a kubectl-slice compatible
// text/template and writes objects into a directory tree.
type Slicer struct {
	tmpl *template.Template
}

// NewSlicer parses the filename template and returns a Slicer.
// The template supports the fields .kind, .apiVersion, .metadata.name and
// .metadata.namespace, and the filter functions lower and dottodash
// (pipe syntax with or without spaces, as in kubectl-slice).
func NewSlicer(tpl string) (*Slicer, error) {
	t, err := template.New("slice").Funcs(sliceFuncs).Parse(tpl)
	if err != nil {
		return nil, fmt.Errorf("invalid slice template %q: %w", tpl, err)
	}
	return &Slicer{tmpl: t}, nil
}

// FileName renders the relative file path for the object. Fields absent on
// the object (for example, metadata.namespace of a cluster-scoped resource)
// render as empty strings, matching kubectl-slice behavior.
func (s *Slicer) FileName(obj *unstructured.Unstructured) (string, error) {
	// A fixed data map keeps missing fields rendering as "" instead of
	// text/template's "<no value>".
	data := map[string]interface{}{
		"kind":       obj.GetKind(),
		"apiVersion": obj.GetAPIVersion(),
		"metadata": map[string]interface{}{
			"name":      obj.GetName(),
			"namespace": obj.GetNamespace(),
		},
	}

	var buf bytes.Buffer
	if err := s.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render slice template for %s %s/%s: %w",
			obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
	}
	name := buf.String()
	if name == "" {
		return "", fmt.Errorf("slice template rendered an empty file name for %s %s/%s",
			obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}
	return name, nil
}

// WriteSlicedObjects writes every object into dir as an individual YAML file
// whose relative path is rendered by the slicer. Each file holds exactly one
// YAML document without a leading "---" separator and with a trailing
// newline — byte-identical to what kubectl-slice produces from the combined
// YAML output of the same objects. Two objects rendering to the same path
// are reported as an error.
func WriteSlicedObjects(dir string, objects []*unstructured.Unstructured, slicer *Slicer) error {
	// Sort the same way as the combined output for deterministic collision
	// reporting; file contents do not depend on the order.
	sorted := sortObjects(objects)

	// path -> human-readable identity of the object that claimed it
	seen := make(map[string]string, len(sorted))

	for _, obj := range sorted {
		id := describeObject(obj)

		relName, err := slicer.FileName(obj)
		if err != nil {
			return err
		}

		if prev, dup := seen[relName]; dup {
			return fmt.Errorf("slice file name collision: %s and %s both render to %q — use a more specific --slice-template", prev, id, relName)
		}
		seen[relName] = id

		filePath := filepath.Join(dir, filepath.FromSlash(relName))
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", filePath, err)
		}

		// Serialize exactly as one document of the combined YAML output.
		var buf bytes.Buffer
		if err := serializeToYAML(&buf, []*unstructured.Unstructured{obj}); err != nil {
			return fmt.Errorf("failed to serialize %s: %w", id, err)
		}
		if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filePath, err)
		}
	}

	return nil
}

// describeObject returns a human-readable object identity for error messages.
func describeObject(obj *unstructured.Unstructured) string {
	if ns := obj.GetNamespace(); ns != "" {
		return fmt.Sprintf("%s %s/%s", obj.GetKind(), ns, obj.GetName())
	}
	return fmt.Sprintf("%s %s", obj.GetKind(), obj.GetName())
}
