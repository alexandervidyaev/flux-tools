package kubeconform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sample captured from kubeconform 0.7.0: -output json on one invalid and
// one unparseable manifest, with -summary.
const sampleJSONOutput = `{
  "resources": [
    {
      "filename": "broken-yaml.yaml",
      "kind": "",
      "name": "",
      "version": "",
      "status": "statusError",
      "msg": "error unmarshalling resource: error converting YAML to JSON: yaml: line 2: mapping values are not allowed in this context"
    },
    {
      "filename": "bad.yaml",
      "kind": "Deployment",
      "name": "bad-deploy",
      "version": "apps/v1",
      "status": "statusInvalid",
      "msg": "problem validating schema: got string, want integer",
      "validationErrors": [
        {"path": "/spec/replicas", "msg": "got string, want null or integer"}
      ]
    }
  ],
  "summary": {"valid": 1, "invalid": 1, "errors": 1, "skipped": 0}
}`

func TestParseJSONOutput(t *testing.T) {
	resources, err := ParseJSONOutput([]byte(sampleJSONOutput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}
	if resources[0].Status != StatusError || resources[0].Filename != "broken-yaml.yaml" {
		t.Errorf("resources[0] = %+v, want statusError broken-yaml.yaml", resources[0])
	}
	if resources[1].Status != StatusInvalid || resources[1].Kind != "Deployment" || resources[1].Name != "bad-deploy" {
		t.Errorf("resources[1] = %+v, want statusInvalid Deployment/bad-deploy", resources[1])
	}

	if _, err := ParseJSONOutput([]byte("not json")); err == nil {
		t.Error("expected error on non-JSON input")
	}
}

func TestToCodeQuality(t *testing.T) {
	tests := []struct {
		name         string
		resource     Resource
		wantIssue    bool
		wantSeverity string
		wantDesc     string
		wantPath     string
	}{
		{
			name:         "invalid maps to major",
			resource:     Resource{Filename: "m/bad.yaml", Kind: "Deployment", Name: "bad-deploy", Status: StatusInvalid, Msg: "boom"},
			wantIssue:    true,
			wantSeverity: "major",
			wantDesc:     "Deployment/bad-deploy: boom",
			wantPath:     "m/bad.yaml",
		},
		{
			name:         "error maps to critical",
			resource:     Resource{Filename: "m/broken.yaml", Status: StatusError, Msg: "error unmarshalling resource"},
			wantIssue:    true,
			wantSeverity: "critical",
			wantDesc:     "error unmarshalling resource",
			wantPath:     "m/broken.yaml",
		},
		{
			name:     "valid is skipped",
			resource: Resource{Filename: "m/good.yaml", Kind: "ConfigMap", Name: "cm", Status: StatusValid},
		},
		{
			name:     "skipped is skipped",
			resource: Resource{Filename: "m/skip.yaml", Kind: "CustomResourceDefinition", Name: "crd", Status: StatusSkipped},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := ToCodeQuality([]Resource{tc.resource}, "")
			if !tc.wantIssue {
				if len(issues) != 0 {
					t.Fatalf("got %d issues, want 0", len(issues))
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("got %d issues, want 1", len(issues))
			}
			issue := issues[0]
			if issue.Severity != tc.wantSeverity {
				t.Errorf("severity=%q, want %q", issue.Severity, tc.wantSeverity)
			}
			if issue.Description != tc.wantDesc {
				t.Errorf("description=%q, want %q", issue.Description, tc.wantDesc)
			}
			if issue.CheckName != "kubeconform" {
				t.Errorf("check_name=%q, want kubeconform", issue.CheckName)
			}
			if issue.Location.Path != tc.wantPath {
				t.Errorf("location.path=%q, want %q", issue.Location.Path, tc.wantPath)
			}
			if issue.Location.Lines.Begin != 1 {
				t.Errorf("lines.begin=%d, want 1", issue.Location.Lines.Begin)
			}
		})
	}
}

func TestToCodeQualityFingerprint(t *testing.T) {
	base := Resource{Filename: "bad.yaml", Kind: "Deployment", Name: "bad-deploy", Status: StatusInvalid, Msg: "boom"}

	// Stable across calls and pinned to sha256(file+NUL+kind+NUL+name+NUL+msg).
	const want = "49633f8ef584e2a335280838fba22a3c76cfd23de3c7f40e39c202e15bfb62b2"
	for range 2 {
		issues := ToCodeQuality([]Resource{base}, "")
		if got := issues[0].Fingerprint; got != want {
			t.Fatalf("fingerprint=%q, want %q", got, want)
		}
	}

	// Any identity field change produces a different fingerprint.
	variants := []Resource{
		{Filename: "other.yaml", Kind: base.Kind, Name: base.Name, Status: StatusInvalid, Msg: base.Msg},
		{Filename: base.Filename, Kind: "StatefulSet", Name: base.Name, Status: StatusInvalid, Msg: base.Msg},
		{Filename: base.Filename, Kind: base.Kind, Name: "other", Status: StatusInvalid, Msg: base.Msg},
		{Filename: base.Filename, Kind: base.Kind, Name: base.Name, Status: StatusInvalid, Msg: "other msg"},
	}
	for i, v := range variants {
		if got := ToCodeQuality([]Resource{v}, "")[0].Fingerprint; got == want {
			t.Errorf("variant %d: fingerprint unchanged (%q)", i, got)
		}
	}
}

func TestToCodeQualityRelativePath(t *testing.T) {
	base := t.TempDir()
	abs := filepath.Join(base, "manifests", "bad.yaml")

	issues := ToCodeQuality([]Resource{{Filename: abs, Status: StatusInvalid, Msg: "x"}}, base)
	if got := issues[0].Location.Path; got != "manifests/bad.yaml" {
		t.Errorf("path=%q, want manifests/bad.yaml", got)
	}

	// A file outside baseDir keeps its absolute path.
	outside := filepath.Join(filepath.Dir(base), "elsewhere.yaml")
	issues = ToCodeQuality([]Resource{{Filename: outside, Status: StatusInvalid, Msg: "x"}}, base)
	if got := issues[0].Location.Path; got != filepath.ToSlash(outside) {
		t.Errorf("path=%q, want %q", got, filepath.ToSlash(outside))
	}

	// Relative input is cleaned, not rewritten.
	issues = ToCodeQuality([]Resource{{Filename: "./m/bad.yaml", Status: StatusInvalid, Msg: "x"}}, base)
	if got := issues[0].Location.Path; got != "m/bad.yaml" {
		t.Errorf("path=%q, want m/bad.yaml", got)
	}
}

func TestWriteCodeQualityReport(t *testing.T) {
	dir := t.TempDir()

	// Empty report is a valid [] document, parent dirs are created.
	path := filepath.Join(dir, "sub", "gl-codequality.json")
	if err := WriteCodeQualityReport(path, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Errorf("empty report = %q, want []", string(data))
	}

	// Non-empty report round-trips through JSON.
	issues := ToCodeQuality([]Resource{{Filename: "bad.yaml", Kind: "Deployment", Name: "d", Status: StatusInvalid, Msg: "boom"}}, "")
	if err := WriteCodeQualityReport(path, issues); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	var decoded []CodeQualityIssue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0] != issues[0] {
		t.Errorf("round-trip mismatch: %+v != %+v", decoded, issues)
	}
}
