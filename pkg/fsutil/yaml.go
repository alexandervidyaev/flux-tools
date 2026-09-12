package fsutil

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// IsYAMLFile checks if a file has a YAML extension (.yaml or .yml, case-insensitive).
func IsYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// kustomizationFileNames are the file names kustomize recognises as a
// kustomization, in the order kustomize itself checks them.
var kustomizationFileNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// HasKustomizationFile reports whether dir holds a kustomization file. A
// directory carrying one of those names is not one, so it never counts.
func HasKustomizationFile(dir string) bool {
	for _, name := range kustomizationFileNames {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// FindOptions configures FindYAMLFiles behavior.
type FindOptions struct {
	ExcludeClean bool // Exclude files containing ".clean." in their name.
	Recursive    bool // Walk subdirectories (default behavior when zero value).
}

// FindYAMLFiles finds YAML files at the given path.
// If path is a file, it is returned if it has a YAML extension.
// If path is a directory, it is walked (recursively by default).
// When no options are provided, defaults to recursive with no exclusions.
func FindYAMLFiles(path string, opts ...FindOptions) ([]string, error) {
	opt := FindOptions{Recursive: true}
	if len(opts) > 0 {
		opt = opts[0]
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		if IsYAMLFile(path) {
			return []string{path}, nil
		}
		return nil, nil
	}

	var manifests []string

	// WalkDir avoids an os.Lstat per entry (it reuses the readdir type),
	// which is noticeably faster on large trees than filepath.Walk.
	err = filepath.WalkDir(path, func(filePath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			// If non-recursive, skip subdirectories (but not the root).
			if !opt.Recursive && filePath != path {
				return filepath.SkipDir
			}
			return nil
		}

		if !IsYAMLFile(filePath) {
			return nil
		}

		if opt.ExcludeClean && strings.Contains(filePath, ".clean.") {
			return nil
		}

		manifests = append(manifests, filePath)
		return nil
	})

	return manifests, err
}

// SplitYAMLDocuments splits a multi-document YAML byte slice into individual
// document byte slices on lines that are exactly "---" (ignoring surrounding
// whitespace). Empty documents are dropped.
//
// This is the single correct implementation shared by the build, test and diff
// paths. It is line-based rather than a naive strings.Split(data, "\n---"),
// which mis-splits content such as "----" or "--- # comment".
func SplitYAMLDocuments(data []byte) [][]byte {
	var documents [][]byte
	var currentDoc []byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Allow long lines (default bufio limit is 64KiB; manifests can exceed it).
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.Equal(bytes.TrimSpace(line), []byte("---")) {
			if len(bytes.TrimSpace(currentDoc)) > 0 {
				documents = append(documents, currentDoc)
			}
			currentDoc = nil
			continue
		}
		currentDoc = append(currentDoc, line...)
		currentDoc = append(currentDoc, '\n')
	}

	if len(bytes.TrimSpace(currentDoc)) > 0 {
		documents = append(documents, currentDoc)
	}

	return documents
}
