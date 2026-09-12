package helm

import (
	"fmt"
	"strings"
)

// ChartRef represents a unique Helm chart reference
type ChartRef struct {
	// Repository URL (e.g., "https://charts.bitnami.com/bitnami" or "oci://registry.example.com")
	Repository string

	// RepoName is the name of the HelmRepository resource
	RepoName string

	// Chart is the name of the chart
	Chart string

	// Version is the chart version
	Version string

	// IsOCI indicates if this is an OCI registry
	IsOCI bool

	// Namespace is the namespace of the HelmRelease (for context)
	Namespace string

	// ReleaseName is the name of the HelmRelease (for context)
	ReleaseName string
}

// Key returns a unique key for deduplication
func (c *ChartRef) Key() string {
	return fmt.Sprintf("%s/%s@%s", c.Repository, c.Chart, c.Version)
}

// String returns a human-readable representation
func (c *ChartRef) String() string {
	if c.IsOCI {
		// Repository may already contain oci:// prefix, don't duplicate it
		repo := c.Repository
		if !strings.HasPrefix(repo, "oci://") {
			repo = "oci://" + repo
		}
		return fmt.Sprintf("%s/%s:%s", repo, c.Chart, c.Version)
	}
	return fmt.Sprintf("%s/%s@%s (from %s)", c.RepoName, c.Chart, c.Version, c.Repository)
}
