package orchestrator

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
)

// JUnit XML structures compatible with the format GitLab expects in
// artifacts:reports:junit. One testsuite per cluster, one testcase per
// Kustomization/HelmRelease test.

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Content string `xml:",chardata"`
}

type junitSkipped struct{}

// MarshalJUnitReport serializes aggregated test results into JUnit XML bytes.
func MarshalJUnitReport(results *AggregatedResults) ([]byte, error) {
	root := junitTestSuites{
		Time: junitSeconds(results.Duration.Seconds()),
	}

	for _, cluster := range results.Clusters {
		suite := junitTestSuite{
			Name: cluster.Name,
			Time: junitSeconds(cluster.Duration.Seconds()),
		}

		if cluster.TestResults != nil {
			for _, tr := range cluster.TestResults.Tests {
				tc := junitTestCase{
					Name:      tr.Name,
					Classname: cluster.Name,
					Time:      junitSeconds(tr.Duration.Seconds()),
				}
				switch tr.Status {
				case test.TestFailed:
					msg := "test failed"
					if tr.Error != nil {
						msg = tr.Error.Error()
					}
					tc.Failure = &junitFailure{Message: msg, Content: msg}
				case test.TestSkipped:
					tc.Skipped = &junitSkipped{}
				}
				suite.Cases = append(suite.Cases, tc)
			}
		}

		// A cluster that failed before producing test results (e.g. runner
		// setup error) still has to be visible in the report.
		if cluster.Error != nil && cluster.TestResults == nil {
			msg := cluster.Error.Error()
			suite.Cases = append(suite.Cases, junitTestCase{
				Name:      cluster.Name,
				Classname: cluster.Name,
				Time:      junitSeconds(cluster.Duration.Seconds()),
				Failure:   &junitFailure{Message: msg, Content: msg},
			})
		}

		for _, tc := range suite.Cases {
			suite.Tests++
			if tc.Failure != nil {
				suite.Failures++
			}
			if tc.Skipped != nil {
				suite.Skipped++
			}
		}

		root.Tests += suite.Tests
		root.Failures += suite.Failures
		root.Skipped += suite.Skipped
		root.Suites = append(root.Suites, suite)
	}

	body, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JUnit report: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// WriteJUnitReport writes aggregated test results as a JUnit XML file,
// creating parent directories when needed.
func WriteJUnitReport(path string, results *AggregatedResults) error {
	data, err := MarshalJUnitReport(results)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create report directory: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write JUnit report: %w", err)
	}
	return nil
}

// junitSeconds formats a duration in seconds the way JUnit consumers expect.
func junitSeconds(s float64) string {
	return strconv.FormatFloat(s, 'f', 3, 64)
}
