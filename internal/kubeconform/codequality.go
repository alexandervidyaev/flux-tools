package kubeconform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Status values emitted by kubeconform -output json (verified against
// kubeconform 0.7.x).
const (
	StatusValid   = "statusValid"
	StatusInvalid = "statusInvalid"
	StatusError   = "statusError"
	StatusSkipped = "statusSkipped"
)

// Resource is a single entry of kubeconform's -output json "resources" array.
type Resource struct {
	Filename string `json:"filename"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Status   string `json:"status"`
	Msg      string `json:"msg"`
}

// jsonOutput mirrors the top-level kubeconform -output json document. The
// optional "summary" object (present with -summary) is ignored.
type jsonOutput struct {
	Resources []Resource `json:"resources"`
}

// ParseJSONOutput decodes the stdout of kubeconform -output json into the
// list of validated resources.
func ParseJSONOutput(data []byte) ([]Resource, error) {
	var out jsonOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unexpected kubeconform JSON output: %w", err)
	}
	return out.Resources, nil
}

// CodeQualityIssue is one entry of the GitLab Code Quality report format
// (https://docs.gitlab.com/ee/ci/testing/code_quality.html#implement-a-custom-tool).
type CodeQualityIssue struct {
	Description string              `json:"description"`
	CheckName   string              `json:"check_name"`
	Fingerprint string              `json:"fingerprint"`
	Severity    string              `json:"severity"`
	Location    CodeQualityLocation `json:"location"`
}

// CodeQualityLocation points at the manifest file an issue belongs to.
type CodeQualityLocation struct {
	Path  string           `json:"path"`
	Lines CodeQualityLines `json:"lines"`
}

// CodeQualityLines carries the line position. Kubeconform does not report
// line numbers, so Begin is always 1.
type CodeQualityLines struct {
	Begin int `json:"begin"`
}

// ToCodeQuality converts kubeconform JSON resources into GitLab Code Quality
// issues. Valid and skipped resources produce no issues; invalid resources
// map to severity "major", errors (unparseable manifests etc.) to "critical".
// baseDir, when non-empty, is used to make absolute manifest paths relative
// (GitLab expects paths relative to the repository root, which in CI is the
// working directory). The result is never nil: zero issues is a valid,
// intentionally empty report.
func ToCodeQuality(resources []Resource, baseDir string) []CodeQualityIssue {
	issues := make([]CodeQualityIssue, 0, len(resources))
	for _, r := range resources {
		var severity string
		switch r.Status {
		case StatusInvalid:
			severity = "major"
		case StatusError:
			severity = "critical"
		default:
			continue
		}

		description := r.Msg
		if r.Kind != "" || r.Name != "" {
			description = fmt.Sprintf("%s/%s: %s", r.Kind, r.Name, r.Msg)
		}

		issues = append(issues, CodeQualityIssue{
			Description: description,
			CheckName:   "kubeconform",
			Fingerprint: fingerprint(r),
			Severity:    severity,
			Location: CodeQualityLocation{
				Path:  relativePath(r.Filename, baseDir),
				Lines: CodeQualityLines{Begin: 1},
			},
		})
	}
	return issues
}

// fingerprint derives a stable issue identity from file+kind+name+message,
// so GitLab can track the same finding across pipelines. Fields are joined
// with a NUL separator to avoid accidental collisions.
func fingerprint(r Resource) string {
	sum := sha256.Sum256([]byte(r.Filename + "\x00" + r.Kind + "\x00" + r.Name + "\x00" + r.Msg))
	return hex.EncodeToString(sum[:])
}

// relativePath rewrites an absolute manifest path relative to baseDir when
// the file lives under it; relative paths are only cleaned. Slashes are
// normalized so the report is stable across platforms.
func relativePath(path, baseDir string) string {
	if baseDir != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(baseDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

// WriteCodeQualityReport writes issues as a GitLab Code Quality JSON file,
// creating parent directories when needed. An empty issue list produces a
// valid empty report ([]).
func WriteCodeQualityReport(path string, issues []CodeQualityIssue) error {
	if issues == nil {
		issues = []CodeQualityIssue{}
	}
	data, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal Code Quality report: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create report directory: %w", err)
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("failed to write Code Quality report: %w", err)
	}
	return nil
}
