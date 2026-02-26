package languagefeatures

import (
	"fmt"
	"regexp"
	"strings"
)

var templateBlockRegex = regexp.MustCompile(`\{\{-?\s*.*?\s*-?\}\}`)

// FormatHelmYAML formats a Helm template file's YAML structure while preserving
// Go template blocks ({{ ... }}).
func FormatHelmYAML(content string, enabled bool) string {
	if !enabled {
		return content
	}

	lines := strings.Split(content, "\n")
	placeholders := make(map[string]string)
	counter := 0
	processedLines := make([]string, len(lines))

	for i, line := range lines {
		// Do not process placeholders for lines with nindent/indent
		// we want to freeze these completely
		if strings.Contains(line, "nindent") || strings.Contains(line, "indent") {
			processedLines[i] = line
			continue
		}

		processed := templateBlockRegex.ReplaceAllStringFunc(line, func(match string) string {
			key := fmt.Sprintf("__HELM_TPL_%d__", counter)
			placeholders[key] = match
			counter++
			return key
		})
		processedLines[i] = processed
	}

	formattedLines := formatYAMLIndentation(processedLines)

	result := make([]string, len(formattedLines))
	for i, line := range formattedLines {
		for key, original := range placeholders {
			line = strings.ReplaceAll(line, key, original)
		}
		result[i] = line
	}

	return strings.Join(result, "\n")
}

// formatYAMLIndentation fixes YAML indentation levels using a simple heuristic.
func formatYAMLIndentation(lines []string) []string {
	result := make([]string, 0, len(lines))

	// A stack to track indentation of block levels
	indentStack := []int{0}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Preserve empty lines and document separators
		if trimmed == "" || trimmed == "---" {
			result = append(result, line)
			indentStack = []int{0}
			continue
		}

		// Preserve comment lines by matching current stack depth if possible
		// or just leaving them alone. We leave them alone for safety.
		if strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		if isTemplateOnlyLine(trimmed) {
			// Align pure template lines (like if/end) to current indent level
			// But do not push or pop stack
			currentIndent := indentStack[len(indentStack)-1]
			result = append(result, strings.Repeat(" ", currentIndent)+trimmed)
			continue
		}

		if strings.Contains(line, "nindent") || strings.Contains(line, "indent") {
			result = append(result, line)
			continue
		}

		// Calculate existing physical indent
		physicalIndent := len(line) - len(strings.TrimLeft(line, " \t"))

		// If this line is un-indented relative to top of stack, pop until it matches
		// (this relies on the user partially formatting their code, which is realistic)
		for len(indentStack) > 1 {
			parentIndent := indentStack[len(indentStack)-2]
			if physicalIndent <= parentIndent {
				indentStack = indentStack[:len(indentStack)-1]
			} else {
				break
			}
		}

		currentIndent := indentStack[len(indentStack)-1]

		// Format the line with the current indent
		if strings.HasPrefix(trimmed, "-") {
			result = append(result, strings.Repeat(" ", currentIndent)+trimmed)
			// A list item usually means child objects are indented by 2 spaces relative to the dash
			if strings.HasSuffix(trimmed, ":") {
				indentStack = append(indentStack, currentIndent+2)
			}
		} else {
			result = append(result, strings.Repeat(" ", currentIndent)+trimmed)
			if strings.HasSuffix(trimmed, ":") {
				// Next block should be indented further
				indentStack = append(indentStack, currentIndent+2)
			}
		}
	}

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
