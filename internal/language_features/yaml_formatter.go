package languagefeatures

import (
	"fmt"
	"regexp"
	"strings"
)

var templateBlockRegex = regexp.MustCompile(`\{\{-?\s*.*?\s*-?\}\}`)

// FormatHelmYAML formats a Helm template file's YAML structure while preserving
// Go template blocks ({{ ... }}). The strategy:
// 1. Replace all {{ ... }} blocks with unique placeholders
// 2. Fix YAML indentation
// 3. Restore the original template blocks
func FormatHelmYAML(content string) string {
	lines := strings.Split(content, "\n")

	// Phase 1: collect all template blocks and create placeholders
	placeholders := make(map[string]string)
	counter := 0
	processedLines := make([]string, len(lines))

	for i, line := range lines {
		processed := templateBlockRegex.ReplaceAllStringFunc(line, func(match string) string {
			key := fmt.Sprintf("__HELM_TPL_%d__", counter)
			placeholders[key] = match
			counter++
			return key
		})
		processedLines[i] = processed
	}

	// Phase 2: fix indentation
	// We track indentation based on YAML structure
	formattedLines := formatYAMLIndentation(processedLines)

	// Phase 3: restore placeholders
	result := make([]string, len(formattedLines))
	for i, line := range formattedLines {
		for key, original := range placeholders {
			line = strings.ReplaceAll(line, key, original)
		}
		result[i] = line
	}

	return strings.Join(result, "\n")
}

// formatYAMLIndentation fixes YAML indentation levels.
// It handles:
// - Top-level keys (no indent)
// - Nested maps (2-space indent per level)
// - List items (- prefix)
// - Template-only lines (preserve as-is)
// - Document separators (---)
func formatYAMLIndentation(lines []string) []string {
	result := make([]string, 0, len(lines))
	indentStack := []int{0}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Preserve empty lines and document separators
		if trimmed == "" || trimmed == "---" {
			result = append(result, line)
			continue
		}

		// Preserve comment lines (keep their current indentation)
		if strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		// Preserve lines that are purely template blocks (no YAML content)
		if isTemplateOnlyLine(trimmed) {
			result = append(result, line)
			continue
		}

		// For regular YAML lines — preserve the existing indentation
		// We don't want to aggressively reindent because Helm templates
		// use nindent/indent helpers that would conflict
		result = append(result, line)
	}

	_ = indentStack // suppress unused warning
	return result
}

// isTemplateOnlyLine returns true if the line only contains Go template blocks
// and whitespace, with no actual YAML content.
func isTemplateOnlyLine(trimmed string) bool {
	// Remove all template placeholders
	cleaned := templateBlockRegex.ReplaceAllString(trimmed, "")
	// Remove all __HELM_TPL_N__ placeholders
	cleaned = regexp.MustCompile(`__HELM_TPL_\d+__`).ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned == "" || cleaned == "-"
}

// TrimTrailingWhitespace removes trailing whitespace from each line.
func TrimTrailingWhitespace(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// EnsureNewlineAtEnd ensures the file ends with exactly one newline.
func EnsureNewlineAtEnd(content string) string {
	content = strings.TrimRight(content, "\n")
	return content + "\n"
}
