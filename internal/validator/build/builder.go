// Package build implements Flux manifest building with kustomize and Helm support.
package build

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alexandervidyaev/flux-tools/pkg/config"
	"github.com/alexandervidyaev/flux-tools/pkg/fsutil"
	"github.com/alexandervidyaev/flux-tools/pkg/helm"
	"github.com/alexandervidyaev/flux-tools/pkg/kustomize"
	"github.com/alexandervidyaev/flux-tools/pkg/manifest"
	"github.com/alexandervidyaev/flux-tools/pkg/output"
	"github.com/alexandervidyaev/flux-tools/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// Stage names of the build pipeline, recorded via output.StageTimer.
const (
	StageDiscovery = "discovery"
	StageKustomize = "kustomize"
	StageHelm      = "helm"
	StageFilter    = "substitute/filter"
	// StageSerialize is recorded by callers around SerializeObjects.
	StageSerialize = "serialize"
)

// StageOrder is the canonical display order for build stage timings.
var StageOrder = []string{StageDiscovery, StageKustomize, StageHelm, StageFilter, StageSerialize}

// BuildOptions contains options for building manifests
type BuildOptions struct {
	// Path to the root directory to scan
	Path string
	// RootPath is the repository root that Kustomization spec.path and local
	// chart paths are resolved against. Empty means: derive it from the
	// flux-system Kustomization of Path (see DeriveRootPath).
	RootPath string
	// EnableHelm enables processing of HelmRelease resources
	EnableHelm bool
	// Strict mode - fail on missing dependencies or validation errors
	Strict bool
	// SkipCRDs skips Custom Resource Definitions
	SkipCRDs bool
	// SkipSecrets skips Secret resources
	SkipSecrets bool
	// SkipFluxSystem skips Flux system objects from flux-system namespace
	SkipFluxSystem bool
	// SkipKinds skips objects whose kind matches any of the listed kinds
	// (case-insensitive). Applied after building and Helm templating,
	// before serialization.
	SkipKinds []string
	// OutputFormat specifies the output format (yaml, json, sliced)
	OutputFormat string
	// SliceTemplate is the filename template for the sliced output format
	// (kubectl-slice compatible). Only used when OutputFormat is "sliced".
	SliceTemplate string
	// Verbose enables verbose output
	Verbose bool
	// CacheDir is the directory for caching Helm charts and data
	CacheDir string
	// HelmTimeout is the timeout in seconds for Helm operations
	HelmTimeout int
	// SkipFailedCharts skips charts that fail to template (by default, template errors are fatal)
	SkipFailedCharts bool
	// NoTemplateCache disables the persistent helm template cache
	// (--no-template-cache); by default renders from locally cached chart
	// archives are cached on disk between runs (I-3).
	NoTemplateCache bool
	// SkipOCI skips Kustomizations with OCIRepository sourceRef instead of failing
	SkipOCI bool
	// TolerateMissingValues: when true, missing valuesFrom references (Secret/ConfigMap)
	// do not fail the build — a placeholder is injected at targetPath. Used in CI tests
	// where cluster secrets are not available.
	TolerateMissingValues bool
	// GlobalSubstitute holds post-build substitution variables supplied externally
	// (e.g. via --substitute). Applied to all built objects regardless of whether a
	// Kustomization with postBuild is present — for source repos whose parent
	// Kustomization (with substituteFrom) lives in another repository.
	GlobalSubstitute map[string]string
	// Timers accumulates per-stage durations of the build pipeline. Optional;
	// pass a shared instance to aggregate timings across parallel builds.
	// When nil, NewBuilder creates a private one.
	Timers *output.StageTimer
}

// Builder handles the build process
type Builder struct {
	options          BuildOptions
	printer          *output.Printer
	kustomizeBuilder *kustomize.Builder
	helmClient       *helm.Client
	rootPath         string // Root path of the repository
	selfSource       string // namespace/name of the GitRepository that is this checkout, empty when unknown
	timers           *output.StageTimer

	// renderCache caches raw `kustomize build` output keyed by absolute build
	// path, so each path is rendered by an external kustomize exec at most
	// once per run: nested Kustomization discovery and object generation
	// would otherwise render every path twice. No invalidation is needed —
	// source files do not change within a single run. Each cluster is built
	// by its own Builder (see internal/cli/build.go), so the cache is not
	// normally shared across goroutines; the mutex makes it safe anyway in
	// case a Builder is ever used concurrently.
	renderCacheMu sync.Mutex
	renderCache   map[string][]byte
	renderHits    int // cache hit counter, reported in verbose output
}

// NewBuilder creates a new Builder instance
func NewBuilder(ctx context.Context, options BuildOptions) (*Builder, error) {
	// Apply defaults for empty CacheDir / HelmTimeout. The Helm client is
	// constructed unconditionally below and its NewClient mkdirs CacheDir, so
	// an empty value would explode with `mkdir : no such file or directory`.
	// Internal callers like helm.DiscoverChartsInCluster construct a builder
	// without populating these fields; defaulting here keeps that working.
	cfg := config.LoadDefaults()
	if options.CacheDir == "" {
		options.CacheDir = cfg.CacheDir
	}
	if options.HelmTimeout == 0 {
		options.HelmTimeout = cfg.HelmTimeout
	}
	if options.Timers == nil {
		options.Timers = output.NewStageTimer()
	}

	// The Helm client is constructed unconditionally because its git helpers
	// (GetGitRepository / ResolveGitRepositoryPath) are needed to resolve
	// Kustomization CRs whose source is an external GitRepository, regardless
	// of whether Helm templating itself is enabled. The `helm` binary itself
	// is only required when EnableHelm is true.
	helmClient, err := helm.NewClient(options.CacheDir, options.HelmTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Helm client: %w", err)
	}
	if options.EnableHelm {
		if err := helmClient.CheckHelmInstalled(ctx); err != nil {
			return nil, err
		}
	}
	// Persistent helm template cache (I-3): enabled where rendering actually
	// happens (here and in the HelmRelease test case), off with --no-template-cache.
	helmClient.SetTemplateCacheEnabled(!options.NoTemplateCache)

	// The repository root is never guessed: spec.path is resolved against it,
	// and a wrong guess renders the wrong manifests. The CLI always supplies
	// one (--flux-workdir, "." by default).
	if options.RootPath == "" {
		return nil, fmt.Errorf("BuildOptions.RootPath is empty: spec.path is resolved against the repository root, which has to be given")
	}
	rootPath, err := filepath.Abs(options.RootPath)
	if err != nil {
		return nil, fmt.Errorf("invalid root path %q: %w", options.RootPath, err)
	}

	// Propagate rootPath to the helm client so it can resolve local charts
	// (HelmRelease.spec.chart.spec.sourceRef.kind == "GitRepository" where chart is a local path)
	helmClient.SetRootPath(rootPath)

	// Which GitRepository is this checkout: the flux-system sync source when
	// there is a flux-system/, otherwise (flux-operator) whichever
	// GitRepository no manifest declares.
	selfSource, err := DetectSelfSource(options.Path)
	if err != nil {
		return nil, err
	}
	helmClient.SetSelfSource(selfSource)

	ignore, err := kustomize.LoadSourceIgnore(rootPath)
	if err != nil {
		return nil, err
	}
	kustomizeBuilder := kustomize.NewBuilder()
	kustomizeBuilder.SetSourceIgnore(ignore)

	return &Builder{
		options:          options,
		printer:          output.New(options.Verbose),
		kustomizeBuilder: kustomizeBuilder,
		helmClient:       helmClient,
		rootPath:         rootPath,
		selfSource:       selfSource,
		timers:           options.Timers,
		renderCache:      make(map[string][]byte),
	}, nil
}

// buildAndParseCached renders the kustomization at path via `kustomize build`
// and parses the output, serving repeated renders of the same path from an
// in-memory cache (see the renderCache field for the rationale). Raw bytes
// (not parsed objects) are cached because post-build substitutions mutate
// objects in place; each call re-parses the bytes, which is still far cheaper
// than an external exec. Failed builds are not cached.
func (b *Builder) buildAndParseCached(ctx context.Context, path string) ([]*unstructured.Unstructured, error) {
	// Normalize the key: kustomize.Builder.Build resolves the path to
	// absolute anyway, so relative and absolute spellings of the same
	// directory share one cache entry.
	key, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	b.renderCacheMu.Lock()
	data, ok := b.renderCache[key]
	if ok {
		b.renderHits++
	}
	b.renderCacheMu.Unlock()

	if !ok {
		data, err = b.kustomizeBuilder.BuildDir(ctx, key)
		if err != nil {
			return nil, err
		}
		b.renderCacheMu.Lock()
		b.renderCache[key] = data
		b.renderCacheMu.Unlock()
	}

	return kustomize.ParseKustomizeOutput(data)
}

// renderCacheStats returns the number of cached renders and cache hits.
func (b *Builder) renderCacheStats() (entries, hits int) {
	b.renderCacheMu.Lock()
	defer b.renderCacheMu.Unlock()
	return len(b.renderCache), b.renderHits
}

// ResolveKustomizationBuildPath returns the local filesystem path that should
// be fed to `kustomize build` for the given Kustomization. Local sources are
// resolved against the builder's rootPath; external GitRepository sources have
// their referenced repository cloned (or fetched from the clone cache) and
// spec.path is resolved inside that clone.
func (b *Builder) ResolveKustomizationBuildPath(ctx context.Context, ks *types.Kustomization) (string, error) {
	rootPath, err := b.resolveSourceRoot(ctx, ks)
	if err != nil {
		return "", err
	}
	if ks.Spec.Path == "" {
		return rootPath, nil
	}
	return fsutil.ResolvePath(rootPath, ks.Spec.Path), nil
}

// resolveSourceRoot returns the filesystem root for the Kustomization's source:
// rootPath when the source is this checkout, a clone of the declared
// GitRepository otherwise. See helm.Client.ResolveGitSourcePath for the rule.
func (b *Builder) resolveSourceRoot(ctx context.Context, ks *types.Kustomization) (string, error) {
	ref := ks.Spec.SourceRef
	if ref.Name == "" {
		return b.rootPath, nil
	}
	if ref.Kind != "GitRepository" {
		return "", fmt.Errorf("unsupported sourceRef kind %q for Kustomization %s/%s", ref.Kind, ks.Namespace, ks.Name)
	}
	path, err := b.helmClient.ResolveGitSourcePath(ctx, ks.SourceRefKey())
	if err != nil {
		return "", fmt.Errorf("kustomization %s/%s: %w", ks.Namespace, ks.Name, err)
	}
	return path, nil
}

// BuildAll builds all manifests from the specified path
func (b *Builder) BuildAll(ctx context.Context) ([]*unstructured.Unstructured, error) {
	b.printer.Verbose("Building Flux manifests from: %s\n", b.options.Path)

	// Step 1: First, run kustomize build on the input path to get all Flux objects
	// This is the entry point kustomization that includes all Flux resources
	b.printer.Verbose("Running kustomize build to get Flux objects...\n")

	// Discovery stage covers the entry kustomization render plus the
	// recursive nested Kustomization discovery below.
	stopDiscovery := b.timers.Start(StageDiscovery)

	fluxObjects, err := b.buildAndParseCached(ctx, b.options.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to build entry kustomization: %w", err)
	}

	b.printer.Verbose("  Got %d objects from entry kustomization\n", len(fluxObjects))

	// Apply global post-build substitutions early — before parsing into the
	// collection — so HelmRelease names and values are substituted before helm
	// templating (helm rejects ${...} in the release name). Unknown ${vars} are
	// left untouched. A second pass before filtering covers objects produced by
	// nested Kustomizations.
	if len(b.options.GlobalSubstitute) > 0 {
		sub := kustomize.NewSubstitution(b.options.GlobalSubstitute)
		if err := sub.ApplySubstitutions(fluxObjects); err != nil {
			return nil, fmt.Errorf("failed to apply global substitutions: %w", err)
		}
	}

	// Step 2: Parse initial Flux objects into collection
	collection := manifest.NewManifestCollection()
	b.parseFluxObjects(fluxObjects, collection)

	b.printer.Verbose("Initial scan: Found %d Kustomizations, %d HelmReleases, %d HelmRepositories\n",
		len(collection.Kustomizations), len(collection.HelmReleases), len(collection.HelmRepositories))

	// Step 2.5: Recursively discover nested Kustomizations
	// This mirrors flux-local's kustomization_traversal() behavior
	if err := b.discoverNestedKustomizations(ctx, collection); err != nil {
		return nil, fmt.Errorf("failed to discover nested Kustomizations: %w", err)
	}
	stopDiscovery()

	b.printer.Verbose("After recursive discovery: %d Kustomizations total\n", len(collection.Kustomizations))

	// Step 2: Validate dependencies
	if err := manifest.ValidateDependencies(collection); err != nil {
		if b.options.Strict {
			// In strict mode, fail on dependency validation errors
			return nil, fmt.Errorf("dependency validation failed: %w", err)
		}
		// In non-strict mode, just warn
		b.printer.Verbose("Warning: %v\n", err)
		// Continue processing available Kustomizations
	}

	// Step 3: If there are no Kustomizations, use all objects from entry kustomization directly
	var allObjects []*unstructured.Unstructured

	// Kustomize stage covers rendering of every discovered Kustomization
	// (or the direct object pass-through when none were found).
	stopKustomize := b.timers.Start(StageKustomize)

	if len(collection.Kustomizations) == 0 {
		b.printer.Verbose("No Flux Kustomizations found, using objects from entry kustomization directly\n")
		allObjects = nonFluxObjects(fluxObjects)
	} else {
		objects, err := b.buildOrderedKustomizations(ctx, collection, fluxObjects)
		if err != nil {
			return nil, err
		}
		allObjects = objects
	}
	stopKustomize()

	// All kustomize renders are done at this point — report cache efficiency.
	if entries, hits := b.renderCacheStats(); entries > 0 {
		b.printer.Verbose("kustomize render cache: %d entries, %d hits\n", entries, hits)
	}

	// Two Kustomizations may render the same object (a sync Kustomization at
	// the repository root and a nested one under it, for instance). In the
	// cluster that is one object, owned by whichever applied last; here the
	// last rendering wins as well, so HelmReleases are templated once and
	// sliced output does not collide.
	var duplicates int
	allObjects, duplicates = dedupeObjects(allObjects)
	if duplicates > 0 {
		b.printer.Verbose("Dropped %d object(s) rendered by more than one Kustomization\n", duplicates)
	}

	// Step 5: Process HelmReleases if enabled
	if b.options.EnableHelm {
		b.printer.Verbose("Processing HelmReleases...\n")

		// Use HelmRelease objects from built manifests (they have full chart spec)
		// and HelmRepository from collection (found during scanning)
		stopHelm := b.timers.Start(StageHelm)
		helmObjects, err := b.processHelmReleases(ctx, collection, allObjects)
		stopHelm()
		if err != nil {
			return nil, fmt.Errorf("failed to process HelmReleases: %w", err)
		}

		allObjects = append(allObjects, helmObjects...)

		b.printer.Verbose("  Generated %d Helm objects\n", len(helmObjects))

		// Persistent template cache efficiency (I-3).
		if hits, misses := b.helmClient.TemplateCacheStats(); hits+misses > 0 {
			b.printer.Verbose("template cache: %d hits, %d misses\n", hits, misses)
		}
	}

	// Substitute/filter stage covers the final global substitution pass and
	// the object filtering below.
	stopFilter := b.timers.Start(StageFilter)

	// Apply global post-build substitutions (e.g. from --substitute). Unlike
	// per-Kustomization substituteFrom, these are supplied externally (CI) and
	// apply even when no Kustomization CR is present — e.g. source repos whose
	// parent Kustomization (with substituteFrom) lives in another repository.
	if len(b.options.GlobalSubstitute) > 0 {
		sub := kustomize.NewSubstitution(b.options.GlobalSubstitute)
		if err := sub.ApplySubstitutions(allObjects); err != nil {
			return nil, fmt.Errorf("failed to apply global substitutions: %w", err)
		}
	}

	// Step 6: Apply filters
	filter := kustomize.NewFilter(kustomize.FilterOptions{
		SkipCRDs:       b.options.SkipCRDs,
		SkipSecrets:    b.options.SkipSecrets,
		SkipFluxSystem: b.options.SkipFluxSystem,
		SkipKinds:      b.options.SkipKinds,
	})

	filteredObjects, stats := filter.FilterWithStats(allObjects)
	stopFilter()

	b.printer.Verbose("Filtering: Total=%d, Included=%d, Filtered=%d\n",
		stats.Total, stats.Included, stats.Filtered)

	return filteredObjects, nil
}

// buildOrderedKustomizations renders every discovered Kustomization in
// dependency order, threading the ConfigMaps and postBuild variables each one
// produces through to the Kustomizations that depend on it.
func (b *Builder) buildOrderedKustomizations(ctx context.Context, collection *manifest.ManifestCollection, fluxObjects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	orderedKustomizations, err := manifest.GetProcessingOrder(collection)
	if err != nil {
		return nil, fmt.Errorf("failed to determine processing order: %w", err)
	}

	b.printer.Verbose("Processing order determined for %d Kustomizations\n", len(orderedKustomizations))

	substitutionVars := make(map[string]map[string]string) // kustomization key -> variables

	// Collect ConfigMaps from bootstrap path — these are available to all Kustomizations
	// via substituteFrom without any dependsOn requirement.
	configMaps := extractConfigMaps(fluxObjects)

	var allObjects []*unstructured.Unstructured
	for _, ks := range orderedKustomizations {
		b.printer.Verbose("Processing Kustomization: %s/%s\n", ks.Namespace, ks.Name)

		vars := b.buildSubstitutionVars(ks, substitutionVars, configMaps)

		objects, err := b.processKustomization(ctx, ks, vars)
		if err != nil {
			return nil, fmt.Errorf("failed to process Kustomization %s/%s: %w", ks.Namespace, ks.Name, err)
		}

		// Collect ConfigMaps produced by this Kustomization so they are
		// available to Kustomizations processed later (dependency order is
		// guaranteed, so later ones can safely reference these).
		for k, v := range extractConfigMaps(objects) {
			configMaps[k] = v
		}

		// Store variables for dependent Kustomizations
		if ks.Spec.PostBuild != nil && len(ks.Spec.PostBuild.Substitute) > 0 {
			substitutionVars[ks.GetKey()] = ks.Spec.PostBuild.Substitute
		}

		allObjects = append(allObjects, objects...)

		b.printer.Verbose("  Generated %d objects\n", len(objects))
	}

	return allObjects, nil
}

// processKustomization processes a single Kustomization
func (b *Builder) processKustomization(ctx context.Context, ks *types.Kustomization, vars map[string]string) ([]*unstructured.Unstructured, error) {
	if b.options.SkipOCI && ks.Spec.SourceRef.Kind == "OCIRepository" {
		b.printer.Verbose("  Skipping Kustomization %s/%s: OCIRepository sourceRef not supported (--skip-oci)\n", ks.Namespace, ks.Name)
		return []*unstructured.Unstructured{}, nil
	}

	// Resolve build path. Local sources resolve against rootPath, an empty
	// spec.path meaning the root itself, as in Flux; external GitRepository
	// sources are cloned by the helm client and spec.path is resolved inside
	// the clone.
	buildPath, err := b.ResolveKustomizationBuildPath(ctx, ks)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve build path for Kustomization %s/%s: %w", ks.Namespace, ks.Name, err)
	}

	b.printer.Verbose("  Building kustomize from: %s\n", buildPath)

	if err := b.kustomizeBuilder.CheckKustomizeInstalled(ctx); err != nil {
		return nil, err
	}

	// The render is usually served from the cache populated during nested
	// Kustomization discovery. A directory without kustomization.yaml is
	// built from a generated one, as kustomize-controller does.
	objects, err := b.buildAndParseCached(ctx, buildPath)
	if err != nil {
		return nil, fmt.Errorf("kustomize build failed for path %s: %w", buildPath, err)
	}

	// Apply post-build substitutions if configured
	if len(vars) > 0 {
		sub := kustomize.NewSubstitution(vars)
		if err := sub.ApplySubstitutions(objects); err != nil {
			return nil, fmt.Errorf("failed to apply substitutions: %w", err)
		}
	}

	// Apply target namespace if specified
	if ks.Spec.TargetNamespace != "" {
		for _, obj := range objects {
			// Only set namespace for namespaced resources
			if obj.GetNamespace() != "" || isNamespacedResource(obj) {
				obj.SetNamespace(ks.Spec.TargetNamespace)
			}
		}
	}

	return objects, nil
}

// buildSubstitutionVars builds the variables map for a Kustomization
// including variables from dependent Kustomizations and substituteFrom references.
func (b *Builder) buildSubstitutionVars(ks *types.Kustomization, allVars map[string]map[string]string, configMaps map[string]map[string]string) map[string]string {
	result := make(map[string]string)

	// First, add variables from dependencies
	for _, depKey := range ks.GetDependencyKeys() {
		if depVars, exists := allVars[depKey]; exists {
			for k, v := range depVars {
				result[k] = v
			}
		}
	}

	if ks.Spec.PostBuild == nil {
		return result
	}

	// substituteFrom: load variables from ConfigMaps found in the built manifests.
	// Secrets are not supported — flux-tools has no cluster access.
	// Order matches Flux runtime: substituteFrom entries are merged in order,
	// then inline substitute overrides everything.
	for _, ref := range ks.Spec.PostBuild.SubstituteFrom {
		if ref.Kind != "ConfigMap" {
			b.printer.Verbose("  Warning: substituteFrom Secret %q skipped (no cluster access)\n", ref.Name)
			continue
		}
		ns := ks.Namespace
		key := types.GetObjectKey(ns, ref.Name)
		data, found := configMaps[key]
		if !found {
			if !ref.Optional {
				b.printer.Verbose("  Warning: ConfigMap %q not found for substituteFrom (Kustomization %s/%s)\n", key, ks.Namespace, ks.Name)
			}
			continue
		}
		for k, v := range data {
			result[k] = v
		}
	}

	// Inline substitute overrides substituteFrom (same as Flux runtime behaviour)
	for k, v := range ks.Spec.PostBuild.Substitute {
		result[k] = v
	}

	return result
}

// extractConfigMaps extracts ConfigMap data from a list of objects.
// Returns a map of "namespace/name" -> data fields.
func extractConfigMaps(objects []*unstructured.Unstructured) map[string]map[string]string {
	result := make(map[string]map[string]string)
	for _, obj := range objects {
		if obj.GetKind() != "ConfigMap" {
			continue
		}
		dataRaw, ok := obj.Object["data"]
		if !ok {
			continue
		}
		dataMap, ok := dataRaw.(map[string]interface{})
		if !ok {
			continue
		}
		cmData := make(map[string]string, len(dataMap))
		for k, v := range dataMap {
			if str, ok := v.(string); ok {
				cmData[k] = str
			}
		}
		key := types.GetObjectKey(obj.GetNamespace(), obj.GetName())
		result[key] = cmData
	}
	return result
}

// processHelmReleases processes all HelmRelease objects from built manifests
func (b *Builder) processHelmReleases(ctx context.Context, collection *manifest.ManifestCollection, objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	secrets := b.extractHelmResourcesFromManifests(collection, objects)
	b.setupOCICredentials(ctx, collection, secrets)
	b.registerRepositories(collection)
	b.registerGitRepositories(collection)
	b.registerArtifactGenerators(collection)
	helmReleaseObjects := b.resolveHelmReleaseObjects(collection, objects)
	return b.templateHelmReleases(ctx, helmReleaseObjects)
}

// extractHelmResourcesFromManifests extracts HelmRepositories and Secrets from built manifests
// extractHelmResourcesFromManifests folds the source resources a rendered
// manifest set carries into the collection discovery already built, and returns
// the Secrets those sources authenticate with, keyed by namespace/name.
func (b *Builder) extractHelmResourcesFromManifests(collection *manifest.ManifestCollection, objects []*unstructured.Unstructured) map[string]map[string]string {
	secrets := make(map[string]map[string]string)

	for _, obj := range objects {
		switch obj.GetKind() {
		case "HelmRepository":
			if repo, ok := decodeFluxObject[types.HelmRepository](b, obj, "source.toolkit.fluxcd.io"); ok {
				if key := repo.GetKey(); putIfAbsent(collection.HelmRepositories, key, repo) {
					b.printer.Verbose("  Found HelmRepository in manifests: %s\n", key)
				}
			}
		case "GitRepository":
			if repo, ok := decodeFluxObject[types.GitRepository](b, obj, "source.toolkit.fluxcd.io"); ok {
				if key := repo.GetKey(); putIfAbsent(collection.GitRepositories, key, repo) {
					b.printer.Verbose("  Found GitRepository in manifests: %s\n", key)
				}
			}
		case "ArtifactGenerator":
			if ag, ok := decodeFluxObject[types.ArtifactGenerator](b, obj, "source.extensions.fluxcd.io"); ok {
				if key := ag.GetKey(); putIfAbsent(collection.ArtifactGenerators, key, ag) {
					b.printer.Verbose("  Found ArtifactGenerator in manifests: %s\n", key)
				}
			}
		case "Secret":
			if b.options.SkipSecrets {
				continue
			}
			if data, ok := secretData(obj); ok {
				key := types.GetObjectKey(obj.GetNamespace(), obj.GetName())
				secrets[key] = data
				b.printer.Verbose("  Found Secret in manifests: %s\n", key)
			}
		}
	}

	return secrets
}

// setupOCICredentials logs in to OCI registries using available secrets
func (b *Builder) setupOCICredentials(ctx context.Context, collection *manifest.ManifestCollection, secrets map[string]map[string]string) {
	if len(secrets) > 0 {
		b.printer.Verbose("  Setting up credentials for OCI repositories...\n")
	}

	for _, repo := range collection.HelmRepositories {
		if repo.IsOCI() && repo.Spec.SecretRef != nil {
			secretKey := types.GetObjectKey(repo.Namespace, repo.Spec.SecretRef.Name)
			if secretData, exists := secrets[secretKey]; exists {
				if err := b.helmClient.LoginOCI(ctx, repo, secretData); err != nil {
					b.printer.Verbose("  Warning: Failed to login to OCI registry %s: %v\n", repo.Spec.URL, err)
				} else {
					b.printer.Verbose("  Logged in to OCI registry: %s\n", repo.Spec.URL)
				}
			}
		}
	}
}

// registerRepositories registers all HelmRepositories for lazy loading
func (b *Builder) registerRepositories(collection *manifest.ManifestCollection) {
	for _, repo := range collection.HelmRepositories {
		b.helmClient.RegisterRepository(repo)
	}

	b.printer.Verbose("  Registered %d Helm repositories (will add on-demand)\n", len(collection.HelmRepositories))
}

// registerGitRepositories registers all GitRepositories so that HelmReleases
// referencing them via sourceRef.kind: GitRepository can be resolved to a
// local filesystem path (either current repo root or a clone of an external
// repository).
func (b *Builder) registerGitRepositories(collection *manifest.ManifestCollection) {
	for _, repo := range collection.GitRepositories {
		b.helmClient.RegisterGitRepository(repo)
	}

	b.printer.Verbose("  Registered %d Git repositories\n", len(collection.GitRepositories))
}

// registerArtifactGenerators registers all ArtifactGenerators so that
// HelmReleases referencing their ExternalArtifacts via spec.chartRef can be
// resolved to a chart directory inside the repository.
func (b *Builder) registerArtifactGenerators(collection *manifest.ManifestCollection) {
	for _, ag := range collection.ArtifactGenerators {
		b.helmClient.RegisterArtifactGenerator(ag)
	}

	b.printer.Verbose("  Registered %d ArtifactGenerators\n", len(collection.ArtifactGenerators))
}

// resolveHelmReleaseObjects finds HelmRelease objects from manifests or collection
func (b *Builder) resolveHelmReleaseObjects(collection *manifest.ManifestCollection, objects []*unstructured.Unstructured) []*unstructured.Unstructured {
	helmReleaseObjects := findHelmReleases(objects)

	if len(helmReleaseObjects) == 0 && len(collection.HelmReleases) > 0 {
		b.printer.Verbose("  No HelmReleases in manifests, using %d from collection\n", len(collection.HelmReleases))
		for _, hr := range collection.HelmReleases {
			data, err := yaml.Marshal(hr)
			if err != nil {
				b.printer.Verbose("  Warning: failed to marshal HelmRelease %s/%s: %v\n", hr.Namespace, hr.Name, err)
				continue
			}
			obj := &unstructured.Unstructured{}
			if err := yaml.Unmarshal(data, obj); err != nil {
				b.printer.Verbose("  Warning: failed to unmarshal HelmRelease %s/%s: %v\n", hr.Namespace, hr.Name, err)
				continue
			}
			helmReleaseObjects = append(helmReleaseObjects, obj)
		}
	}

	b.printer.Verbose("  Processing %d HelmRelease objects from manifests\n", len(helmReleaseObjects))

	return helmReleaseObjects
}

// templateHelmReleases templates each HelmRelease and returns the generated objects
func (b *Builder) templateHelmReleases(ctx context.Context, helmReleaseObjects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	var helmObjects []*unstructured.Unstructured
	valuesBuilder := helm.NewValuesBuilder()
	if b.options.TolerateMissingValues {
		valuesBuilder.SetTolerateMissing(true)
	}

	for _, hrObj := range helmReleaseObjects {
		hr, err := parseHelmRelease(hrObj)
		if err != nil {
			b.printer.Verbose("    Warning: Failed to parse HelmRelease: %v\n", err)
			continue
		}

		b.printer.Verbose("  Templating HelmRelease: %s/%s (chart: %s, version: %s)\n",
			hr.Namespace, hr.Name, hr.Spec.Chart.Spec.Chart, hr.Spec.Chart.Spec.Version)

		values, err := valuesBuilder.BuildValues(hr)
		if err != nil {
			b.printer.Verbose("    Warning: Failed to build values for %s/%s: %v\n", hr.Namespace, hr.Name, err)
			continue
		}

		chartObjects, err := b.helmClient.Template(ctx, hr, values)
		if err != nil {
			if !b.options.SkipFailedCharts {
				return nil, fmt.Errorf("failed to template HelmRelease %s/%s: %w", hr.Namespace, hr.Name, err)
			}
			b.printer.Verbose("    Warning: Failed to template HelmRelease %s/%s: %v\n", hr.Namespace, hr.Name, err)
			continue
		}

		targetNamespace := hr.Spec.TargetNamespace
		if targetNamespace == "" {
			targetNamespace = hr.Namespace
		}
		for _, obj := range chartObjects {
			if obj.GetNamespace() == "" && isNamespacedResource(obj) {
				obj.SetNamespace(targetNamespace)
			}
		}

		helmObjects = append(helmObjects, chartObjects...)

		b.printer.Verbose("    Generated %d objects from HelmRelease\n", len(chartObjects))
	}

	return helmObjects, nil
}

// findHelmReleases finds all HelmRelease objects in the list
func findHelmReleases(objects []*unstructured.Unstructured) []*unstructured.Unstructured {
	var helmReleases []*unstructured.Unstructured

	for _, obj := range objects {
		if obj.GetKind() == "HelmRelease" && strings.HasPrefix(obj.GetAPIVersion(), "helm.toolkit.fluxcd.io") {
			helmReleases = append(helmReleases, obj)
		}
	}

	return helmReleases
}

// parseHelmRelease parses an unstructured HelmRelease into a typed object
func parseHelmRelease(obj *unstructured.Unstructured) (*types.HelmRelease, error) {
	// Marshal to JSON
	data, err := obj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal HelmRelease: %w", err)
	}

	// Unmarshal to HelmRelease using yaml package
	var hr types.HelmRelease
	if err := yaml.Unmarshal(data, &hr); err != nil {
		return nil, fmt.Errorf("failed to unmarshal HelmRelease: %w", err)
	}

	return &hr, nil
}

// isNamespacedResource checks if a resource is namespaced
// This is a simple heuristic - in reality, we'd need to check the API
func isNamespacedResource(obj *unstructured.Unstructured) bool {
	kind := obj.GetKind()

	// List of known cluster-scoped resources
	clusterScoped := map[string]bool{
		"Namespace":                      true,
		"Node":                           true,
		"PersistentVolume":               true,
		"ClusterRole":                    true,
		"ClusterRoleBinding":             true,
		"CustomResourceDefinition":       true,
		"StorageClass":                   true,
		"PriorityClass":                  true,
		"IngressClass":                   true,
		"RuntimeClass":                   true,
		"APIService":                     true,
		"MutatingWebhookConfiguration":   true,
		"ValidatingWebhookConfiguration": true,
	}

	return !clusterScoped[kind]
}

// parseFluxObjects parses Flux objects from a list of unstructured objects into a collection
func (b *Builder) parseFluxObjects(objects []*unstructured.Unstructured, collection *manifest.ManifestCollection) {
	for _, obj := range objects {
		switch obj.GetKind() {
		case "Kustomization":
			if ks, ok := decodeFluxObject[types.Kustomization](b, obj, "kustomize.toolkit.fluxcd.io"); ok {
				putIfAbsent(collection.Kustomizations, ks.GetKey(), ks)
			}
		case "HelmRelease":
			if hr, ok := decodeFluxObject[types.HelmRelease](b, obj, "helm.toolkit.fluxcd.io"); ok {
				putIfAbsent(collection.HelmReleases, hr.GetKey(), hr)
			}
		case "ArtifactGenerator":
			if ag, ok := decodeFluxObject[types.ArtifactGenerator](b, obj, "source.extensions.fluxcd.io"); ok {
				putIfAbsent(collection.ArtifactGenerators, ag.GetKey(), ag)
			}
		case "HelmRepository":
			if repo, ok := decodeFluxObject[types.HelmRepository](b, obj, "source.toolkit.fluxcd.io"); ok {
				putIfAbsent(collection.HelmRepositories, repo.GetKey(), repo)
			}
		case "GitRepository":
			if repo, ok := decodeFluxObject[types.GitRepository](b, obj, "source.toolkit.fluxcd.io"); ok {
				putIfAbsent(collection.GitRepositories, repo.GetKey(), repo)
			}
		}
	}
}

// decodeFluxObject converts a rendered object into its typed Flux resource.
// It reports false — after a warning, never an error — when the object belongs
// to a different API group or cannot be decoded, so one malformed manifest
// never aborts a whole build.
func decodeFluxObject[T any](b *Builder, obj *unstructured.Unstructured, apiGroup string) (*T, bool) {
	if !strings.HasPrefix(obj.GetAPIVersion(), apiGroup) {
		return nil, false
	}

	kind := obj.GetKind()
	data, err := obj.MarshalJSON()
	if err != nil {
		b.printer.Verbose("  Warning: failed to marshal %s %s/%s: %v\n", kind, obj.GetNamespace(), obj.GetName(), err)
		return nil, false
	}

	var out T
	if err := yaml.Unmarshal(data, &out); err != nil {
		b.printer.Verbose("  Warning: failed to unmarshal %s %s/%s: %v\n", kind, obj.GetNamespace(), obj.GetName(), err)
		return nil, false
	}

	return &out, true
}

// secretData returns the string entries of a Secret's data block, and false
// when the object carries no data map at all. Non-string values cannot be
// Secret entries and are dropped.
func secretData(obj *unstructured.Unstructured) (map[string]string, bool) {
	dataMap, ok := obj.Object["data"].(map[string]interface{})
	if !ok {
		return nil, false
	}

	data := make(map[string]string, len(dataMap))
	for k, v := range dataMap {
		if str, ok := v.(string); ok {
			data[k] = str
		}
	}
	return data, true
}

// putIfAbsent keeps the first resource seen under a key: a nested Kustomization
// re-rendering an object must not overwrite the one already collected. It
// reports whether the value was stored.
func putIfAbsent[T any](m map[string]*T, key string, v *T) bool {
	if _, exists := m[key]; exists {
		return false
	}
	m[key] = v
	return true
}

// discoverNestedKustomizations recursively discovers nested Kustomizations
// This implements a queue-based traversal similar to flux-local's kustomization_traversal()
func (b *Builder) discoverNestedKustomizations(ctx context.Context, collection *manifest.ManifestCollection) error {
	// Track which Kustomizations we've already processed for discovery
	discoveredKeys := make(map[string]bool)
	// Track build failures for error reporting
	buildFailures := make(map[string]error)
	maxIterations := 100 // Safety limit to prevent infinite loops
	iteration := 0

	for {
		iteration++
		if iteration > maxIterations {
			return fmt.Errorf("exceeded maximum iterations (%d) during Kustomization discovery - possible circular reference", maxIterations)
		}

		// Find Kustomizations that haven't been processed for discovery yet
		newKustomizations := []*types.Kustomization{}
		for key, ks := range collection.Kustomizations {
			if !discoveredKeys[key] {
				newKustomizations = append(newKustomizations, ks)
				discoveredKeys[key] = true
			}
		}

		// If no new Kustomizations found, we're done
		if len(newKustomizations) == 0 {
			break
		}

		b.printer.Verbose("Discovering nested Kustomizations from %d sources (iteration %d)...\n", len(newKustomizations), iteration)

		// Make sure GitRepositories discovered so far are registered with the
		// helm client so external Kustomization sources can be resolved on
		// this iteration.
		b.registerGitRepositories(collection)

		// Process each new Kustomization to discover nested ones
		for _, ks := range newKustomizations {
			// Determine build path
			if ks.Spec.Path == "" {
				continue
			}

			buildPath, err := b.ResolveKustomizationBuildPath(ctx, ks)
			if err != nil {
				b.printer.Verbose("    Warning: failed to resolve build path for %s/%s: %v\n", ks.Namespace, ks.Name, err)
				continue
			}

			b.printer.Verbose("  Scanning %s/%s at %s\n", ks.Namespace, ks.Name, buildPath)

			// Build this Kustomization to find nested Flux objects. The raw
			// render is cached and reused later by processKustomization.
			objects, err := b.buildAndParseCached(ctx, buildPath)
			if err != nil {
				// Track the failure
				ksKey := fmt.Sprintf("%s/%s", ks.Namespace, ks.Name)
				buildFailures[ksKey] = err

				// Log warning and continue
				b.printer.Verbose("    Warning: Failed to build: %v\n", err)
				continue
			}

			// Parse any new Kustomizations from the result
			beforeCount := len(collection.Kustomizations)
			b.parseFluxObjects(objects, collection)
			afterCount := len(collection.Kustomizations)

			if afterCount > beforeCount {
				b.printer.Verbose("    Found %d new Kustomization(s)\n", afterCount-beforeCount)
			}
		}
	}

	// If we have build failures and found no HelmReleases, report the failures (only in verbose mode)
	if len(buildFailures) > 0 && len(collection.HelmReleases) == 0 {
		b.printer.Verbose("\nWarning: Kustomize build failed for %d location(s) and no HelmReleases were discovered.\n", len(buildFailures))
		b.printer.Verbose("This usually indicates a compatibility issue between kustomize versions.\n")
		b.printer.Verbose("\nFailed builds:\n")
		for ksKey, err := range buildFailures {
			b.printer.Verbose("  - %s: %v\n", ksKey, err)
		}
		b.printer.Verbose("\n")
	}

	return nil
}

// dedupeObjects keeps one object per (apiVersion, kind, namespace, name),
// the last one in order, at the position of that last occurrence.
// nonFluxObjects keeps only regular Kubernetes objects, dropping the Flux
// resources the entry kustomization rendered alongside them.
func nonFluxObjects(objects []*unstructured.Unstructured) []*unstructured.Unstructured {
	var kept []*unstructured.Unstructured
	for _, obj := range objects {
		kind := obj.GetKind()
		apiVersion := obj.GetAPIVersion()

		if kind == "Kustomization" && strings.HasPrefix(apiVersion, "kustomize.toolkit.fluxcd.io") {
			continue
		}
		if kind == "HelmRelease" && strings.HasPrefix(apiVersion, "helm.toolkit.fluxcd.io") {
			continue
		}
		if kind == "HelmRepository" && strings.HasPrefix(apiVersion, "source.toolkit.fluxcd.io") {
			continue
		}
		if kind == "GitRepository" && strings.HasPrefix(apiVersion, "source.toolkit.fluxcd.io") {
			continue
		}

		kept = append(kept, obj)
	}
	return kept
}

func dedupeObjects(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, int) {
	lastIndex := make(map[string]int, len(objects))
	for i, obj := range objects {
		lastIndex[objectIdentity(obj)] = i
	}
	if len(lastIndex) == len(objects) {
		return objects, 0
	}
	out := make([]*unstructured.Unstructured, 0, len(lastIndex))
	for i, obj := range objects {
		if lastIndex[objectIdentity(obj)] == i {
			out = append(out, obj)
		}
	}
	return out, len(objects) - len(out)
}

func objectIdentity(obj *unstructured.Unstructured) string {
	return obj.GetAPIVersion() + "|" + obj.GetKind() + "|" + obj.GetNamespace() + "|" + obj.GetName()
}
