package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HelmRelease represents a Flux HelmRelease resource
type HelmRelease struct {
	TypeMeta   `json:",inline"`
	ObjectMeta `json:"metadata,omitempty"`
	Spec       HelmReleaseSpec   `json:"spec,omitempty"`
	Status     HelmReleaseStatus `json:"status,omitempty"`
}

// HelmReleaseSpec defines the desired state of a HelmRelease
type HelmReleaseSpec struct {
	// Chart defines the template of the v1.HelmChart that should be created for this HelmRelease
	Chart HelmChartTemplate `json:"chart"`

	// ChartRef holds a reference to a source that already carries the chart,
	// as an alternative to Chart. Mutually exclusive with Chart in Flux.
	ChartRef *ChartRef `json:"chartRef,omitempty"`

	// Interval at which to reconcile the Helm release
	Interval metav1.Duration `json:"interval"`

	// KubeConfig for reconciling the HelmRelease on a remote cluster
	KubeConfig *KubeConfig `json:"kubeConfig,omitempty"`

	// Suspend tells the controller to suspend reconciliation for this HelmRelease
	Suspend bool `json:"suspend,omitempty"`

	// ReleaseName used for the Helm release. Defaults to a composition of '[TargetNamespace-]Name'
	ReleaseName string `json:"releaseName,omitempty"`

	// TargetNamespace to target when performing operations for the HelmRelease
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// StorageNamespace used for the Helm storage
	StorageNamespace string `json:"storageNamespace,omitempty"`

	// DependsOn may contain a meta.NamespacedObjectReference slice with references to HelmRelease resources
	DependsOn []NamespacedObjectReference `json:"dependsOn,omitempty"`

	// Timeout is the time to wait for any individual Kubernetes operation
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// MaxHistory is the number of revisions saved by Helm for this HelmRelease
	MaxHistory *int `json:"maxHistory,omitempty"`

	// ServiceAccountName used for this Helm release
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// PersistentClient tells the controller to use a persistent Kubernetes client for this release
	PersistentClient bool `json:"persistentClient,omitempty"`

	// Install holds the configuration for Helm install actions for this HelmRelease
	Install *Install `json:"install,omitempty"`

	// Upgrade holds the configuration for Helm upgrade actions for this HelmRelease
	Upgrade *Upgrade `json:"upgrade,omitempty"`

	// Test holds the configuration for Helm test actions for this HelmRelease
	Test *Test `json:"test,omitempty"`

	// Rollback holds the configuration for Helm rollback actions for this HelmRelease
	Rollback *Rollback `json:"rollback,omitempty"`

	// Uninstall holds the configuration for Helm uninstall actions for this HelmRelease
	Uninstall *Uninstall `json:"uninstall,omitempty"`

	// ValuesFrom holds references to resources containing Helm values
	ValuesFrom []ValuesReference `json:"valuesFrom,omitempty"`

	// Values holds the values for this Helm release
	Values *runtime.RawExtension `json:"values,omitempty"`

	// PostRenderers holds an array of Helm PostRenderers
	PostRenderers []PostRenderer `json:"postRenderers,omitempty"`
}

// ChartRef references a source object that carries the chart itself, used
// instead of Chart. Kinds are OCIRepository, HelmChart and (via the
// flux-operator source-watcher) ExternalArtifact.
type ChartRef struct {
	// Kind of the referenced source
	Kind string `json:"kind"`

	// Name of the referenced source
	Name string `json:"name"`

	// Namespace of the referenced source, defaults to the HelmRelease namespace
	Namespace string `json:"namespace,omitempty"`
}

// HelmChartTemplate defines the template from which the controller will generate a HelmChart object
type HelmChartTemplate struct {
	// Spec holds the template for the v1.HelmChartSpec for this HelmRelease
	Spec HelmChartTemplateSpec `json:"spec"`
}

// HelmChartTemplateSpec defines the template from which the controller will generate a HelmChartSpec object
type HelmChartTemplateSpec struct {
	// Chart is the name or path the Helm chart is available at in the SourceRef
	Chart string `json:"chart"`

	// Version is the chart version semver expression, ignored for charts from GitRepository and Bucket sources
	Version string `json:"version,omitempty"`

	// SourceRef is the reference to the Source the chart is available at
	SourceRef CrossNamespaceObjectReference `json:"sourceRef"`

	// Interval at which to check the v1.Source for updates
	Interval *metav1.Duration `json:"interval,omitempty"`

	// ReconcileStrategy determines what enables the creation of a new artifact
	ReconcileStrategy string `json:"reconcileStrategy,omitempty"`

	// ValuesFiles is an alternative list of values files to use as the chart values
	ValuesFiles []string `json:"valuesFiles,omitempty"`

	// Verify contains the secret name containing the trusted public keys used to verify the signature
	Verify *OCIRepositoryVerification `json:"verify,omitempty"`
}

// OCIRepositoryVerification verifies the authenticity of an OCI Artifact
type OCIRepositoryVerification struct {
	// Provider specifies the technology used to sign the OCI Artifact
	Provider string `json:"provider"`

	// SecretRef specifies the Kubernetes Secret containing the trusted public keys
	SecretRef *NamespacedObjectReference `json:"secretRef,omitempty"`
}

// KubeConfig references a Kubernetes secret that contains a kubeconfig file
type KubeConfig struct {
	// SecretRef holds the name of a secret that contains a key with the kubeconfig file
	SecretRef NamespacedObjectReference `json:"secretRef"`
}

// Install holds the configuration for Helm install actions
type Install struct {
	// Timeout is the time to wait for any individual Kubernetes operation
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Remediation holds the remediation configuration for when the Helm install action fails
	Remediation *InstallRemediation `json:"remediation,omitempty"`

	// DisableWait disables the waiting for resources to be ready after a Helm install has been performed
	DisableWait bool `json:"disableWait,omitempty"`

	// DisableWaitForJobs disables waiting for jobs to complete after a Helm install has been performed
	DisableWaitForJobs bool `json:"disableWaitForJobs,omitempty"`

	// DisableHooks prevents hooks from running during the Helm install action
	DisableHooks bool `json:"disableHooks,omitempty"`

	// DisableOpenAPIValidation prevents the Helm install action from validating rendered templates against the Kubernetes OpenAPI Schema
	DisableOpenAPIValidation bool `json:"disableOpenAPIValidation,omitempty"`

	// DisableSchemaValidation prevents the Helm install action from validating the values against the JSON schema
	DisableSchemaValidation bool `json:"disableSchemaValidation,omitempty"`

	// Replace tells the Helm install action to re-use the 'ReleaseName', but only if that name is a deleted release
	Replace bool `json:"replace,omitempty"`

	// SkipCRDs tells the Helm install action to not install any CRDs
	SkipCRDs bool `json:"skipCRDs,omitempty"`

	// CreateNamespace tells the Helm install action to create the HelmReleaseSpec.TargetNamespace if it does not exist yet
	CreateNamespace bool `json:"createNamespace,omitempty"`
}

// InstallRemediation holds the configuration for Helm install remediation
type InstallRemediation struct {
	// Retries is the number of retries that should be attempted on failures before bailing
	Retries int `json:"retries,omitempty"`

	// IgnoreTestFailures tells the controller to skip remediation when the Helm tests are run after an install action but fail
	IgnoreTestFailures bool `json:"ignoreTestFailures,omitempty"`

	// RemediateLastFailure tells the controller to remediate the last failure, when no retries remain
	RemediateLastFailure bool `json:"remediateLastFailure,omitempty"`
}

// Upgrade holds the configuration for Helm upgrade actions
type Upgrade struct {
	// Timeout is the time to wait for any individual Kubernetes operation
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Remediation holds the remediation configuration for when the Helm upgrade action fails
	Remediation *UpgradeRemediation `json:"remediation,omitempty"`

	// DisableWait disables the waiting for resources to be ready after a Helm upgrade has been performed
	DisableWait bool `json:"disableWait,omitempty"`

	// DisableWaitForJobs disables waiting for jobs to complete after a Helm upgrade has been performed
	DisableWaitForJobs bool `json:"disableWaitForJobs,omitempty"`

	// DisableHooks prevents hooks from running during the Helm upgrade action
	DisableHooks bool `json:"disableHooks,omitempty"`

	// DisableOpenAPIValidation prevents the Helm upgrade action from validating rendered templates against the Kubernetes OpenAPI Schema
	DisableOpenAPIValidation bool `json:"disableOpenAPIValidation,omitempty"`

	// DisableSchemaValidation prevents the Helm upgrade action from validating the values against the JSON schema
	DisableSchemaValidation bool `json:"disableSchemaValidation,omitempty"`

	// Force forces resource updates through a replacement strategy
	Force bool `json:"force,omitempty"`

	// PreserveValues will make Helm reuse the last release's values and merge in overrides
	PreserveValues bool `json:"preserveValues,omitempty"`

	// CleanupOnFail allows deletion of new resources created during the Helm upgrade action when it fails
	CleanupOnFail bool `json:"cleanupOnFail,omitempty"`
}

// UpgradeRemediation holds the configuration for Helm upgrade remediation
type UpgradeRemediation struct {
	// Retries is the number of retries that should be attempted on failures before bailing
	Retries int `json:"retries,omitempty"`

	// IgnoreTestFailures tells the controller to skip remediation when the Helm tests are run after an upgrade action but fail
	IgnoreTestFailures bool `json:"ignoreTestFailures,omitempty"`

	// RemediateLastFailure tells the controller to remediate the last failure, when no retries remain
	RemediateLastFailure bool `json:"remediateLastFailure,omitempty"`

	// Strategy to use for failure remediation
	Strategy string `json:"strategy,omitempty"`
}

// Test holds the configuration for Helm test actions
type Test struct {
	// Enable enables Helm test actions for this HelmRelease after an Helm install or upgrade action has been performed
	Enable bool `json:"enable,omitempty"`

	// Timeout is the time to wait for any individual Kubernetes operation during the performance of a Helm test action
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// IgnoreFailures tells the controller to skip remediation when the Helm tests are run but fail
	IgnoreFailures bool `json:"ignoreFailures,omitempty"`
}

// Rollback holds the configuration for Helm rollback actions
type Rollback struct {
	// Timeout is the time to wait for any individual Kubernetes operation
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// DisableWait disables the waiting for resources to be ready after a Helm rollback has been performed
	DisableWait bool `json:"disableWait,omitempty"`

	// DisableWaitForJobs disables waiting for jobs to complete after a Helm rollback has been performed
	DisableWaitForJobs bool `json:"disableWaitForJobs,omitempty"`

	// DisableHooks prevents hooks from running during the Helm rollback action
	DisableHooks bool `json:"disableHooks,omitempty"`

	// Recreate performs pod restarts for the resource if applicable
	Recreate bool `json:"recreate,omitempty"`

	// Force forces resource updates through a replacement strategy
	Force bool `json:"force,omitempty"`

	// CleanupOnFail allows deletion of new resources created during the Helm rollback action when it fails
	CleanupOnFail bool `json:"cleanupOnFail,omitempty"`
}

// Uninstall holds the configuration for Helm uninstall actions
type Uninstall struct {
	// Timeout is the time to wait for any individual Kubernetes operation
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// DisableHooks prevents hooks from running during the Helm rollback action
	DisableHooks bool `json:"disableHooks,omitempty"`

	// KeepHistory tells Helm to remove all associated resources and mark the release as deleted, but retain the release history
	KeepHistory bool `json:"keepHistory,omitempty"`

	// DisableWait disables waiting for all the resources to be deleted after a Helm uninstall is performed
	DisableWait bool `json:"disableWait,omitempty"`

	// DeletionPropagation specifies the deletion propagation policy when a Helm uninstall is performed
	DeletionPropagation string `json:"deletionPropagation,omitempty"`
}

// ValuesReference contains a reference to a resource containing Helm values
type ValuesReference struct {
	// Kind of the values referent, valid values are ('Secret', 'ConfigMap')
	Kind string `json:"kind"`

	// Name of the values referent
	Name string `json:"name"`

	// ValuesKey is the data key where the values.yaml or a specific value can be found
	ValuesKey string `json:"valuesKey,omitempty"`

	// TargetPath is the YAML dot notation path the value should be merged at
	TargetPath string `json:"targetPath,omitempty"`

	// Optional marks this ValuesReference as optional
	Optional bool `json:"optional,omitempty"`
}

// PostRenderer contains a Helm PostRenderer specification
type PostRenderer struct {
	// Kustomization to apply as PostRenderer
	Kustomize *Kustomize `json:"kustomize,omitempty"`
}

// Kustomize holds the configuration for a Kustomize PostRenderer
type Kustomize struct {
	// Strategic merge and JSON patches, defined as inline YAML objects
	Patches []Patch `json:"patches,omitempty"`

	// Strategic merge patches, defined as inline YAML objects
	PatchesStrategicMerge []string `json:"patchesStrategicMerge,omitempty"`

	// JSON 6902 patches, defined as inline YAML objects
	PatchesJSON6902 []JSON6902Patch `json:"patchesJson6902,omitempty"`

	// Images is a list of (image name, new name, new tag or digest) for changing image names, tags or digests
	Images []Image `json:"images,omitempty"`
}

// HelmReleaseStatus defines the observed state of a HelmRelease
type HelmReleaseStatus struct {
	// ObservedGeneration is the last observed generation
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions holds the conditions for the HelmRelease
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastAppliedRevision is the revision of the last successfully applied source
	LastAppliedRevision string `json:"lastAppliedRevision,omitempty"`

	// LastAttemptedRevision is the revision of the last reconciliation attempt
	LastAttemptedRevision string `json:"lastAttemptedRevision,omitempty"`

	// LastAttemptedValuesChecksum is the SHA1 checksum of the values of the last reconciliation attempt
	LastAttemptedValuesChecksum string `json:"lastAttemptedValuesChecksum,omitempty"`

	// LastReleaseRevision is the revision of the last successful Helm release
	LastReleaseRevision int `json:"lastReleaseRevision,omitempty"`

	// HelmChart is the namespaced name of the HelmChart resource created by the controller for the HelmRelease
	HelmChart string `json:"helmChart,omitempty"`

	// Failures is the reconciliation failure count against the latest desired state
	Failures int64 `json:"failures,omitempty"`

	// InstallFailures is the install failure count against the latest desired state
	InstallFailures int64 `json:"installFailures,omitempty"`

	// UpgradeFailures is the upgrade failure count against the latest desired state
	UpgradeFailures int64 `json:"upgradeFailures,omitempty"`
}

// GetKey returns namespace/name key for the HelmRelease
// ChartSourceKey returns namespace/name of spec.chart.spec.sourceRef. Flux
// defaults the namespace to the HelmRelease's own when it is not set.
func (hr *HelmRelease) ChartSourceKey() string {
	ref := hr.Spec.Chart.Spec.SourceRef
	ns := ref.Namespace
	if ns == "" {
		ns = hr.Namespace
	}
	return GetObjectKey(ns, ref.Name)
}

func (hr *HelmRelease) GetKey() string {
	return GetObjectKey(hr.Namespace, hr.Name)
}

// GetDependencyKeys returns all dependency keys
func (hr *HelmRelease) GetDependencyKeys() []string {
	var keys []string
	for _, dep := range hr.Spec.DependsOn {
		ns := dep.Namespace
		if ns == "" {
			ns = hr.Namespace
		}
		keys = append(keys, GetObjectKey(ns, dep.Name))
	}
	return keys
}
