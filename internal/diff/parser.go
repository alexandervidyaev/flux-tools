package diff

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Resource-name regexes, compiled once at package level (ParseResourceInfo is
// called for every file in a diff).
var (
	namespaceRegex = regexp.MustCompile(`Namespace:([^/]*)`)
	kindRegex      = regexp.MustCompile(`Kind:([^/]+)`)
	nameRegex      = regexp.MustCompile(`Name:([^/]+)`)
)

// ParseResourceInfo extracts resource information from filename
// Expected format: Namespace:prod/Kind:Deployment/Name:app1.yaml
func ParseResourceInfo(filePath string) ResourceInfo {
	info := ResourceInfo{
		Namespace: "(none)",
		Kind:      "Unknown",
		Name:      filepath.Base(filePath),
	}

	// Remove .yaml/.yml extension
	nameWithoutExt := strings.TrimSuffix(filePath, filepath.Ext(filePath))

	// Parse pattern: Namespace:X/Kind:Y/Name:Z
	if matches := namespaceRegex.FindStringSubmatch(nameWithoutExt); len(matches) > 1 {
		if matches[1] == "" || matches[1] == " " {
			info.Namespace = "(none)"
		} else {
			info.Namespace = matches[1]
		}
	}

	if matches := kindRegex.FindStringSubmatch(nameWithoutExt); len(matches) > 1 {
		info.Kind = matches[1]
	}

	if matches := nameRegex.FindStringSubmatch(nameWithoutExt); len(matches) > 1 {
		info.Name = matches[1]
	}

	return info
}
