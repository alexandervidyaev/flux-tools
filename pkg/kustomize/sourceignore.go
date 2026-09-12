package kustomize

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SourceIgnore holds the patterns of a .sourceignore file, the file Flux's
// source-controller reads at the root of a GitRepository to leave files out of
// the artifact (https://fluxcd.io/flux/components/source/gitrepositories/#ignore).
// The format is that of .gitignore; the subset implemented here is:
//
//   - blank lines and lines starting with # are skipped;
//   - a trailing / makes the pattern match directories only;
//   - a leading / anchors the pattern to the root, a pattern with a / inside is
//     anchored as well, any other pattern matches at any depth;
//   - *, ? and [...] match within one path segment, ** spans segments;
//   - a leading ! negates: the last matching pattern wins.
//
// Paths are matched relative to the root, with forward slashes.
type SourceIgnore struct {
	root     string
	patterns []ignorePattern
}

type ignorePattern struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// SourceIgnoreFileName is the file source-controller reads.
const SourceIgnoreFileName = ".sourceignore"

// LoadSourceIgnore reads <root>/.sourceignore. A missing file yields an empty
// matcher, not an error.
func LoadSourceIgnore(root string) (*SourceIgnore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	// Paths are compared after symlink resolution on both sides: the
	// generator walks real paths, and TMPDIR on macOS is a symlink.
	ig := &SourceIgnore{root: realPath(abs)}

	f, err := os.Open(filepath.Join(abs, SourceIgnoreFileName))
	if os.IsNotExist(err) {
		return ig, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", SourceIgnoreFileName, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if p, ok := parseIgnoreLine(scanner.Text()); ok {
			ig.patterns = append(ig.patterns, p)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", SourceIgnoreFileName, err)
	}
	return ig, nil
}

func parseIgnoreLine(line string) (ignorePattern, bool) {
	line = strings.TrimRight(line, " \t")
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	p := ignorePattern{}
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if strings.HasPrefix(line, "/") {
		p.anchored = true
		line = strings.TrimPrefix(line, "/")
	} else if strings.Contains(line, "/") {
		p.anchored = true
	}
	if line == "" {
		return ignorePattern{}, false
	}
	p.pattern = line
	return p, true
}

// Root is the directory the patterns are relative to.
func (ig *SourceIgnore) Root() string {
	if ig == nil {
		return ""
	}
	return ig.root
}

// Ignored reports whether absPath, which must be under Root, is left out by
// the patterns. Paths outside the root are never ignored: the patterns say
// nothing about them.
func (ig *SourceIgnore) Ignored(absPath string, isDir bool) bool {
	if ig == nil || len(ig.patterns) == 0 {
		return false
	}
	rel, err := filepath.Rel(ig.root, realPath(absPath))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	rel = filepath.ToSlash(rel)

	ignored := false
	for _, p := range ig.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.matches(rel) {
			ignored = !p.negate
		}
	}
	return ignored
}

func (p ignorePattern) matches(rel string) bool {
	if p.anchored {
		return globMatch(p.pattern, rel)
	}
	// Unanchored: the pattern may match the path at any depth.
	segments := strings.Split(rel, "/")
	for i := range segments {
		if globMatch(p.pattern, strings.Join(segments[i:], "/")) {
			return true
		}
	}
	return false
}

// globMatch matches pattern against rel, where * does not cross a / and **
// spans any number of segments. A pattern matching a parent directory matches
// everything below it, as in gitignore.
func globMatch(pattern, rel string) bool {
	pSegs := strings.Split(pattern, "/")
	rSegs := strings.Split(rel, "/")
	return matchSegments(pSegs, rSegs)
}

func matchSegments(pSegs, rSegs []string) bool {
	if len(pSegs) == 0 {
		// The whole pattern is consumed: a match, whether rel ended here or
		// continues below a matched directory.
		return true
	}
	if pSegs[0] == "**" {
		for i := 0; i <= len(rSegs); i++ {
			if matchSegments(pSegs[1:], rSegs[i:]) {
				return true
			}
		}
		return false
	}
	if len(rSegs) == 0 {
		return false
	}
	ok, err := path.Match(pSegs[0], rSegs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pSegs[1:], rSegs[1:])
}
