// Package types holds the Go structs of the Flux resources the tool reads:
// Kustomization, HelmRelease, HelmRepository, GitRepository and
// ArtifactGenerator.
package types

// ArtifactGenerator mirrors the source.extensions.fluxcd.io/v1beta1
// ArtifactGenerator CRD provided by the flux-operator source-watcher. It
// carves one or more ExternalArtifacts out of an existing Flux source: the
// canonical use is extracting a chart directory that is vendored inside the
// GitOps repository itself, so a HelmRelease can consume it via
// spec.chartRef without publishing the chart to a registry.
//
// Only the fields needed to map an ExternalArtifact name back to a directory
// inside the source are modelled.
type ArtifactGenerator struct {
	TypeMeta   `json:",inline"`
	ObjectMeta `json:"metadata,omitempty"`
	Spec       ArtifactGeneratorSpec `json:"spec,omitempty"`
}

// ArtifactGeneratorSpec is the desired state of an ArtifactGenerator
type ArtifactGeneratorSpec struct {
	// Sources are the Flux sources the artifacts are carved out of, each bound
	// to an alias referenced as "@<alias>" in copy operations
	Sources []ArtifactGeneratorSource `json:"sources,omitempty"`

	// Artifacts are the ExternalArtifacts produced by this generator
	Artifacts []ArtifactGeneratorArtifact `json:"artifacts,omitempty"`
}

// ArtifactGeneratorSource binds an alias to a Flux source object
type ArtifactGeneratorSource struct {
	// Alias is the name the source is referenced by in copy operations
	Alias string `json:"alias"`

	// Kind of the referenced source, e.g. OCIRepository
	Kind string `json:"kind"`

	// Name of the referenced source
	Name string `json:"name"`

	// Namespace of the referenced source
	Namespace string `json:"namespace,omitempty"`
}

// ArtifactGeneratorArtifact describes a single produced ExternalArtifact
type ArtifactGeneratorArtifact struct {
	// Name of the produced ExternalArtifact
	Name string `json:"name"`

	// OriginRevision propagates the revision of the named source alias
	OriginRevision string `json:"originRevision,omitempty"`

	// Copy lists the copy operations that assemble the artifact
	Copy []ArtifactGeneratorCopy `json:"copy,omitempty"`
}

// ArtifactGeneratorCopy is a single copy operation, with paths expressed
// relative to a source alias ("@bundle/charts/foo/") or to the artifact being
// assembled ("@artifact/")
type ArtifactGeneratorCopy struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GetKey returns the namespace/name key of the ArtifactGenerator
func (a *ArtifactGenerator) GetKey() string {
	return GetObjectKey(a.Namespace, a.Name)
}
