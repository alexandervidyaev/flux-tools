// Package orchestrator coordinates multi-cluster build and test operations.
package orchestrator

import (
	"time"

	"github.com/alexandervidyaev/flux-tools/internal/validator/test"
)

// Mode defines the operation mode.
type Mode int

const (
	ModeInvalid Mode = iota
	ModeSingle       // Single cluster
	ModeMulti        // Multiple clusters
)

// ClusterResult holds the result of processing a single cluster.
type ClusterResult struct {
	Name           string
	Passed         bool
	TestResults    *test.TestResults // For the test command
	CapturedOutput string            // Captured validator output (used in multi mode)
	Duration       time.Duration
	Error          error
}

// AggregatedResults holds combined results for all clusters.
type AggregatedResults struct {
	Clusters []ClusterResult
	Total    int
	Passed   int
	Failed   int
	Duration time.Duration
}
