package helm

import (
	"testing"
)

func TestGroupChartsByRepository_MultipleNamesPerURL(t *testing.T) {
	charts := []ChartRef{
		{
			Chart:      "teleport-cluster",
			Version:    "16.4.14",
			Repository: "https://charts.releases.teleport.dev",
			RepoName:   "teleport-agent-repo",
			IsOCI:      false,
		},
		{
			Chart:      "teleport-kube-agent",
			Version:    "16.4.14",
			Repository: "https://charts.releases.teleport.dev",
			RepoName:   "teleport-repo",
			IsOCI:      false,
		},
		{
			Chart:      "another-chart",
			Version:    "1.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "example-repo",
			IsOCI:      false,
		},
	}

	groups := groupChartsByRepository(charts)

	// Check that we have 2 repository URLs
	if len(groups) != 2 {
		t.Errorf("Expected 2 repository groups, got %d", len(groups))
	}

	// Check teleport repo has 2 names
	teleportGroup := groups["https://charts.releases.teleport.dev"]
	if teleportGroup == nil {
		t.Fatal("Expected teleport repository group to exist")
	}

	if len(teleportGroup.RepoNames) != 2 {
		t.Errorf("Expected 2 repository names for teleport, got %d", len(teleportGroup.RepoNames))
	}

	// Check both names are present
	hasAgent := false
	hasTeleportRepo := false
	for _, name := range teleportGroup.RepoNames {
		if name == "teleport-agent-repo" {
			hasAgent = true
		}
		if name == "teleport-repo" {
			hasTeleportRepo = true
		}
	}

	if !hasAgent {
		t.Error("Expected 'teleport-agent-repo' in repository names")
	}
	if !hasTeleportRepo {
		t.Error("Expected 'teleport-repo' in repository names")
	}

	// Check teleport repo has 2 charts
	if len(teleportGroup.Charts) != 2 {
		t.Errorf("Expected 2 charts for teleport, got %d", len(teleportGroup.Charts))
	}

	// Check example repo has 1 name
	exampleGroup := groups["https://charts.example.com"]
	if exampleGroup == nil {
		t.Fatal("Expected example repository group to exist")
	}

	if len(exampleGroup.RepoNames) != 1 {
		t.Errorf("Expected 1 repository name for example, got %d", len(exampleGroup.RepoNames))
	}

	if exampleGroup.RepoNames[0] != "example-repo" {
		t.Errorf("Expected 'example-repo', got '%s'", exampleGroup.RepoNames[0])
	}

	// Check example repo has 1 chart
	if len(exampleGroup.Charts) != 1 {
		t.Errorf("Expected 1 chart for example, got %d", len(exampleGroup.Charts))
	}
}

func TestGroupChartsByRepository_DuplicateNames(t *testing.T) {
	charts := []ChartRef{
		{
			Chart:      "chart-a",
			Version:    "1.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "example-repo",
			IsOCI:      false,
		},
		{
			Chart:      "chart-b",
			Version:    "2.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "example-repo", // Same name again
			IsOCI:      false,
		},
		{
			Chart:      "chart-c",
			Version:    "3.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "example-repo", // Same name again
			IsOCI:      false,
		},
	}

	groups := groupChartsByRepository(charts)

	// Check that we have 1 repository URL
	if len(groups) != 1 {
		t.Errorf("Expected 1 repository group, got %d", len(groups))
	}

	group := groups["https://charts.example.com"]
	if group == nil {
		t.Fatal("Expected repository group to exist")
	}

	// Should only have 1 unique name (duplicates removed)
	if len(group.RepoNames) != 1 {
		t.Errorf("Expected 1 unique repository name, got %d: %v", len(group.RepoNames), group.RepoNames)
	}

	if group.RepoNames[0] != "example-repo" {
		t.Errorf("Expected 'example-repo', got '%s'", group.RepoNames[0])
	}

	// Should have all 3 charts
	if len(group.Charts) != 3 {
		t.Errorf("Expected 3 charts, got %d", len(group.Charts))
	}
}

func TestGroupChartsByRepository_EmptyInput(t *testing.T) {
	charts := []ChartRef{}

	groups := groupChartsByRepository(charts)

	if len(groups) != 0 {
		t.Errorf("Expected 0 repository groups for empty input, got %d", len(groups))
	}
}

func TestGroupChartsByRepository_OCIRepository(t *testing.T) {
	charts := []ChartRef{
		{
			Chart:      "oci-chart",
			Version:    "1.0.0",
			Repository: "oci://registry.example.com/charts",
			RepoName:   "oci-repo",
			IsOCI:      true,
		},
		{
			Chart:      "http-chart",
			Version:    "2.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "http-repo",
			IsOCI:      false,
		},
	}

	groups := groupChartsByRepository(charts)

	// Check that we have 2 repository URLs
	if len(groups) != 2 {
		t.Errorf("Expected 2 repository groups, got %d", len(groups))
	}

	// Check OCI repo
	ociGroup := groups["oci://registry.example.com/charts"]
	if ociGroup == nil {
		t.Fatal("Expected OCI repository group to exist")
	}

	if !ociGroup.IsOCI {
		t.Error("Expected OCI repository to be marked as OCI")
	}

	if len(ociGroup.RepoNames) != 1 {
		t.Errorf("Expected 1 repository name for OCI, got %d", len(ociGroup.RepoNames))
	}

	if ociGroup.RepoNames[0] != "oci-repo" {
		t.Errorf("Expected 'oci-repo', got '%s'", ociGroup.RepoNames[0])
	}

	// Check HTTP repo
	httpGroup := groups["https://charts.example.com"]
	if httpGroup == nil {
		t.Fatal("Expected HTTP repository group to exist")
	}

	if httpGroup.IsOCI {
		t.Error("Expected HTTP repository to not be marked as OCI")
	}

	if len(httpGroup.RepoNames) != 1 {
		t.Errorf("Expected 1 repository name for HTTP, got %d", len(httpGroup.RepoNames))
	}

	if httpGroup.RepoNames[0] != "http-repo" {
		t.Errorf("Expected 'http-repo', got '%s'", httpGroup.RepoNames[0])
	}
}

func TestGroupChartsByRepository_MixedDuplicatesAndUnique(t *testing.T) {
	charts := []ChartRef{
		{
			Chart:      "chart-1",
			Version:    "1.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "repo-a",
			IsOCI:      false,
		},
		{
			Chart:      "chart-2",
			Version:    "2.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "repo-b",
			IsOCI:      false,
		},
		{
			Chart:      "chart-3",
			Version:    "3.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "repo-a", // Duplicate of first
			IsOCI:      false,
		},
		{
			Chart:      "chart-4",
			Version:    "4.0.0",
			Repository: "https://charts.example.com",
			RepoName:   "repo-c",
			IsOCI:      false,
		},
	}

	groups := groupChartsByRepository(charts)

	// Check that we have 1 repository URL
	if len(groups) != 1 {
		t.Errorf("Expected 1 repository group, got %d", len(groups))
	}

	group := groups["https://charts.example.com"]
	if group == nil {
		t.Fatal("Expected repository group to exist")
	}

	// Should have 3 unique names (repo-a, repo-b, repo-c)
	if len(group.RepoNames) != 3 {
		t.Errorf("Expected 3 unique repository names, got %d: %v", len(group.RepoNames), group.RepoNames)
	}

	// Check all expected names are present
	expectedNames := map[string]bool{"repo-a": false, "repo-b": false, "repo-c": false}
	for _, name := range group.RepoNames {
		if _, exists := expectedNames[name]; exists {
			expectedNames[name] = true
		} else {
			t.Errorf("Unexpected repository name: %s", name)
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("Expected repository name '%s' not found", name)
		}
	}

	// Should have all 4 charts
	if len(group.Charts) != 4 {
		t.Errorf("Expected 4 charts, got %d", len(group.Charts))
	}
}
