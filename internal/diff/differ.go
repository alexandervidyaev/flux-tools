package diff

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
)

// CompareCluster compares manifests for a single cluster
func CompareCluster(ctx context.Context, clusterName string, currentDir string, incomingDir string, contextLines int) *ClusterDiff {
	_ = ctx // kept for API compatibility; the native diff needs no cancellation points

	result := &ClusterDiff{
		ClusterName: clusterName,
		Changes:     []*FileChange{},
	}

	// Find manifests in both directories
	currentManifests, err := FindManifests(currentDir)
	if err != nil {
		result.Error = fmt.Errorf("failed to find current manifests: %w", err)
		return result
	}

	incomingManifests, err := FindManifests(incomingDir)
	if err != nil {
		result.Error = fmt.Errorf("failed to find incoming manifests: %w", err)
		return result
	}

	// Track which files we've processed
	processed := make(map[string]bool)

	// Check for deleted and modified files
	for relPath, currentPath := range currentManifests {
		processed[relPath] = true

		if incomingPath, exists := incomingManifests[relPath]; exists {
			// File exists in both - check if modified
			change, err := compareFiles(relPath, currentPath, incomingPath, contextLines)
			if err != nil {
				continue // Skip files we can't compare
			}
			if change != nil {
				result.Changes = append(result.Changes, change)
			}
		} else {
			// File deleted
			resource := ParseResourceInfo(relPath)
			content, err := os.ReadFile(currentPath)
			if err != nil {
				// Surface the read failure instead of emitting an empty diff:
				// a silently blank deleted-resource body hides real content.
				result.Error = fmt.Errorf("failed to read deleted file %s: %w", relPath, err)
				return result
			}
			result.Changes = append(result.Changes, &FileChange{
				Resource: resource,
				Status:   StatusDeleted,
				Diff:     string(content),
			})
		}
	}

	// Check for added files
	for relPath, incomingPath := range incomingManifests {
		if !processed[relPath] {
			// File added
			resource := ParseResourceInfo(relPath)
			content, err := os.ReadFile(incomingPath)
			if err != nil {
				// Surface the read failure instead of emitting an empty diff:
				// a silently blank added-resource body hides real content.
				result.Error = fmt.Errorf("failed to read added file %s: %w", relPath, err)
				return result
			}
			result.Changes = append(result.Changes, &FileChange{
				Resource: resource,
				Status:   StatusAdded,
				Diff:     string(content),
			})
		}
	}

	// Changes are collected by iterating maps, so their order is random per
	// run — sort for a stable report (MR comments otherwise reshuffle
	// sections between pipeline runs on the same input).
	sort.Slice(result.Changes, func(i, j int) bool {
		a, b := result.Changes[i].Resource, result.Changes[j].Resource
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})

	return result
}

// compareFiles compares two files and returns a FileChange if they differ.
// The unified diff is generated in-process (see unified.go) instead of
// spawning `git diff --no-index` per file pair: the hunk content matches git
// byte for byte, only the file headers differ — CleanDiffLines strips them
// before any report is rendered.
func compareFiles(relPath string, currentPath string, incomingPath string, contextLines int) (*FileChange, error) {
	currentContent, err := os.ReadFile(currentPath)
	if err != nil {
		return nil, err
	}

	incomingContent, err := os.ReadFile(incomingPath)
	if err != nil {
		return nil, err
	}

	// Skip if files are identical
	if bytes.Equal(currentContent, incomingContent) {
		return nil, nil
	}

	hunks := unifiedDiff(currentContent, incomingContent, contextLines)
	if hunks == "" {
		return nil, nil
	}

	resource := ParseResourceInfo(relPath)
	return &FileChange{
		Resource: resource,
		Status:   StatusModified,
		Diff:     fmt.Sprintf("--- %s\n+++ %s\n%s", currentPath, incomingPath, hunks),
	}, nil
}
