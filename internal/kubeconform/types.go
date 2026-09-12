// Package kubeconform provides Kubernetes manifest validation using the kubeconform tool.
package kubeconform

import "time"

// ValidationResult represents the result of validating a single manifest file
type ValidationResult struct {
	File     string        // Path to the validated file
	Output   string        // Raw output from kubeconform
	ExitCode int           // Exit code from kubeconform
	Duration time.Duration // Time taken to validate
	Err      error         // Error if validation failed to run
}

// Passed returns true if validation passed (exit code 0)
func (r *ValidationResult) Passed() bool {
	return r.ExitCode == 0 && r.Err == nil
}
