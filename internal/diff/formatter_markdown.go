package diff

import (
	"fmt"
	"strings"
)

// FormatClusterDiffMarkdown formats a cluster diff as Markdown for GitLab MR
func FormatClusterDiffMarkdown(clusterDiff *ClusterDiff) string {
	var output strings.Builder

	if clusterDiff.Error != nil {
		output.WriteString(fmt.Sprintf("## ❌ Error\n\n```\n%v\n```\n", clusterDiff.Error))
		return output.String()
	}

	if len(clusterDiff.Changes) == 0 {
		output.WriteString("## ✅ No changes\n\n")
		output.WriteString("All manifests are identical between branches.\n")
		return output.String()
	}

	// Separate changes by type
	var addedChanges []*FileChange
	var deletedChanges []*FileChange
	var modifiedChanges []*FileChange

	for _, change := range clusterDiff.Changes {
		switch change.Status {
		case StatusAdded:
			addedChanges = append(addedChanges, change)
		case StatusDeleted:
			deletedChanges = append(deletedChanges, change)
		case StatusModified:
			modifiedChanges = append(modifiedChanges, change)
		}
	}

	// Added section - show full details
	if len(addedChanges) > 0 {
		output.WriteString(fmt.Sprintf("### ➕ Added (%d):\n\n", len(addedChanges)))
		for i, change := range addedChanges {
			formatChangeMarkdown(&output, change, i+1)
		}
	}

	// Deleted section - show full details with minus prefix
	if len(deletedChanges) > 0 {
		output.WriteString(fmt.Sprintf("### ➖ Deleted (%d):\n\n", len(deletedChanges)))
		for i, change := range deletedChanges {
			formatChangeMarkdown(&output, change, i+1)
		}
	}

	// Modified section - show full details
	if len(modifiedChanges) > 0 {
		output.WriteString(fmt.Sprintf("### ⚙️ Modified (%d):\n\n", len(modifiedChanges)))
		for i, change := range modifiedChanges {
			formatChangeMarkdown(&output, change, i+1)
		}
	}

	return output.String()
}

// formatChangeMarkdown formats a single file change as Markdown
func formatChangeMarkdown(output *strings.Builder, change *FileChange, index int) {
	ns := change.Resource.Namespace
	if ns == "" {
		ns = "(none)"
	}

	// Change header with format: N. Namespace:X | Kind:Y | Name:Z
	output.WriteString(fmt.Sprintf("#### %d. Namespace:%s | Kind:%s | Name:%s\n\n",
		index,
		ns,
		change.Resource.Kind,
		change.Resource.Name))

	// Diff content
	switch change.Status {
	case StatusDeleted:
		output.WriteString("```diff\n")
		// Show all deleted content with minus prefix
		lines := strings.Split(change.Diff, "\n")
		for i, line := range lines {
			// Skip only the very last empty line (trailing newline)
			if line == "" && i == len(lines)-1 {
				continue
			}
			output.WriteString("- " + line + "\n")
		}
		output.WriteString("```\n\n")

	case StatusAdded:
		output.WriteString("```diff\n")
		// Show all added content with plus prefix
		lines := strings.Split(change.Diff, "\n")
		for i, line := range lines {
			// Skip only the very last empty line (trailing newline)
			if line == "" && i == len(lines)-1 {
				continue
			}
			output.WriteString("+ " + line + "\n")
		}
		output.WriteString("```\n\n")

	case StatusModified:
		output.WriteString("```diff\n")
		for _, line := range CleanDiffLines(change.Diff) {
			output.WriteString(line + "\n")
		}
		output.WriteString("```\n\n")
	}
}

// CleanDiffLines strips git-specific noise (diff --git, index, file and hunk
// headers) and the trailing empty line from a raw unified diff, returning the
// lines to render. Shared by the markdown formatter and the poster's compact
// format so both produce the same output from a raw `git diff`.
func CleanDiffLines(rawDiff string) []string {
	lines := strings.Split(rawDiff, "\n")
	cleaned := make([]string, 0, len(lines))
	for i, line := range lines {
		// Skip git-specific headers
		if strings.HasPrefix(line, "diff --git") {
			continue
		}
		if strings.HasPrefix(line, "index ") {
			continue
		}
		// Skip file headers
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}
		// Skip hunk headers - they're less useful in Markdown
		if strings.HasPrefix(line, "@@") {
			continue
		}
		// Skip only the very last empty line (trailing newline)
		if line == "" && i == len(lines)-1 {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return cleaned
}
