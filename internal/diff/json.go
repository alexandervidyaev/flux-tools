package diff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// clusterDiffJSON is the serializable form of ClusterDiff: the error is
// carried as a plain string because the error interface does not survive
// a JSON round-trip.
type clusterDiffJSON struct {
	ClusterName string        `json:"clusterName"`
	Changes     []*FileChange `json:"changes"`
	Error       string        `json:"error,omitempty"`
}

// MarshalClusterDiff serializes a ClusterDiff to JSON. The artifact is the
// structured counterpart of the per-cluster markdown file: consumers (the
// GitLab poster, external tooling) read it instead of reverse-parsing the
// markdown.
func MarshalClusterDiff(d *ClusterDiff) ([]byte, error) {
	out := clusterDiffJSON{
		ClusterName: d.ClusterName,
		Changes:     d.Changes,
	}
	if d.Error != nil {
		out.Error = d.Error.Error()
	}
	return json.MarshalIndent(out, "", "  ")
}

// LoadClusterDiffFile reads a <cluster>-diff.json file produced by
// SaveDiffsToFiles back into a ClusterDiff.
func LoadClusterDiffFile(path string) (*ClusterDiff, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var in clusterDiffJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	d := &ClusterDiff{
		ClusterName: in.ClusterName,
		Changes:     in.Changes,
	}
	if in.Changes == nil {
		d.Changes = []*FileChange{}
	}
	if in.Error != "" {
		d.Error = errors.New(in.Error)
	}
	return d, nil
}
