package kube

import "strings"

func ParseClusters(s string) []string { return strings.Fields(s) }
