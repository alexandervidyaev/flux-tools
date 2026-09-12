package kustomize

import (
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// placeholderRe matches ${varName} placeholders. Compiled once at package
// level: substituteString runs on every string of every object of every
// cluster (the hottest path of `build`), so per-call compilation was pure
// waste.
var placeholderRe = regexp.MustCompile(`\$\{([a-zA-Z0-9_-]+)\}`)

// Substitution handles variable substitution in Kubernetes objects
type Substitution struct {
	variables map[string]string
}

// NewSubstitution creates a new Substitution handler
func NewSubstitution(variables map[string]string) *Substitution {
	return &Substitution{
		variables: variables,
	}
}

// ApplySubstitutions applies variable substitutions to a list of objects
func (s *Substitution) ApplySubstitutions(objects []*unstructured.Unstructured) error {
	for _, obj := range objects {
		if err := s.substituteObject(obj); err != nil {
			return fmt.Errorf("failed to substitute variables in %s/%s: %w",
				obj.GetKind(), obj.GetName(), err)
		}
	}
	return nil
}

// substituteObject recursively substitutes variables in an object
func (s *Substitution) substituteObject(obj *unstructured.Unstructured) error {
	// Get the object as a map
	content := obj.Object

	// Recursively substitute in the map
	if err := s.substituteInMap(content); err != nil {
		return err
	}

	return nil
}

// substituteInMap recursively substitutes variables in a map
func (s *Substitution) substituteInMap(m map[string]interface{}) error {
	for key, value := range m {
		switch v := value.(type) {
		case string:
			// Substitute in string values
			m[key] = s.substituteString(v)
		case map[string]interface{}:
			// Recursively substitute in nested maps
			if err := s.substituteInMap(v); err != nil {
				return err
			}
		case []interface{}:
			// Recursively substitute in slices
			if err := s.substituteInSlice(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// substituteInSlice recursively substitutes variables in a slice
func (s *Substitution) substituteInSlice(slice []interface{}) error {
	for i, value := range slice {
		switch v := value.(type) {
		case string:
			// Substitute in string values
			slice[i] = s.substituteString(v)
		case map[string]interface{}:
			// Recursively substitute in nested maps
			if err := s.substituteInMap(v); err != nil {
				return err
			}
		case []interface{}:
			// Recursively substitute in nested slices
			if err := s.substituteInSlice(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// substituteString substitutes variables in a string
// Supports ${varName} syntax
func (s *Substitution) substituteString(str string) string {
	result := placeholderRe.ReplaceAllStringFunc(str, func(match string) string {
		// Extract variable name (remove ${ and })
		varName := match[2 : len(match)-1]

		// Look up the variable
		if value, exists := s.variables[varName]; exists {
			return value
		}

		// If variable not found, keep the original
		return match
	})

	return result
}
