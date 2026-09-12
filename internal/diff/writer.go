package diff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SaveDiffsToFiles saves each cluster diff to a Markdown file (human-facing)
// plus a JSON file (machine-facing, consumed by post-comment instead of
// reverse-parsing the markdown), and writes an index.md summary over all
// clusters.
func SaveDiffsToFiles(diffs []*ClusterDiff, outputDir string) error {
	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Sort diffs by cluster name for consistent output
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].ClusterName < diffs[j].ClusterName
	})

	// Save each cluster diff to a separate file
	for _, clusterDiff := range diffs {
		filename := fmt.Sprintf("%s-diff.md", clusterDiff.ClusterName)

		// Format as Markdown
		content := FormatClusterDiffMarkdown(clusterDiff)

		// Write to file
		if err := os.WriteFile(filepath.Join(outputDir, filename), []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filename, err)
		}

		// Structured counterpart of the markdown file
		jsonName := fmt.Sprintf("%s-diff.json", clusterDiff.ClusterName)
		jsonData, err := MarshalClusterDiff(clusterDiff)
		if err != nil {
			return fmt.Errorf("failed to marshal %s: %w", jsonName, err)
		}
		if err := os.WriteFile(filepath.Join(outputDir, jsonName), jsonData, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", jsonName, err)
		}
	}

	// Summary over all clusters — README and post-comment promise this file
	return GenerateSummaryIndex(diffs, outputDir)
}

// GenerateSummaryIndex creates an index.md file with links to all cluster diffs
func GenerateSummaryIndex(diffs []*ClusterDiff, outputDir string) error {
	indexPath := filepath.Join(outputDir, "index.md")

	var b strings.Builder
	b.WriteString("# 🔄 Changes Summary\n\n")

	// Count total changes
	totalClusters := len(diffs)
	clustersWithChanges := 0
	totalChanges := 0
	totalAdded := 0
	totalDeleted := 0
	totalModified := 0

	for _, diff := range diffs {
		if diff.Error == nil && len(diff.Changes) > 0 {
			clustersWithChanges++
			totalChanges += len(diff.Changes)

			for _, change := range diff.Changes {
				switch change.Status {
				case StatusAdded:
					totalAdded++
				case StatusDeleted:
					totalDeleted++
				case StatusModified:
					totalModified++
				}
			}
		}
	}

	// Summary table
	b.WriteString("| Metric | Value |\n")
	b.WriteString("|--------|-------|\n")
	fmt.Fprintf(&b, "| Total clusters | %d |\n", totalClusters)
	fmt.Fprintf(&b, "| Clusters with changes | %d |\n", clustersWithChanges)
	fmt.Fprintf(&b, "| Total changes | %d |\n", totalChanges)
	fmt.Fprintf(&b, "| ➕ Added | %d |\n", totalAdded)
	fmt.Fprintf(&b, "| ➖ Deleted | %d |\n", totalDeleted)
	fmt.Fprintf(&b, "| ⚙️ Modified | %d |\n", totalModified)
	b.WriteString("\n")

	// Cluster list
	b.WriteString("## 📦 Clusters\n\n")

	// Sort diffs by cluster name
	sortedDiffs := make([]*ClusterDiff, len(diffs))
	copy(sortedDiffs, diffs)
	sort.Slice(sortedDiffs, func(i, j int) bool {
		return sortedDiffs[i].ClusterName < sortedDiffs[j].ClusterName
	})

	for _, diff := range sortedDiffs {
		icon := "✅"
		status := "No changes"
		if diff.Error != nil {
			icon = "❌"
			status = "Error"
		} else if len(diff.Changes) > 0 {
			icon = "⚙️"
			status = fmt.Sprintf("%d changes", len(diff.Changes))
		}

		fmt.Fprintf(&b, "- %s **%s** - %s\n", icon, diff.ClusterName, status)
	}

	if err := os.WriteFile(indexPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("failed to write index.md: %w", err)
	}

	return nil
}
