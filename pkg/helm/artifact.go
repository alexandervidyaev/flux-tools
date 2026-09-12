package helm

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// externalArtifactSource is the resolution outcome for one ExternalArtifact
// name. Resolution happens at registration time, but a failure is only
// reported when a HelmRelease actually references the artifact — that is where
// the error is actionable and can name the offending release.
type externalArtifactSource struct {
	// path is the directory inside the source, relative to the source root
	path string
	// err explains why the artifact could not be mapped to a directory
	err error
}

// artifactRootRef is the only supported destination for a copy operation: the
// copied directory becomes the artifact root, so the artifact is exactly one
// chart directory.
const artifactRootRef = "@artifact"

// resolveArtifactPath maps a single produced artifact of an ArtifactGenerator
// to a directory path relative to the root of the referenced source.
//
// Only the shape used for vendored charts is supported: exactly one copy
// operation, from a source alias into the artifact root. Anything else
// assembles an artifact out of several pieces, which does not correspond to a
// single chart directory on disk.
func resolveArtifactPath(ag *types.ArtifactGenerator, artifact types.ArtifactGeneratorArtifact) (string, error) {
	if len(artifact.Copy) == 0 {
		return "", fmt.Errorf("artifact %q of ArtifactGenerator %s has no copy operations", artifact.Name, ag.GetKey())
	}
	if len(artifact.Copy) > 1 {
		return "", fmt.Errorf(
			"artifact %q of ArtifactGenerator %s is assembled from %d copy operations; only a single copy into %s/ can be mapped to a chart directory",
			artifact.Name, ag.GetKey(), len(artifact.Copy), artifactRootRef,
		)
	}

	cp := artifact.Copy[0]

	if to := strings.TrimSuffix(cp.To, "/"); to != artifactRootRef {
		return "", fmt.Errorf(
			"artifact %q of ArtifactGenerator %s copies into %q; only %s/ can be mapped to a chart directory",
			artifact.Name, ag.GetKey(), cp.To, artifactRootRef,
		)
	}

	alias, sub, ok := splitAliasPath(cp.From)
	if !ok {
		return "", fmt.Errorf(
			"artifact %q of ArtifactGenerator %s copies from %q, which is not of the form @<alias>/<path>",
			artifact.Name, ag.GetKey(), cp.From,
		)
	}

	if !hasSourceAlias(ag, alias) {
		return "", fmt.Errorf(
			"artifact %q of ArtifactGenerator %s copies from alias %q, which is not declared in spec.sources",
			artifact.Name, ag.GetKey(), alias,
		)
	}

	if sub == "" {
		return "", fmt.Errorf(
			"artifact %q of ArtifactGenerator %s copies the whole source %q; expected a chart subdirectory",
			artifact.Name, ag.GetKey(), alias,
		)
	}

	return sub, nil
}

// splitAliasPath splits "@bundle/charts/foo/" into ("bundle", "charts/foo").
func splitAliasPath(ref string) (alias, path string, ok bool) {
	if !strings.HasPrefix(ref, "@") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(ref, "@")
	alias, rest, found := strings.Cut(trimmed, "/")
	if !found || alias == "" {
		return "", "", false
	}
	rest = filepath.Clean(strings.TrimSuffix(rest, "/"))
	if rest == "." {
		rest = ""
	}
	if strings.HasPrefix(rest, "..") || filepath.IsAbs(rest) {
		return "", "", false
	}
	return alias, rest, true
}

// hasSourceAlias reports whether the alias is declared in spec.sources.
func hasSourceAlias(ag *types.ArtifactGenerator, alias string) bool {
	for _, src := range ag.Spec.Sources {
		if src.Alias == alias {
			return true
		}
	}
	return false
}

// RegisterArtifactGenerator records every ExternalArtifact the generator
// produces, so a HelmRelease referencing one via spec.chartRef can be
// resolved to a chart directory inside the repository.
func (c *Client) RegisterArtifactGenerator(ag *types.ArtifactGenerator) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, artifact := range ag.Spec.Artifacts {
		if artifact.Name == "" {
			continue
		}
		path, err := resolveArtifactPath(ag, artifact)
		key := types.GetObjectKey(ag.Namespace, artifact.Name)
		// First generator to declare a name wins, mirroring how the other
		// registries in this client treat duplicates.
		if _, exists := c.externalArtifacts[key]; exists {
			continue
		}
		c.externalArtifacts[key] = externalArtifactSource{path: path, err: err}
	}
}

// GetExternalArtifactPath returns the chart directory of a registered
// ExternalArtifact, relative to the source root.
func (c *Client) GetExternalArtifactPath(key string) (string, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	src, exists := c.externalArtifacts[key]
	if !exists {
		return "", nil, false
	}
	return src.path, src.err, true
}
