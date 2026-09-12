package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Kustomization represents a Flux Kustomization resource
type Kustomization struct {
	TypeMeta   `json:",inline"`
	ObjectMeta `json:"metadata,omitempty"`
	Spec       KustomizationSpec   `json:"spec,omitempty"`
	Status     KustomizationStatus `json:"status,omitempty"`
}

// KustomizationSpec defines the configuration for a Kustomization
type KustomizationSpec struct {
	// DependsOn may contain a meta.NamespacedObjectReference slice with references to Kustomization resources
	DependsOn []NamespacedObjectReference `json:"dependsOn,omitempty"`

	// The interval at which to reconcile the Kustomization
	Interval metav1.Duration `json:"interval"`

	// The interval at which to retry a previously failed reconciliation
	RetryInterval *metav1.Duration `json:"retryInterval,omitempty"`

	// Path to the directory containing the kustomization.yaml file
	Path string `json:"path,omitempty"`

	// Prune enables garbage collection
	Prune bool `json:"prune,omitempty"`

	// Reference to the source where the kustomization file is
	SourceRef CrossNamespaceObjectReference `json:"sourceRef"`

	// HealthChecks contains a list of resources to be checked for health
	HealthChecks []NamespacedObjectKindReference `json:"healthChecks,omitempty"`

	// A list of resources to be included in the health assessment
	Patches []Patch `json:"patches,omitempty"`

	// Strategic merge and JSON patches, defined as inline YAML objects
	PatchesStrategicMerge []string `json:"patchesStrategicMerge,omitempty"`

	// JSON 6902 patches, defined as inline YAML objects
	PatchesJSON6902 []JSON6902Patch `json:"patchesJson6902,omitempty"`

	// Images is a list of (image name, new name, new tag or digest) for changing image names, tags or digests
	Images []Image `json:"images,omitempty"`

	// The name of the Kubernetes service account to use for applying the Kustomization
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// TargetNamespace sets or overrides the namespaces of all Kustomization objects
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// Timeout for operations
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Force instructs the controller to recreate resources when patching fails
	Force bool `json:"force,omitempty"`

	// Wait instructs the controller to check the health of all the reconciled resources
	Wait bool `json:"wait,omitempty"`

	// PostBuild describes which actions to perform on the YAML manifest
	PostBuild *PostBuild `json:"postBuild,omitempty"`
}

// NamespacedObjectKindReference contains enough information to locate the typed referenced Kubernetes resource object
type NamespacedObjectKindReference struct {
	// API version of the referent, if not specified the Kubernetes preferred version will be used
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind of the referent
	Kind string `json:"kind"`
	// Name of the referent
	Name string `json:"name"`
	// Namespace of the referent, when not specified it acts as LocalObjectReference
	Namespace string `json:"namespace,omitempty"`
}

// Patch contains either a StrategicMerge or a JSON6902 patch
type Patch struct {
	// Patch contains the JSON6902 patch document with an array of operation objects
	Patch string `json:"patch,omitempty"`
	// Target points to the resources that the patch document should be applied to
	Target *Selector `json:"target,omitempty"`
}

// Selector specifies a set of resources
type Selector struct {
	// Group of the referent
	Group string `json:"group,omitempty"`
	// Version of the referent
	Version string `json:"version,omitempty"`
	// Kind of the referent
	Kind string `json:"kind,omitempty"`
	// Name of the referent
	Name string `json:"name,omitempty"`
	// Namespace of the referent
	Namespace string `json:"namespace,omitempty"`
	// AnnotationSelector is a string that follows the label selection expression
	AnnotationSelector string `json:"annotationSelector,omitempty"`
	// LabelSelector is a string that follows the label selection expression
	LabelSelector string `json:"labelSelector,omitempty"`
}

// JSON6902Patch contains a JSON6902 patch and the target the patch should be applied to
type JSON6902Patch struct {
	// Patch contains the JSON6902 patch document with an array of operation objects
	Patch []JSON6902 `json:"patch"`
	// Target points to the resources that the patch document should be applied to
	Target Selector `json:"target"`
}

// JSON6902 is a JSON6902 operation object
type JSON6902 struct {
	// Op indicates the operation to perform
	Op string `json:"op"`
	// Path contains the JSON-Pointer value
	Path string `json:"path"`
	// From contains a JSON-Pointer value that references a location within the target document
	From string `json:"from,omitempty"`
	// Value contains a valid JSON structure
	Value interface{} `json:"value,omitempty"`
}

// Image contains an image name, a new name, a new tag or digest
type Image struct {
	// Name is a tag-less image name
	Name string `json:"name"`
	// NewName is the value used to replace the original name
	NewName string `json:"newName,omitempty"`
	// NewTag is the value used to replace the original tag
	NewTag string `json:"newTag,omitempty"`
	// Digest is the value used to replace the original image tag
	Digest string `json:"digest,omitempty"`
}

// PostBuild describes which actions to perform on the YAML manifest generated from the Kustomization
type PostBuild struct {
	// Substitute holds a map of key/value pairs
	Substitute map[string]string `json:"substitute,omitempty"`
	// SubstituteFrom holds references to ConfigMaps and Secrets containing the variables and their values
	SubstituteFrom []SubstituteReference `json:"substituteFrom,omitempty"`
}

// SubstituteReference contains a reference to a resource containing the variables and their values
type SubstituteReference struct {
	// Kind of the values referent, valid values are ('Secret', 'ConfigMap')
	Kind string `json:"kind"`
	// Name of the values referent
	Name string `json:"name"`
	// Optional indicates whether the referenced resource must exist
	Optional bool `json:"optional,omitempty"`
}

// KustomizationStatus defines the observed state of a Kustomization
type KustomizationStatus struct {
	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions holds the conditions for the Kustomization
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastAppliedRevision is the revision of the last successfully applied source
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`
	// LastAttemptedRevision is the revision of the last reconciliation attempt
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`
}

// GetKey returns namespace/name key for the Kustomization
func (k *Kustomization) GetKey() string {
	return GetObjectKey(k.Namespace, k.Name)
}

// GetDependencyKeys returns all dependency keys
func (k *Kustomization) GetDependencyKeys() []string {
	var keys []string
	for _, dep := range k.Spec.DependsOn {
		ns := dep.Namespace
		if ns == "" {
			ns = k.Namespace
		}
		keys = append(keys, GetObjectKey(ns, dep.Name))
	}
	return keys
}

// SourceRefKey returns the namespace/name key under which this Kustomization's
// sourceRef should be looked up in a registry of GitRepositories. When
// spec.sourceRef.namespace is empty Flux defaults to the Kustomization's own
// namespace; this helper mirrors that convention so callers don't have to
// duplicate the fallback.
func (k *Kustomization) SourceRefKey() string {
	ns := k.Spec.SourceRef.Namespace
	if ns == "" {
		ns = k.Namespace
	}
	return GetObjectKey(ns, k.Spec.SourceRef.Name)
}
