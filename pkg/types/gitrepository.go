package types

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// GitRepository represents a Flux GitRepository resource
type GitRepository struct {
	TypeMeta   `json:",inline"`
	ObjectMeta `json:"metadata,omitempty"`
	Spec       GitRepositorySpec   `json:"spec,omitempty"`
	Status     GitRepositoryStatus `json:"status,omitempty"`
}

// GitRepositorySpec defines the desired state of a Git repository sync
type GitRepositorySpec struct {
	// URL specifies the Git repository URL
	URL string `json:"url"`

	// SecretRef specifies the Secret containing authentication credentials for the GitRepository
	SecretRef *NamespacedObjectReference `json:"secretRef,omitempty"`

	// Interval at which to check the GitRepository for updates
	Interval metav1.Duration `json:"interval"`

	// Timeout for Git operations like cloning, defaults to 60s
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Reference specifies the Git reference to resolve and monitor for changes
	Reference *GitRepositoryRef `json:"ref,omitempty"`

	// Verification specifies the configuration to verify the Git commit signature(s)
	Verification *GitRepositoryVerification `json:"verify,omitempty"`

	// Ignore overrides the set of excluded patterns in the .sourceignore format
	Ignore *string `json:"ignore,omitempty"`

	// Suspend tells the controller to suspend the reconciliation of this GitRepository
	Suspend bool `json:"suspend,omitempty"`

	// GitImplementation specifies which Git client library implementation to use
	GitImplementation string `json:"gitImplementation,omitempty"`

	// RecurseSubmodules enables the initialization of all submodules within the GitRepository
	RecurseSubmodules bool `json:"recurseSubmodules,omitempty"`

	// Include specifies a list of GitRepository resources which Artifacts should be included in the Artifact produced for this GitRepository
	Include []GitRepositoryInclude `json:"include,omitempty"`

	// AccessFrom specifies an Access Control List for allowing cross-namespace references to this object
	AccessFrom *AccessFrom `json:"accessFrom,omitempty"`
}

// GitRepositoryRef defines the Git ref used for pull and checkout operations
type GitRepositoryRef struct {
	// Branch to check out, defaults to 'master' if no other field is defined
	Branch string `json:"branch,omitempty"`

	// Tag to check out, takes precedence over Branch
	Tag string `json:"tag,omitempty"`

	// SemVer tag expression to check out, takes precedence over Tag
	SemVer string `json:"semver,omitempty"`

	// Name of the reference to check out; takes precedence over Branch, Tag and SemVer
	Name string `json:"name,omitempty"`

	// Commit SHA to check out, takes precedence over all reference fields
	Commit string `json:"commit,omitempty"`
}

// GitRepositoryVerification specifies the Git commit signature verification strategy
type GitRepositoryVerification struct {
	// Mode specifies what Git object should be verified, currently ('head')
	Mode string `json:"mode"`

	// SecretRef specifies the Secret containing the public keys of trusted Git authors
	SecretRef NamespacedObjectReference `json:"secretRef"`
}

// GitRepositoryInclude specifies a local reference to a GitRepository which Artifact (sub-)set to include
type GitRepositoryInclude struct {
	// GitRepositoryRef specifies the GitRepository which Artifact contents must be included
	GitRepositoryRef NamespacedObjectReference `json:"repository"`

	// FromPath specifies the path to copy contents from, defaults to the root of the Artifact
	FromPath string `json:"fromPath,omitempty"`

	// ToPath specifies the path to copy contents to, defaults to the name of the GitRepositoryRef
	ToPath string `json:"toPath,omitempty"`
}

// GitRepositoryStatus defines the observed state of the GitRepository
type GitRepositoryStatus struct {
	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the GitRepository
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Artifact represents the output of the last successful repository sync
	Artifact *Artifact `json:"artifact,omitempty"`

	// IncludedArtifacts represents the included artifacts from the last successful repository sync
	IncludedArtifacts []*Artifact `json:"includedArtifacts,omitempty"`

	// ContentConfigChecksum is a checksum of all the configurations related to the content of the source artifact
	ContentConfigChecksum string `json:"contentConfigChecksum,omitempty"`

	// ObservedIgnore is the observed exclusion patterns used for constructing the source artifact
	ObservedIgnore *string `json:"observedIgnore,omitempty"`

	// ObservedRecurseSubmodules is the observed resource submodules configuration used to produce the current Artifact
	ObservedRecurseSubmodules bool `json:"observedRecurseSubmodules,omitempty"`

	// ObservedInclude is the observed list of GitRepository resources used to produce the current Artifact
	ObservedInclude []GitRepositoryInclude `json:"observedInclude,omitempty"`
}

// GetKey returns namespace/name key for the GitRepository
func (gr *GitRepository) GetKey() string {
	return GetObjectKey(gr.Namespace, gr.Name)
}
