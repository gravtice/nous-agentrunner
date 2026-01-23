package runnerd

import (
	"strings"
)

func normalizeImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "local/") {
		return ref
	}
	// If the first path segment looks like a registry host (contains '.' or ':' or is 'localhost'),
	// treat as explicit. Otherwise it's an implicit Docker Hub reference.
	first, _, ok := strings.Cut(ref, "/")
	if !ok {
		// "alpine:3.19" (no slash) - leave as-is; caller may reject via registry base.
		return ref
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return ref
	}
	return "docker.io/" + ref
}
