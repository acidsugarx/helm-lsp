package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// WordAtPosition extracts the word under the cursor in a specific line.
// In Helm context, we want to extract things like ".Values.image.repository"
func WordAtPosition(line string, charIndex int) (string, error) {
	if charIndex < 0 || charIndex > len(line) {
		return "", fmt.Errorf("position out of bounds")
	}

	// For Helm templates, a "word" is usually a dot-separated path, alphanumeric chars, underscores, dashes, and $ for variables.
	// E.g., .Values.my-db.user_name or $ing.annotations
	// It could also just be a plain function name like `include`, `dict`, `default`
	wordRegex := regexp.MustCompile(`[\$a-zA-Z0-9_\-\.]+`)
	matches := wordRegex.FindAllStringIndex(line, -1)

	for _, match := range matches {
		start, end := match[0], match[1]
		// If cursor is within or immediately after the match
		if charIndex >= start && charIndex <= end {
			word := line[start:end]
			// Trim trailing dots which might happen during typing
			return strings.TrimRight(word, "."), nil
		}
	}

	return "", nil
}

// ExtractValuesPath checks if the word is a Values path and returns the clean path.
// Input: ".Values.image.tag" -> Output: []string{"image", "tag"}
func ExtractValuesPath(word string) ([]string, bool) {
	if !strings.HasPrefix(word, ".Values.") {
		return nil, false
	}

	pathStr := strings.TrimPrefix(word, ".Values.")
	if pathStr == "" {
		return nil, true // Just ".Values."
	}

	return strings.Split(pathStr, "."), true
}

// ResolveValuesPath takes the document lines and the current line index,
// and resolves a variable (e.g. $ing.annotations) to its underlying .Values path
// by scanning upwards for its assignment.
func ResolveValuesPath(lines []string, lineIdx int, word string) ([]string, bool, bool) {
	if strings.HasPrefix(word, ".Values.") {
		path, isValues := ExtractValuesPath(word)
		return path, isValues, false
	}

	if !strings.HasPrefix(word, "$") {
		return nil, false, false
	}

	parts := strings.SplitN(word, ".", 2)
	varName := parts[0] // e.g. "$ing" or "$host"

	// Look upwards from lineIdx to find variable assignment
	// We handle two common cases:
	// 1: range $key, $val := .Values.ingresses
	// 2: $val := .Values.someVar
	// Case 1: range $key, $val := .Values.path
	assignPatternKV := `range\s+([\$a-zA-Z0-9_\-\.]+)\s*,\s*([\$a-zA-Z0-9_\-\.]+)\s*:=\s*\.Values\.([a-zA-Z0-9_\-\.]+)`
	assignRegexKV := regexp.MustCompile(assignPatternKV)

	// Case 2: $val := .Values.path (or range $val := .Values.path)
	assignPatternV := fmt.Sprintf(`(?:range\s+)?%s\s*:=\s*\.Values\.([a-zA-Z0-9_\-\.]+)`, regexp.QuoteMeta(varName))
	assignRegexV := regexp.MustCompile(assignPatternV)

	for i := lineIdx; i >= 0; i-- {
		if i >= len(lines) {
			continue
		}

		line := lines[i]

		// Try Key-Value match first
		matchesKV := assignRegexKV.FindStringSubmatch(line)
		if len(matchesKV) == 4 {
			keyVar := matchesKV[1]
			valVar := matchesKV[2]
			valuesPath := matchesKV[3]

			if varName == keyVar {
				// The word is the KEY of the map
				return strings.Split(valuesPath, "."), true, true
			} else if varName == valVar {
				// The word is the VALUE of the map
				basePath := strings.Split(valuesPath, ".")
				if len(parts) > 1 && parts[1] != "" {
					basePath = append(basePath, strings.Split(parts[1], ".")...)
				}
				return basePath, true, false
			}
		}

		// Try Value-only match
		matchesV := assignRegexV.FindStringSubmatch(line)
		if len(matchesV) > 1 {
			// matchesV[1] is the path after .Values.
			basePath := strings.Split(matchesV[1], ".")

			// If the word was $ing.annotations, append "annotations"
			if len(parts) > 1 && parts[1] != "" {
				basePath = append(basePath, strings.Split(parts[1], ".")...)
			}
			return basePath, true, false
		}
	}

	return nil, false, false
}

// TemplateAtPosition checks if the cursor is currently inside a template name
// string, such as: include "helpers.tplvalues.render" or template "my.tmpl"
func TemplateAtPosition(line string, charIndex int) (string, bool) {
	if charIndex < 0 || charIndex > len(line) {
		return "", false
	}

	// We look for: (include|template)\s+"([^"]+)"
	tmplRegex := regexp.MustCompile(`(?:include|template)\s+"([^"]+)"`)
	matches := tmplRegex.FindAllStringSubmatchIndex(line, -1)

	for _, match := range matches {
		// match[0], match[1] = full match start and end
		// match[2], match[3] = group 1 (the template name) start and end
		fullStart, fullEnd := match[0], match[1]
		nameStart, nameEnd := match[2], match[3]

		// Check if cursor is anywhere within the include/template statement
		// We could restrict it to just the name, but being anywhere in the statement is usually fine
		if charIndex >= fullStart && charIndex <= fullEnd {
			tmplName := line[nameStart:nameEnd]
			return tmplName, true
		}
	}

	return "", false
}
