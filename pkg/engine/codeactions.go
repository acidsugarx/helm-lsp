package engine

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// CodeActionKinds supported by this LSP
var CodeActionKinds = []protocol.CodeActionKind{
	protocol.CodeActionKindQuickFix,
	protocol.CodeActionKindRefactorExtract,
	protocol.CodeActionKindSource,
}

// Pre-compiled regexes for code actions
var (
	// Matches YAML keys including annotations with dots, slashes, etc.
	// e.g. "  replicas: 3", "    kubernetes.io/ingress.class: nginx", "    - name: APP_ENV"
	yamlKVRegex = regexp.MustCompile(`^(\s*(?:-\s+)?)([\w\-\.\/]+):\s+(.+)$`)

	// Matches template expressions for quote suggestion
	quoteRegex = regexp.MustCompile(`(\{\{-?\s*(?:\$\.|\.)[^\}]+?)(\s*-?\}\})`)

	// Matches include/template with | indent N
	indentPipeRegex = regexp.MustCompile(`(\{\{-?\s*(?:include|template)\s+"[^"]+"\s+[^\}]+)\|\s*indent\s+(\d+)\s*(-?\}\})`)

	// Block detection regexes
	rangeBlockRegex  = regexp.MustCompile(`\{\{-?\s*range\b`)
	rangeVarsRegex   = regexp.MustCompile(`range\s+(\$\w+)\s*,\s*(\$\w+)\s*:=`)
	rangeSimpleRegex = regexp.MustCompile(`range\s+(\$\w+)\s*:=`)
	withBlockRegex   = regexp.MustCompile(`\{\{-?\s*with\b`)
	endBlockRegex    = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	defineBlockRegex = regexp.MustCompile(`\{\{-?\s*define\b`)

	// Matches a YAML key (without value) — for path detection
	yamlKeyOnlyRegex = regexp.MustCompile(`^(\s*)([\w\-\.\/]+):\s*$`)
)

// GetCodeActions analyzes the document at the given range and returns applicable code actions.
func GetCodeActions(content string, rng protocol.Range, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction
	lines := strings.Split(content, "\n")
	lineIdx := int(rng.Start.Line)

	if lineIdx >= len(lines) {
		return actions
	}

	line := lines[lineIdx]
	trimmed := strings.TrimSpace(line)

	// Detect template scope context (range, with, define)
	scope := detectScope(lines, lineIdx)

	// 1. Extract hardcoded value to Values (scope-aware + YAML path-aware)
	actions = append(actions, extractToValuesActions(lines, line, trimmed, lineIdx, uri, scope)...)

	// 2. Quote/toYaml helpers
	actions = append(actions, quoteWrapActions(line, lineIdx, uri)...)

	// 3. indent → nindent conversion
	actions = append(actions, nindentActions(line, lineIdx, uri)...)

	// 4. Add nindent to toYaml without it
	actions = append(actions, toYamlNindentActions(line, lineIdx, uri)...)

	return actions
}

// TemplateScope describes the enclosing template scope context for a given line.
type TemplateScope struct {
	InRange     bool
	InWith      bool
	InDefine    bool
	Depth       int
	RootRef     string // "$" if inside range/with, "." if at top level
	RangeKeyVar string // e.g. "$name" from `range $name, $ing := ...`
	RangeValVar string // e.g. "$ing" from `range $name, $ing := ...`
	WithVar     string // e.g. "$res" from `with $res := ...`
}

// detectScope scans upward from lineIdx to determine the enclosing template scope.
func detectScope(lines []string, lineIdx int) TemplateScope {
	scope := TemplateScope{RootRef: "."}
	depth := 0

	for i := lineIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])

		// Skip empty/comment lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}

		// Count end blocks going up — each end we pass means there's a closed block above
		endCount := len(endBlockRegex.FindAllString(trimmed, -1))
		depth += endCount

		// Check for openers
		if rangeBlockRegex.MatchString(trimmed) {
			if depth > 0 {
				depth--
			} else {
				scope.InRange = true
				scope.Depth++
				scope.RootRef = "$"
				// Extract range variables: `range $key, $val := ...`
				if m := rangeVarsRegex.FindStringSubmatch(trimmed); m != nil {
					scope.RangeKeyVar = m[1]
					scope.RangeValVar = m[2]
				} else if m := rangeSimpleRegex.FindStringSubmatch(trimmed); m != nil {
					scope.RangeValVar = m[1]
				}
				log.Printf("CodeAction: detected RANGE at line %d (key=%s val=%s)", i, scope.RangeKeyVar, scope.RangeValVar)
			}
			continue
		}
		if withBlockRegex.MatchString(trimmed) {
			if depth > 0 {
				depth--
			} else {
				scope.InWith = true
				scope.Depth++
				scope.RootRef = "$"
				log.Printf("CodeAction: detected WITH at line %d (from line %d)", i, lineIdx)
			}
			continue
		}
		if defineBlockRegex.MatchString(trimmed) {
			if depth > 0 {
				depth--
			} else {
				scope.InDefine = true
				scope.RootRef = "$"
			}
			continue
		}
	}

	log.Printf("CodeAction: scope for line %d → InRange=%v InWith=%v RootRef=%q Depth=%d",
		lineIdx, scope.InRange, scope.InWith, scope.RootRef, scope.Depth)
	return scope
}

// detectYAMLPath scans upward from lineIdx to build the YAML key path.
// For example, if the cursor is on `replicas: 3` under `spec:`, returns ["spec"].
// Handles nested indentation and skips list items, template lines, and comments.
func detectYAMLPath(lines []string, lineIdx int) []string {
	if lineIdx >= len(lines) {
		return nil
	}

	currentLine := lines[lineIdx]
	currentIndent := countIndent(currentLine)

	var path []string
	targetIndent := currentIndent

	for i := lineIdx - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip empty, comments, template-only, document separators
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		if strings.HasPrefix(trimmed, "{{") {
			continue
		}
		// Skip list items (lines starting with -)
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			continue
		}

		lineIndent := countIndent(line)

		// We're looking for keys at a LOWER indentation level (parent keys)
		if lineIndent < targetIndent {
			m := yamlKeyOnlyRegex.FindStringSubmatch(line)
			if m != nil {
				parentKey := m[2]
				path = append([]string{parentKey}, path...)
				targetIndent = lineIndent
			}
			if lineIndent == 0 {
				break // Top level reached
			}
		}
	}

	return path
}

func countIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// sanitizeKeyForValues converts a YAML key to a valid Go template path segment.
// For simple keys like "replicas", returns as-is.
// For annotation-style keys like "kubernetes.io/ingress.class" → "ingressClass".
// For keys with hyphens like "cert-manager.io/cluster-issuer" → "clusterIssuer".
func sanitizeKeyForValues(key string) string {
	// If the key is a simple identifier (no dots, slashes, hyphens), return as-is
	if !strings.ContainsAny(key, "./-") {
		return key
	}

	// Normalize all separators to dots
	normalized := strings.NewReplacer("/", ".", "-", ".").Replace(key)
	parts := strings.Split(normalized, ".")

	// Drop common prefixes: kubernetes, io, cert, manager, etc.
	prefixes := map[string]bool{
		"kubernetes": true, "io": true, "cert": true, "manager": true,
		"k8s": true, "nginx": true, "app": true,
	}

	// Find the first meaningful segment (skip common prefixes)
	startIdx := 0
	for startIdx < len(parts)-1 && prefixes[parts[startIdx]] {
		startIdx++
	}

	// CamelCase the remaining segments
	if startIdx >= len(parts) {
		startIdx = len(parts) - 1
	}

	result := parts[startIdx]
	for i := startIdx + 1; i < len(parts); i++ {
		p := parts[i]
		if len(p) > 0 {
			result += strings.ToUpper(p[:1]) + p[1:]
		}
	}

	return result
}

// quoteDefault wraps a value in quotes if it's a string (not a number or bool).
// Go templates need `default "nginx"` not `default nginx` (nginx would be treated as a function).
func quoteDefault(value string) string {
	// If it's a pure integer or float (Go template-valid), leave as-is
	// K8s quantities like 500m, 256Mi are NOT valid Go numbers — they must be quoted
	isNumeric := regexp.MustCompile(`^\d+(\.\d+)?$`).MatchString(value)
	if isNumeric {
		return value
	}

	// If it's a boolean, leave as-is
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" {
		return value
	}

	// If already quoted, leave as-is
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
		(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		return value
	}

	// It's a string — wrap in quotes
	return fmt.Sprintf("%q", value)
}

// findSiblingName looks for a sibling "name:" field in the same YAML list item.
// It scans up and down from lineIdx within the same indentation block.
// Returns the name value if found (e.g., "APP_ENV"), or empty string.
func findSiblingName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}

	currentIndent := countIndent(lines[lineIdx])
	nameRegex := regexp.MustCompile(`^\s*-?\s*name:\s+(.+)$`)

	// Scan up (max 5 lines) looking for `name:` at same or parent indent
	for i := lineIdx - 1; i >= 0 && i >= lineIdx-5; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineIndent := countIndent(line)
		// If we hit something at lower indent, we've left the list item
		if lineIndent < currentIndent-2 {
			break
		}
		if m := nameRegex.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}

	// Scan down (max 3 lines)
	for i := lineIdx + 1; i < len(lines) && i <= lineIdx+3; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineIndent := countIndent(line)
		if lineIndent < currentIndent-2 {
			break
		}
		if m := nameRegex.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}

	return ""
}

// envNameToValuesKey converts a K8s env var name to a camelCase values key.
// Examples:
//   - "APP_ENV" → "appEnv"
//   - "LOG_LEVEL" → "logLevel"
//   - "DATABASE_URL" → "databaseUrl"
//   - "my-var" → "myVar"
func envNameToValuesKey(name string) string {
	// Normalize separators to spaces for word splitting
	normalized := strings.NewReplacer("_", " ", "-", " ").Replace(name)
	words := strings.Fields(normalized)

	if len(words) == 0 {
		return name
	}

	// First word lowercase, rest Title case
	result := strings.ToLower(words[0])
	for _, w := range words[1:] {
		if len(w) > 0 {
			result += strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}

	return result
}

// containsPath checks if a path slice contains a specific segment.
func containsPath(path []string, target string) bool {
	for _, p := range path {
		if p == target {
			return true
		}
	}
	return false
}

// findContainerName scans upward from lineIdx to find the container's `- name:` field.
// It looks for the nearest `- name:` at or above the `containers:` level.
func findContainerName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}

	currentIndent := countIndent(lines[lineIdx])
	nameWithDashRegex := regexp.MustCompile(`^\s*-\s+name:\s+(.+)$`)

	// Scan upward looking for `- name:` that starts a container list item
	for i := lineIdx - 1; i >= 0 && i >= lineIdx-30; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		lineIndent := countIndent(line)

		// If we hit `containers:` itself, stop
		if strings.TrimSpace(line) == "containers:" {
			break
		}

		// Look for `- name:` at a lower indent (the start of the container item)
		if lineIndent < currentIndent {
			if m := nameWithDashRegex.FindStringSubmatch(line); m != nil {
				name := strings.TrimSpace(m[1])
				// Make sure it's not an env name (env names are deeper indented)
				// Container names are at the containers list level
				return name
			}
		}
	}

	return ""
}

// extractToValuesActions suggests extracting a hardcoded value to values.yaml.
// Scope-aware (uses $.Values inside range/with) and YAML-path-aware.
func extractToValuesActions(lines []string, line, trimmed string, lineIdx int, uri string, scope TemplateScope) []protocol.CodeAction {
	var actions []protocol.CodeAction

	// Don't suggest on lines that already use {{ }}
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "}}") {
		return nil
	}

	matches := yamlKVRegex.FindStringSubmatch(line)
	if len(matches) != 4 {
		return nil
	}

	indent := matches[1]
	key := matches[2]
	value := strings.TrimSpace(matches[3])

	// Skip nested maps (value ends with ":"), comments, empty values
	if strings.HasSuffix(value, ":") || strings.HasPrefix(value, "#") || value == "" {
		return nil
	}

	// Skip K8s structural fields
	skipFields := map[string]bool{
		"apiVersion": true, "kind": true, "metadata": true, "spec": true,
		"status": true, "template": true, "data": true, "type": true,
	}
	if skipFields[key] {
		return nil
	}

	// Build YAML path: detect parent keys from indentation
	parentPath := detectYAMLPath(lines, lineIdx)

	// Smart env var detection: when extracting 'value:' inside an env list item,
	// look for a sibling 'name:' field and use it as the values key.
	// e.g. "- name: APP_ENV\n  value: production" → .Values.appEnv
	sanitizedKey := sanitizeKeyForValues(key)
	if key == "value" || key == "name" {
		if siblingName := findSiblingName(lines, lineIdx); siblingName != "" {
			sanitizedKey = envNameToValuesKey(siblingName)
		}
	}

	// Container name prefix: if inside a containers list, find the container name
	// and use it as a prefix. e.g. under `- name: web-server` → .Values.webServer.image
	containerPrefix := ""
	if containsPath(parentPath, "containers") {
		if cName := findContainerName(lines, lineIdx); cName != "" {
			containerPrefix = envNameToValuesKey(cName) // reuse camelCase converter
		}
	}

	// Build the full values path: parentPath + sanitized key
	// Keep meaningful parents (like "annotations"), filter only K8s structural ones
	structuralParents := map[string]bool{
		"metadata": true, "spec": true, "template": true, "containers": true,
		"selector": true, "matchLabels": true, "data": true, "env": true,
		"ports": true,
	}
	var cleanPath []string
	if containerPrefix != "" {
		cleanPath = append(cleanPath, containerPrefix)
	}
	for _, p := range parentPath {
		if !structuralParents[p] {
			cleanPath = append(cleanPath, p)
		}
	}
	cleanPath = append(cleanPath, sanitizedKey)

	valuesPath := strings.Join(cleanPath, ".")

	// Build the Values reference
	var valuesRef string
	if scope.RootRef == "$" {
		valuesRef = fmt.Sprintf("$.Values.%s", valuesPath)
	} else {
		valuesRef = fmt.Sprintf(".Values.%s", valuesPath)
	}

	newLine := fmt.Sprintf("%s%s: {{ %s | default %s }}", indent, key, valuesRef, quoteDefault(value))

	kind := protocol.CodeActionKindRefactorExtract

	// Helper to build a TextDocumentEdit
	makeEdit := func(newText string) protocol.TextDocumentEdit {
		return protocol.TextDocumentEdit{
			TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			},
			Edits: []interface{}{
				protocol.TextEdit{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
						End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
					},
					NewText: newText,
				},
			},
		}
	}

	// Action 1: Global extract — $.Values.xxx (same value for all iterations)
	globalTitle := fmt.Sprintf("Extract → %s (global, same for all)", valuesRef)
	actions = append(actions, protocol.CodeAction{
		Title: globalTitle,
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{makeEdit(newLine)},
		},
	})

	// Action 2: Per-key extract — $var.key (unique per loop item)
	// Only available inside range blocks with a value variable
	if scope.InRange && scope.RangeValVar != "" {
		perKeyRef := fmt.Sprintf("%s.%s", scope.RangeValVar, sanitizedKey)
		perKeyLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, perKeyRef)
		perKeyTitle := fmt.Sprintf("Extract → %s (per-item from loop)", perKeyRef)

		actions = append(actions, protocol.CodeAction{
			Title: perKeyTitle,
			Kind:  &kind,
			Edit: &protocol.WorkspaceEdit{
				DocumentChanges: []interface{}{makeEdit(perKeyLine)},
			},
		})
	}

	// For with blocks, offer access via . (rebound context)
	if scope.InWith && !scope.InRange {
		withRef := fmt.Sprintf(".%s", sanitizedKey)
		withLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, withRef)
		withTitle := fmt.Sprintf("Extract → %s (from with context)", withRef)

		actions = append(actions, protocol.CodeAction{
			Title: withTitle,
			Kind:  &kind,
			Edit: &protocol.WorkspaceEdit{
				DocumentChanges: []interface{}{makeEdit(withLine)},
			},
		})
	}

	return actions
}

// quoteWrapActions suggests adding | quote to template expressions.
func quoteWrapActions(line string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	matches := quoteRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	expr := line[matches[2]:matches[3]]
	if strings.Contains(expr, "| quote") || strings.Contains(expr, "| squote") || strings.Contains(expr, "| toYaml") {
		return nil
	}

	kind := protocol.CodeActionKindQuickFix
	insertPos := matches[4]
	quoteText := line[:insertPos] + " | quote" + line[insertPos:]

	actions = append(actions, protocol.CodeAction{
		Title: "Add | quote to template expression",
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
					TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					},
					Edits: []interface{}{
						protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
								End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
							},
							NewText: quoteText,
						},
					},
				},
			},
		},
	})

	return actions
}

// nindentActions suggests converting `| indent N` to `| nindent N`.
func nindentActions(line string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	matches := indentPipeRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	newLine := line[:matches[0]] +
		line[matches[2]:matches[3]] + "| nindent " + line[matches[4]:matches[5]] + " " + line[matches[6]:matches[7]] +
		line[matches[7]:]

	kind := protocol.CodeActionKindQuickFix
	actions = append(actions, protocol.CodeAction{
		Title: "Use nindent instead of indent",
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
					TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					},
					Edits: []interface{}{
						protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
								End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
							},
							NewText: newLine,
						},
					},
				},
			},
		},
	})

	return actions
}

// toYamlNindentActions suggests adding `| nindent N` to `toYaml` expressions without it.
func toYamlNindentActions(line string, lineIdx int, uri string) []protocol.CodeAction {
	var actions []protocol.CodeAction

	toYamlNoIndent := regexp.MustCompile(`(\{\{-?\s*toYaml\s+[^\}|]+?)(\s*-?\}\})`)
	matches := toYamlNoIndent.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}

	expr := line[matches[2]:matches[3]]
	if strings.Contains(expr, "| nindent") || strings.Contains(expr, "| indent") {
		return nil
	}

	indentLevel := countIndent(line)
	kind := protocol.CodeActionKindQuickFix
	insertPos := matches[4]
	newText := line[:insertPos] + fmt.Sprintf(" | nindent %d", indentLevel) + line[insertPos:]

	actions = append(actions, protocol.CodeAction{
		Title: fmt.Sprintf("Add | nindent %d to toYaml", indentLevel),
		Kind:  &kind,
		Edit: &protocol.WorkspaceEdit{
			DocumentChanges: []interface{}{
				protocol.TextDocumentEdit{
					TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
						TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
					},
					Edits: []interface{}{
						protocol.TextEdit{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(lineIdx), Character: 0},
								End:   protocol.Position{Line: uint32(lineIdx), Character: uint32(len(line))},
							},
							NewText: newText,
						},
					},
				},
			},
		},
	})

	return actions
}
