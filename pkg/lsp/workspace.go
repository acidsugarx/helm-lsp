package lsp

import (
	"os"
	"path/filepath"
	"strings"
)

// uriToPath converts an LSP file:// URI to a local filesystem path.
func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

// pathToURI converts a local filesystem path to an LSP file:// URI.
func pathToURI(path string) string {
	return "file://" + path
}

// findChartRoot starts at the given directory path and walks up until it finds a directory containing Chart.yaml.
// Returns the path to the chart root directory, or empty string if not found.
func findChartRoot(startPath string) string {
	current := startPath
	for {
		if _, err := os.Stat(filepath.Join(current, "Chart.yaml")); err == nil {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current || parent == "." || parent == "/" {
			break
		}
		current = parent
	}
	return ""
}
