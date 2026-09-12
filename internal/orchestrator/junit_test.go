package orchestrator

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
)

func TestMarshalJUnitReport(t *testing.T) {
	tests := []struct {
		name          string
		results       *AggregatedResults
		wantTests     int
		wantFailures  int
		wantSkipped   int
		wantSuites    int
		wantSubstring []string
	}{
		{
			name: "passed only",
			results: &AggregatedResults{
				Duration: 1500 * time.Millisecond,
				Clusters: []ClusterResult{{
					Name:     "kube-dev-1",
					Passed:   true,
					Duration: 1200 * time.Millisecond,
					TestResults: &test.TestResults{
						Tests: []test.TestResult{
							{Name: "apps", Type: test.KustomizationTest, Status: test.TestPassed, Duration: 300 * time.Millisecond},
							{Name: "monitoring/prometheus", Type: test.HelmReleaseTest, Status: test.TestPassed, Duration: 900 * time.Millisecond},
						},
					},
				}},
			},
			wantTests:     2,
			wantFailures:  0,
			wantSkipped:   0,
			wantSuites:    1,
			wantSubstring: []string{`classname="kube-dev-1"`, `name="apps"`},
		},
		{
			name: "failed with special characters",
			results: &AggregatedResults{
				Clusters: []ClusterResult{{
					Name: "kube-prod",
					TestResults: &test.TestResults{
						Tests: []test.TestResult{
							{Name: "broken", Status: test.TestFailed, Error: errors.New(`kustomize build failed: <path> && "quotes"`)},
						},
					},
				}},
			},
			wantTests:     1,
			wantFailures:  1,
			wantSuites:    1,
			wantSubstring: []string{"&lt;path&gt;", "&amp;&amp;"},
		},
		{
			name: "skipped",
			results: &AggregatedResults{
				Clusters: []ClusterResult{{
					Name: "kube-stage",
					TestResults: &test.TestResults{
						Tests: []test.TestResult{
							{Name: "oci-app", Status: test.TestSkipped},
							{Name: "apps", Status: test.TestPassed},
						},
					},
				}},
			},
			wantTests:     2,
			wantSkipped:   1,
			wantSuites:    1,
			wantSubstring: []string{"<skipped"},
		},
		{
			name: "cluster error without test results",
			results: &AggregatedResults{
				Clusters: []ClusterResult{{
					Name:  "kube-broken",
					Error: errors.New("failed to create test runner"),
				}},
			},
			wantTests:     1,
			wantFailures:  1,
			wantSuites:    1,
			wantSubstring: []string{"failed to create test runner"},
		},
		{
			name: "multiple clusters",
			results: &AggregatedResults{
				Clusters: []ClusterResult{
					{
						Name: "cluster-a",
						TestResults: &test.TestResults{
							Tests: []test.TestResult{{Name: "apps", Status: test.TestPassed}},
						},
					},
					{
						Name: "cluster-b",
						TestResults: &test.TestResults{
							Tests: []test.TestResult{{Name: "apps", Status: test.TestFailed, Error: errors.New("boom")}},
						},
					},
				},
			},
			wantTests:    2,
			wantFailures: 1,
			wantSuites:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := MarshalJUnitReport(tt.results)
			if err != nil {
				t.Fatalf("MarshalJUnitReport: %v", err)
			}

			// The output must be valid XML that round-trips back.
			var parsed junitTestSuites
			if err := xml.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("output is not valid XML: %v\n%s", err, data)
			}

			if parsed.Tests != tt.wantTests {
				t.Errorf("tests = %d, want %d", parsed.Tests, tt.wantTests)
			}
			if parsed.Failures != tt.wantFailures {
				t.Errorf("failures = %d, want %d", parsed.Failures, tt.wantFailures)
			}
			if parsed.Skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", parsed.Skipped, tt.wantSkipped)
			}
			if len(parsed.Suites) != tt.wantSuites {
				t.Errorf("suites = %d, want %d", len(parsed.Suites), tt.wantSuites)
			}
			for _, sub := range tt.wantSubstring {
				if !strings.Contains(string(data), sub) {
					t.Errorf("output does not contain %q:\n%s", sub, data)
				}
			}
		})
	}
}

func TestMarshalJUnitReportFailureRoundTrip(t *testing.T) {
	errText := `error with <tags>, "quotes" & newline
second line`
	results := &AggregatedResults{
		Clusters: []ClusterResult{{
			Name: "kube-dev",
			TestResults: &test.TestResults{
				Tests: []test.TestResult{
					{Name: "broken", Status: test.TestFailed, Error: errors.New(errText)},
				},
			},
		}},
	}

	data, err := MarshalJUnitReport(results)
	if err != nil {
		t.Fatalf("MarshalJUnitReport: %v", err)
	}

	var parsed junitTestSuites
	if err := xml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, data)
	}

	failure := parsed.Suites[0].Cases[0].Failure
	if failure == nil {
		t.Fatal("failure element missing")
	}
	// encoding/xml escapes newlines in attributes as character references
	// (&#xA;), so both the attribute and the body round-trip verbatim.
	if failure.Message != errText {
		t.Errorf("failure message = %q, want %q", failure.Message, errText)
	}
	if failure.Content != errText {
		t.Errorf("failure content = %q, want %q", failure.Content, errText)
	}
}

func TestWriteJUnitReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reports", "junit.xml")

	results := &AggregatedResults{
		Clusters: []ClusterResult{{
			Name: "kube-dev",
			TestResults: &test.TestResults{
				Tests: []test.TestResult{{Name: "apps", Status: test.TestPassed}},
			},
		}},
	}

	if err := WriteJUnitReport(path, results); err != nil {
		t.Fatalf("WriteJUnitReport: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.HasPrefix(string(data), xml.Header) {
		t.Errorf("report does not start with XML header:\n%s", data)
	}
	var parsed junitTestSuites
	if err := xml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("report is not valid XML: %v", err)
	}
}
