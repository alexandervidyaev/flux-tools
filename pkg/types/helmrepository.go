package types

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HelmRepository represents a Flux HelmRepository resource
type HelmRepository struct {
	TypeMeta   `json:",inline"`
	ObjectMeta `json:"metadata,omitempty"`
	Spec       HelmRepositorySpec   `json:"spec,omitempty"`
	Status     HelmRepositoryStatus `json:"status,omitempty"`
}

// HelmRepositorySpec defines the reference to a Helm repository
type HelmRepositorySpec struct {
	// URL of the Helm repository, a valid URL contains at least a protocol and host
	URL string `json:"url"`

	// SecretRef specifies the Secret containing authentication credentials for the HelmRepository
	SecretRef *NamespacedObjectReference `json:"secretRef,omitempty"`

	// PassCredentials allows the credentials from the SecretRef to be passed on to a host that does not match the host as defined in URL
	PassCredentials bool `json:"passCredentials,omitempty"`

	// Interval at which to check the URL for updates
	Interval metav1.Duration `json:"interval"`

	// Timeout of the index fetch operation, defaults to 60s
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Provider used for authentication, can be 'generic', 'aws', 'azure', 'gcp' or 'oci'
	Provider string `json:"provider,omitempty"`

	// Type of the HelmRepository, valid values are 'default' and 'oci'
	Type string `json:"type,omitempty"`

	// Suspend tells the controller to suspend the reconciliation of this HelmRepository
	Suspend bool `json:"suspend,omitempty"`

	// AccessFrom specifies an Access Control List for allowing cross-namespace references to this object
	AccessFrom *AccessFrom `json:"accessFrom,omitempty"`
}

// AccessFrom specifies an Access Control List for allowing cross-namespace references to this object
type AccessFrom struct {
	// NamespaceSelectors is the list of namespace selectors to which this ACL applies
	NamespaceSelectors []NamespaceSelector `json:"namespaceSelectors"`
}

// NamespaceSelector is a selector that contains values, a namespace selector operator and optionally a list of namespace names
type NamespaceSelector struct {
	// MatchLabels is a map of {key,value} pairs
	MatchLabels map[string]string `json:"matchLabels,omitempty"`
}

// HelmRepositoryStatus defines the observed state of the HelmRepository
type HelmRepositoryStatus struct {
	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the HelmRepository
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// URL is the download link for the last index fetched
	URL string `json:"url,omitempty"`

	// Artifact represents the output of the last successful repository sync
	Artifact *Artifact `json:"artifact,omitempty"`
}

// Artifact represents the output of a Source reconciliation
type Artifact struct {
	// Path is the relative file path of this artifact
	Path string `json:"path"`

	// URL is the HTTP address of this artifact
	URL string `json:"url"`

	// Revision is a human readable identifier traceable in the origin source system
	Revision string `json:"revision"`

	// Checksum is the SHA256 checksum of the artifact
	Checksum string `json:"checksum,omitempty"`

	// LastUpdateTime is the timestamp corresponding to the last update of this artifact
	LastUpdateTime metav1.Time `json:"lastUpdateTime"`

	// Size is the size of the artifact in bytes
	Size *int64 `json:"size,omitempty"`

	// Metadata holds upstream information such as OCI annotations
	Metadata map[string]string `json:"metadata,omitempty"`
}

// GetKey returns namespace/name key for the HelmRepository
func (hr *HelmRepository) GetKey() string {
	return GetObjectKey(hr.Namespace, hr.Name)
}

// IsOCI returns true if the HelmRepository is an OCI repository
func (hr *HelmRepository) IsOCI() bool {
	return hr.Spec.Type == "oci"
}
