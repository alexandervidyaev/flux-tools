package kustomize

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FilterOptions contains options for filtering Kubernetes objects
type FilterOptions struct {
	// SkipCRDs skips Custom Resource Definitions
	SkipCRDs bool
	// SkipSecrets skips Secret resources
	SkipSecrets bool
	// SkipFluxSystem skips Flux system objects from flux-system namespace
	SkipFluxSystem bool
	// SkipKinds skips objects whose kind matches any of the listed kinds.
	// Comparison is case-insensitive.
	SkipKinds []string
}

// Filter handles filtering of Kubernetes objects
type Filter struct {
	options FilterOptions
	// skipKinds is the lowercased set built from options.SkipKinds for
	// case-insensitive kind matching.
	skipKinds map[string]bool
}

// NewFilter creates a new Filter with the given options
func NewFilter(options FilterOptions) *Filter {
	skipKinds := make(map[string]bool, len(options.SkipKinds))
	for _, kind := range options.SkipKinds {
		skipKinds[strings.ToLower(kind)] = true
	}
	return &Filter{
		options:   options,
		skipKinds: skipKinds,
	}
}

// FilterObjects filters a list of objects based on the filter options
func (f *Filter) FilterObjects(objects []*unstructured.Unstructured) []*unstructured.Unstructured {
	var filtered []*unstructured.Unstructured

	for _, obj := range objects {
		if f.shouldInclude(obj) {
			filtered = append(filtered, obj)
		}
	}

	return filtered
}

// shouldInclude determines if an object should be included based on filter options
func (f *Filter) shouldInclude(obj *unstructured.Unstructured) bool {
	kind := obj.GetKind()
	namespace := obj.GetNamespace()
	apiVersion := obj.GetAPIVersion()

	// Always skip kustomize config files (not actual Kubernetes resources)
	// These are kustomization.yaml files used by kustomize build, not K8s resources
	if kind == "Kustomization" && strings.HasPrefix(apiVersion, "kustomize.config.k8s.io") {
		return false
	}

	// Check if the kind is explicitly skipped (case-insensitive)
	if f.skipKinds[strings.ToLower(kind)] {
		return false
	}

	// Check if CRDs should be skipped
	if f.options.SkipCRDs && f.isCRD(obj) {
		return false
	}

	// Check if Secrets should be skipped
	if f.options.SkipSecrets && kind == "Secret" {
		return false
	}

	// Check if flux-system namespace should be skipped
	if f.options.SkipFluxSystem {
		// Skip objects in flux-system namespace
		if namespace == "flux-system" {
			return false
		}
		// Skip Flux CRDs (cluster-scoped, so namespace is empty)
		if f.isFluxCRD(obj) {
			return false
		}
	}

	return true
}

// isCRD checks if an object is a CustomResourceDefinition
func (f *Filter) isCRD(obj *unstructured.Unstructured) bool {
	kind := obj.GetKind()
	apiVersion := obj.GetAPIVersion()

	// Check for CRD v1
	if kind == "CustomResourceDefinition" && strings.HasPrefix(apiVersion, "apiextensions.k8s.io") {
		return true
	}

	return false
}

// isFluxCRD checks if an object is a Flux CustomResourceDefinition
func (f *Filter) isFluxCRD(obj *unstructured.Unstructured) bool {
	if !f.isCRD(obj) {
		return false
	}

	name := obj.GetName()

	// Flux CRDs have names ending with specific domains
	fluxDomains := []string{
		".toolkit.fluxcd.io",
		".source.toolkit.fluxcd.io",
		".kustomize.toolkit.fluxcd.io",
		".helm.toolkit.fluxcd.io",
		".notification.toolkit.fluxcd.io",
		".image.toolkit.fluxcd.io",
	}

	for _, domain := range fluxDomains {
		if strings.HasSuffix(name, domain) {
			return true
		}
	}

	return false
}

// GetStats returns statistics about filtered objects
type FilterStats struct {
	Total    int
	Filtered int
	Included int
}

// FilterWithStats filters objects and returns statistics
func (f *Filter) FilterWithStats(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, FilterStats) {
	stats := FilterStats{
		Total: len(objects),
	}

	filtered := f.FilterObjects(objects)
	stats.Included = len(filtered)
	stats.Filtered = stats.Total - stats.Included

	return filtered, stats
}
