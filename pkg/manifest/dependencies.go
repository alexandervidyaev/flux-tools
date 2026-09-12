// Package manifest collects the Flux resources of one cluster and orders the
// Kustomizations by their dependsOn graph.
package manifest

import (
	"fmt"

	"github.com/alexandervidyaev/flux-tools/pkg/types"
)

// DependencyGraph represents a directed acyclic graph of dependencies
type DependencyGraph struct {
	// nodes maps resource key to its dependencies
	nodes map[string][]string
	// reverse dependencies (what depends on this node)
	dependents map[string][]string
}

// NewDependencyGraph creates a new dependency graph
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		nodes:      make(map[string][]string),
		dependents: make(map[string][]string),
	}
}

// AddNode adds a node to the graph
func (dg *DependencyGraph) AddNode(key string) {
	if _, exists := dg.nodes[key]; !exists {
		dg.nodes[key] = []string{}
	}
	if _, exists := dg.dependents[key]; !exists {
		dg.dependents[key] = []string{}
	}
}

// AddDependency adds a dependency edge (from depends on to)
func (dg *DependencyGraph) AddDependency(from, to string) {
	dg.AddNode(from)
	dg.AddNode(to)

	// Add edge: from -> to
	dg.nodes[from] = append(dg.nodes[from], to)

	// Add reverse edge: to <- from
	dg.dependents[to] = append(dg.dependents[to], from)
}

// TopologicalSort performs topological sort on the graph
// Returns sorted nodes or an error if a cycle is detected
func (dg *DependencyGraph) TopologicalSort() ([]string, error) {
	// Calculate in-degree for each node
	inDegree := make(map[string]int)
	for node := range dg.nodes {
		inDegree[node] = 0
	}
	for _, deps := range dg.nodes {
		for _, dep := range deps {
			inDegree[dep]++
		}
	}

	// Queue of nodes with no incoming edges
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	var sorted []string
	visited := make(map[string]bool)

	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]

		sorted = append(sorted, current)
		visited[current] = true

		// Process dependencies
		for _, dep := range dg.nodes[current] {
			inDegree[dep]--
			if inDegree[dep] == 0 && !visited[dep] {
				queue = append(queue, dep)
			}
		}
	}

	// Check for cycles
	if len(sorted) != len(dg.nodes) {
		// Find nodes involved in cycle
		var cycleNodes []string
		for node := range dg.nodes {
			if !visited[node] {
				cycleNodes = append(cycleNodes, node)
			}
		}
		return nil, fmt.Errorf("circular dependency detected involving: %v", cycleNodes)
	}

	return sorted, nil
}

// BuildDependencyGraph builds a dependency graph from a manifest collection
func BuildDependencyGraph(collection *ManifestCollection) (*DependencyGraph, error) {
	graph := NewDependencyGraph()

	// Add all Kustomizations to the graph
	for key, ks := range collection.Kustomizations {
		graph.AddNode(key)

		// Add dependencies
		for _, depKey := range ks.GetDependencyKeys() {
			graph.AddDependency(key, depKey)
		}
	}

	// Add all HelmReleases to the graph
	for key, hr := range collection.HelmReleases {
		graph.AddNode(key)

		// Add dependencies
		for _, depKey := range hr.GetDependencyKeys() {
			graph.AddDependency(key, depKey)
		}
	}

	return graph, nil
}

// GetProcessingOrder returns the order in which resources should be processed
// based on their dependencies
func GetProcessingOrder(collection *ManifestCollection) ([]*types.Kustomization, error) {
	graph, err := BuildDependencyGraph(collection)
	if err != nil {
		return nil, err
	}

	sorted, err := graph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	// Build result in processing order
	var result []*types.Kustomization
	for _, key := range sorted {
		if ks, ok := collection.Kustomizations[key]; ok {
			result = append(result, ks)
		}
	}

	return result, nil
}

// ValidateDependencies validates that all dependencies exist
func ValidateDependencies(collection *ManifestCollection) error {
	// Check Kustomization dependencies
	for key, ks := range collection.Kustomizations {
		for _, depKey := range ks.GetDependencyKeys() {
			if _, exists := collection.Kustomizations[depKey]; !exists {
				return fmt.Errorf("kustomization %s depends on %s which does not exist", key, depKey)
			}
		}
	}

	// Check HelmRelease dependencies
	for key, hr := range collection.HelmReleases {
		for _, depKey := range hr.GetDependencyKeys() {
			// HelmRelease can depend on other HelmReleases or Kustomizations
			_, ksExists := collection.Kustomizations[depKey]
			_, hrExists := collection.HelmReleases[depKey]

			if !ksExists && !hrExists {
				return fmt.Errorf("helmrelease %s depends on %s which does not exist", key, depKey)
			}
		}
	}

	return nil
}
