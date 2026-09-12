package diff

// ChangeStatus represents the type of change
type ChangeStatus string

const (
	StatusAdded    ChangeStatus = "ADDED"
	StatusDeleted  ChangeStatus = "DELETED"
	StatusModified ChangeStatus = "MODIFIED"
)

// ResourceInfo represents parsed resource information from filename
type ResourceInfo struct {
	Namespace string
	Kind      string
	Name      string
}

// FileChange represents a change in a single file
type FileChange struct {
	Resource ResourceInfo
	Status   ChangeStatus
	Diff     string // Unified diff output
}

// ClusterDiff represents all changes for a single cluster
type ClusterDiff struct {
	ClusterName string
	Changes     []*FileChange
	Error       error
}
