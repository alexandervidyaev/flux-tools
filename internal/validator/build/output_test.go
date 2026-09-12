package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// legacySerializeObjects is a verbatim copy of the pre-streaming
// SerializeObjects implementation (single []byte assembled via append).
// It serves as the golden reference: the streaming path must produce
// byte-identical output.
func legacySerializeObjects(objects []*unstructured.Unstructured, format OutputFormat) ([]byte, error) {
	sortedObjects := sortObjects(objects)

	switch format {
	case OutputFormatYAML:
		var result []byte
		for i, obj := range sortedObjects {
			if i > 0 {
				result = append(result, []byte("---\n")...)
			}
			jsonData, err := obj.MarshalJSON()
			if err != nil {
				return nil, err
			}
			yamlData, err := yaml.JSONToYAML(jsonData)
			if err != nil {
				return nil, err
			}
			result = append(result, yamlData...)
		}
		return result, nil
	case OutputFormatJSON:
		var objectList []map[string]interface{}
		for _, obj := range sortedObjects {
			objectList = append(objectList, obj.Object)
		}
		return json.MarshalIndent(objectList, "", "  ")
	default:
		return nil, os.ErrInvalid
	}
}

// goldenTestObjects returns a varied set of objects: different kinds,
// cluster-scoped and namespaced resources, multiline strings, nested
// structures, and special characters that exercise JSON escaping.
func goldenTestObjects() []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		{Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "web",
				"namespace": "prod",
				"labels":    map[string]interface{}{"app": "web", "tier": "frontend"},
			},
			"spec": map[string]interface{}{
				"replicas": int64(3),
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "web",
								"image": "nginx:1.27",
								"args":  []interface{}{"--flag=<value>", "a&b"},
							},
						},
					},
				},
			},
		}},
		{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "scripts",
				"namespace": "prod",
			},
			"data": map[string]interface{}{
				"run.sh":     "#!/bin/sh\nset -e\necho \"hello world\"\nexit 0\n",
				"multiline":  "line one\nline two\n  indented line\nlast line",
				"unicode":    "значение с кириллицей и emoji \U0001F680",
				"empty":      "",
				"whitespace": "trailing space \nand tab\t",
			},
		}},
		{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]interface{}{"name": "prod"},
		}},
		{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      "creds",
				"namespace": "staging",
			},
			"type":       "Opaque",
			"stringData": map[string]interface{}{"password": "p@ss<word>&"},
		}},
		{Object: map[string]interface{}{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata":   map[string]interface{}{"name": "reader"},
			"rules": []interface{}{
				map[string]interface{}{
					"apiGroups": []interface{}{""},
					"resources": []interface{}{"pods"},
					"verbs":     []interface{}{"get", "list"},
				},
			},
		}},
	}
}

// TestSerializeObjectsToGolden verifies byte identity between the legacy
// in-memory serialization, the SerializeObjects wrapper, and the streaming
// SerializeObjectsTo path writing into a file.
func TestSerializeObjectsToGolden(t *testing.T) {
	cases := []struct {
		name    string
		objects []*unstructured.Unstructured
	}{
		{"varied objects", goldenTestObjects()},
		{"single object", goldenTestObjects()[:1]},
		{"empty list", nil},
	}

	for _, format := range []OutputFormat{OutputFormatYAML, OutputFormatJSON} {
		for _, tc := range cases {
			t.Run(string(format)+"/"+tc.name, func(t *testing.T) {
				golden, err := legacySerializeObjects(tc.objects, format)
				if err != nil {
					t.Fatalf("legacy serialization failed: %v", err)
				}

				// Wrapper path (bytes.Buffer).
				wrapped, err := SerializeObjects(tc.objects, format)
				if err != nil {
					t.Fatalf("SerializeObjects failed: %v", err)
				}
				if !bytes.Equal(golden, wrapped) {
					t.Errorf("SerializeObjects output differs from legacy output\nlegacy:\n%s\nnew:\n%s", golden, wrapped)
				}

				// Streaming path into a real file.
				path := filepath.Join(t.TempDir(), "out")
				f, err := os.Create(path)
				if err != nil {
					t.Fatalf("failed to create temp file: %v", err)
				}
				if err := SerializeObjectsTo(f, tc.objects, format); err != nil {
					t.Fatalf("SerializeObjectsTo failed: %v", err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("failed to close temp file: %v", err)
				}
				streamed, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("failed to read temp file: %v", err)
				}
				if !bytes.Equal(golden, streamed) {
					t.Errorf("SerializeObjectsTo file output differs from legacy output\nlegacy:\n%s\nnew:\n%s", golden, streamed)
				}
			})
		}
	}
}

// TestSerializeObjectsToUnsupportedFormat verifies the error path.
func TestSerializeObjectsToUnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := SerializeObjectsTo(&buf, nil, OutputFormat("toml")); err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
}
