package types

// NamespacedObjectReference contains enough information to locate the referenced Kubernetes resource object
type NamespacedObjectReference struct {
	// Name of the referent
	Name string `json:"name"`
	// Namespace of the referent, when not specified it acts as LocalObjectReference
	Namespace string `json:"namespace,omitempty"`
}

// CrossNamespaceObjectReference contains enough information to locate the referenced object
type CrossNamespaceObjectReference struct {
	// API version of the referent, if not specified the Kubernetes preferred version will be used
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind of the referent
	Kind string `json:"kind"`
	// Name of the referent
	Name string `json:"name"`
	// Namespace of the referent, defaults to the namespace of the Kubernetes resource object
	Namespace string `json:"namespace,omitempty"`
}

// GetKey returns a unique identifier for the object
func GetObjectKey(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// TypeMeta describes an individual object in an API response or request
type TypeMeta struct {
	// APIVersion defines the versioned schema of this representation of an object
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind is a string value representing the REST resource this object represents
	Kind string `json:"kind,omitempty"`
}

// ObjectMeta is metadata that all persisted resources must have
type ObjectMeta struct {
	Name        string            `json:"name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
