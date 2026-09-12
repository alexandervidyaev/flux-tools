package diff

import (
	"context"
	"path/filepath"

	"github.com/alexandervidyaev/flux-tools/pkg/workerpool"
)

// CompareClustersParallel compares clusters in parallel
func CompareClustersParallel(ctx context.Context, clusters []string, currentBaseDir string, incomingBaseDir string, contextLines int, workers int) []*ClusterDiff {
	return workerpool.Run(ctx, clusters, workers, func(ctx context.Context, clusterName string) *ClusterDiff {
		currentDir := filepath.Join(currentBaseDir, clusterName)
		incomingDir := filepath.Join(incomingBaseDir, clusterName)
		return CompareCluster(ctx, clusterName, currentDir, incomingDir, contextLines)
	})
}
