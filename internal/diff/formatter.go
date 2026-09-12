package diff

import (
	"fmt"
	"strings"
)

// FormatClusterDiff formats a cluster diff for console output
func FormatClusterDiff(clusterDiff *ClusterDiff) string {
	var output strings.Builder

	if clusterDiff.Error != nil {
		output.WriteString(fmt.Sprintf("❌ Cluster: %s - Error: %v\n", clusterDiff.ClusterName, clusterDiff.Error))
		return output.String()
	}

	if len(clusterDiff.Changes) == 0 {
		return "" // No changes, no output
	}

	// Cluster header
	output.WriteString(fmt.Sprintf("\n📦 Cluster: %s\n", clusterDiff.ClusterName))
	output.WriteString(fmt.Sprintf("   Changes: %d\n\n", len(clusterDiff.Changes)))

	// Output each change
	for _, change := range clusterDiff.Changes {
		formatChange(&output, change)
	}

	return output.String()
}

// formatChange formats a single file change
func formatChange(output *strings.Builder, change *FileChange) {
	// Header line
	output.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	output.WriteString(fmt.Sprintf("⚙️  | Kind:%s | Namespace:%s | Name:%s | Status:%s\n",
		change.Resource.Kind,
		change.Resource.Namespace,
		change.Resource.Name,
		change.Status,
	))
	output.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	// Diff content
	if change.Status == StatusDeleted {
		output.WriteString("(File deleted)\n\n")
		return
	}

	if change.Status == StatusAdded {
		output.WriteString("(File added)\n")
		// Show first 10 lines of new content
		lines := strings.Split(change.Diff, "\n")
		maxLines := 10
		if len(lines) < maxLines {
			maxLines = len(lines)
		}
		for i := 0; i < maxLines; i++ {
			output.WriteString(fmt.Sprintf("+ %s\n", lines[i]))
		}
		if len(lines) > maxLines {
			output.WriteString(fmt.Sprintf("... (%d more lines)\n", len(lines)-maxLines))
		}
		output.WriteString("\n")
		return
	}

	if change.Status == StatusModified {
		// Show diff output, removing only file headers
		lines := strings.Split(change.Diff, "\n")
		for i, line := range lines {
			// Skip diff file header lines
			if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
				continue
			}
			// Skip hunk headers (they're not useful in our output)
			if strings.HasPrefix(line, "@@") {
				continue
			}
			// Skip empty line only at the very end
			if line == "" && i == len(lines)-1 {
				continue
			}
			output.WriteString(line + "\n")
		}
		output.WriteString("\n")
	}
}

// FormatAllDiffs formats all cluster diffs
func FormatAllDiffs(diffs []*ClusterDiff) string {
	var output strings.Builder

	totalChanges := 0
	clustersWithChanges := 0

	for _, diff := range diffs {
		if diff.Error == nil && len(diff.Changes) > 0 {
			clustersWithChanges++
			totalChanges += len(diff.Changes)
		}
	}

	// Summary header
	output.WriteString("\n")
	output.WriteString("═══════════════════════════════════════════════════════\n")
	output.WriteString(fmt.Sprintf("  DIFF SUMMARY: %d clusters, %d changes\n", clustersWithChanges, totalChanges))
	output.WriteString("═══════════════════════════════════════════════════════\n")

	// Output each cluster diff
	for _, diff := range diffs {
		formatted := FormatClusterDiff(diff)
		if formatted != "" {
			output.WriteString(formatted)
		}
	}

	// Footer
	output.WriteString("═══════════════════════════════════════════════════════\n")
	output.WriteString(fmt.Sprintf("  Total: %d changes across %d clusters\n", totalChanges, clustersWithChanges))
	output.WriteString("═══════════════════════════════════════════════════════\n")

	return output.String()
}
