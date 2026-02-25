package templatehandler

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	lsp "go.lsp.dev/protocol"
)

// CodeActionKinds supported by this handler
var CodeActionKinds = []lsp.CodeActionKind{
	lsp.QuickFix,
	lsp.RefactorExtract,
	lsp.Source,
}

var (
	yamlKVRegex      = regexp.MustCompile(`^(\s*(?:-\s+)?)([\w\-\.\/]+):\s+(.+)$`)
	quoteRegex       = regexp.MustCompile(`(\{\{-?\s*(?:\$\.|\.)[^\}]+?)(\s*-?\}\})`)
	indentPipeRegex  = regexp.MustCompile(`(\{\{-?\s*(?:include|template)\s+"[^"]+"\s+[^\}]+)\|\s*indent\s+(\d+)\s*(-?\}\})`)
	rangeBlockRegex  = regexp.MustCompile(`\{\{-?\s*range\b`)
	rangeVarsRegex   = regexp.MustCompile(`range\s+(\$\w+)\s*,\s*(\$\w+)\s*:=`)
	rangeSimpleRegex = regexp.MustCompile(`range\s+(\$\w+)\s*:=`)
	withBlockRegex   = regexp.MustCompile(`\{\{-?\s*with\b`)
	endBlockRegex    = regexp.MustCompile(`\{\{-?\s*end\s*-?\}\}`)
	defineBlockRegex = regexp.MustCompile(`\{\{-?\s*define\b`)
	yamlKeyOnlyRegex = regexp.MustCompile(`^(\s*)([\w\-\.\/]+):\s*$`)
)

func (h *TemplateHandler) CodeAction(ctx context.Context, params *lsp.CodeActionParams) (result []lsp.CodeAction, err error) {
	doc, ok := h.documents.GetTemplateDoc(params.TextDocument.URI)
	if !ok {
		return nil, nil
	}

	content := string(doc.Content)
	return getCodeActions(content, params.Range, string(params.TextDocument.URI)), nil
}

func getCodeActions(content string, rng lsp.Range, uri string) []lsp.CodeAction {
	var actions []lsp.CodeAction
	lines := strings.Split(content, "\n")
	lineIdx := int(rng.Start.Line)

	if lineIdx >= len(lines) {
		return actions
	}

	line := lines[lineIdx]
	trimmed := strings.TrimSpace(line)

	scope := detectScope(lines, lineIdx)

	actions = append(actions, extractToValuesActions(lines, line, trimmed, lineIdx, uri, scope)...)
	actions = append(actions, quoteWrapActions(line, lineIdx, uri)...)
	actions = append(actions, nindentActions(line, lineIdx, uri)...)
	actions = append(actions, toYamlNindentActions(line, lineIdx, uri)...)

	return actions
}

type TemplateScope struct {
	InRange     bool
	InWith      bool
	InDefine    bool
	Depth       int
	RootRef     string
	RangeKeyVar string
	RangeValVar string
	WithVar     string
}

func detectScope(lines []string, lineIdx int) TemplateScope {
	scope := TemplateScope{RootRef: "."}
	depth := 0

	for i := lineIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}

		endCount := len(endBlockRegex.FindAllString(trimmed, -1))
		depth += endCount

		if rangeBlockRegex.MatchString(trimmed) {
			if depth > 0 {
				depth--
			} else {
				scope.InRange = true
				scope.Depth++
				scope.RootRef = "$"
				if m := rangeVarsRegex.FindStringSubmatch(trimmed); m != nil {
					scope.RangeKeyVar = m[1]
					scope.RangeValVar = m[2]
				} else if m := rangeSimpleRegex.FindStringSubmatch(trimmed); m != nil {
					scope.RangeValVar = m[1]
				}
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
	return scope
}

func countIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func DetectYAMLPath(lines []string, lineIdx int) []string {
	if lineIdx >= len(lines) {
		return nil
	}
	currentIndent := countIndent(lines[lineIdx])
	var path []string
	targetIndent := currentIndent

	for i := lineIdx - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || strings.HasPrefix(trimmed, "{{") || strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			continue
		}
		lineIndent := countIndent(line)
		if lineIndent < targetIndent {
			m := yamlKeyOnlyRegex.FindStringSubmatch(line)
			if m != nil {
				path = append([]string{m[2]}, path...)
				targetIndent = lineIndent
			}
			if lineIndent == 0 {
				break
			}
		}
	}
	return path
}

func sanitizeKeyForValues(key string) string {
	if !strings.ContainsAny(key, "./-") {
		return key
	}
	normalized := strings.NewReplacer("/", ".", "-", ".").Replace(key)
	parts := strings.Split(normalized, ".")
	prefixes := map[string]bool{"kubernetes": true, "io": true, "cert": true, "manager": true, "k8s": true, "nginx": true, "app": true}
	startIdx := 0
	for startIdx < len(parts)-1 && prefixes[parts[startIdx]] {
		startIdx++
	}
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

func quoteDefault(value string) string {
	isNumeric := regexp.MustCompile(`^\d+(\.\d+)?$`).MatchString(value)
	if isNumeric {
		return value
	}
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" {
		return value
	}
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) || (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		return value
	}
	return fmt.Sprintf("%q", value)
}

func findSiblingName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}
	startIdx, endIdx, listItemIndent := lineIdx, lineIdx, -1
	for i := lineIdx; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			startIdx = i
			listItemIndent = countIndent(lines[i])
			break
		}
	}
	if listItemIndent == -1 {
		return ""
	}
	for i := startIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if countIndent(lines[i]) <= listItemIndent {
			endIdx = i
			break
		}
		endIdx = i
	}
	if endIdx == startIdx && endIdx < len(lines)-1 {
		endIdx = len(lines)
	}
	nameRegex := regexp.MustCompile(`^\s*-?\s*name:\s+(.+)$`)
	for i := startIdx; i < endIdx && i < len(lines); i++ {
		if m := nameRegex.FindStringSubmatch(lines[i]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func envNameToValuesKey(name string) string {
	if strings.HasPrefix(strings.TrimSpace(name), "{{") {
		m := regexp.MustCompile(`default\s+"([^"]+)"`).FindStringSubmatch(name)
		if len(m) > 1 {
			name = m[1]
		} else {
			m = regexp.MustCompile(`\.Values\.([a-zA-Z0-9_]+)`).FindStringSubmatch(name)
			if len(m) > 1 {
				name = m[1]
			} else {
				name = regexp.MustCompile(`[\{\}\|"'\s]`).ReplaceAllString(name, "")
			}
		}
	}
	name = strings.Trim(name, `"'`)
	normalized := strings.NewReplacer("_", " ", "-", " ").Replace(name)
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return name
	}
	result := strings.ToLower(words[0])
	for _, w := range words[1:] {
		if len(w) > 0 {
			result += strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return result
}

func containsPath(path []string, target string) bool {
	for _, p := range path {
		if p == target {
			return true
		}
	}
	return false
}

func findContainerName(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}
	currentIndent := countIndent(lines[lineIdx])
	nameWithDashRegex := regexp.MustCompile(`^\s*-\s+name:\s+(.+)$`)
	lastSeenName := ""
	lastSeenIndent := 999
	for i := lineIdx - 1; i >= 0 && i >= lineIdx-50; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lineIndent := countIndent(line)
		if strings.TrimSpace(line) == "containers:" {
			return lastSeenName
		}
		if lineIndent < currentIndent && nameWithDashRegex.MatchString(line) {
			if lineIndent < lastSeenIndent {
				lastSeenName = strings.TrimSpace(nameWithDashRegex.FindStringSubmatch(line)[1])
				lastSeenIndent = lineIndent
			}
		}
	}
	return ""
}

func makeEdit(uri string, lineIdx int, oldLineLen int, newText string) *lsp.WorkspaceEdit {
	return &lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			lsp.DocumentURI(uri): {
				{
					Range: lsp.Range{
						Start: lsp.Position{Line: uint32(lineIdx), Character: 0},
						End:   lsp.Position{Line: uint32(lineIdx), Character: uint32(oldLineLen)},
					},
					NewText: newText,
				},
			},
		},
	}
}

func extractToValuesActions(lines []string, line, trimmed string, lineIdx int, uri string, scope TemplateScope) []lsp.CodeAction {
	var actions []lsp.CodeAction
	if strings.Contains(trimmed, "{{") || strings.Contains(trimmed, "}}") {
		return nil
	}
	matches := yamlKVRegex.FindStringSubmatch(line)
	if len(matches) != 4 {
		return nil
	}
	indent, key, value := matches[1], matches[2], strings.TrimSpace(matches[3])
	if strings.HasSuffix(value, ":") || strings.HasPrefix(value, "#") || value == "" {
		return nil
	}
	skipFields := map[string]bool{"apiVersion": true, "kind": true, "metadata": true, "spec": true, "status": true, "template": true, "data": true, "type": true, "name": true}
	if skipFields[key] {
		return nil
	}

	parentPath := DetectYAMLPath(lines, lineIdx)
	sanitizedKey := sanitizeKeyForValues(key)
	if key == "value" {
		if siblingName := findSiblingName(lines, lineIdx); siblingName != "" {
			sanitizedKey = envNameToValuesKey(siblingName)
		}
	}
	containerPrefix := ""
	if containsPath(parentPath, "containers") {
		if cName := findContainerName(lines, lineIdx); cName != "" {
			containerPrefix = envNameToValuesKey(cName)
		}
	}

	structuralParents := map[string]bool{"metadata": true, "spec": true, "template": true, "containers": true, "selector": true, "matchLabels": true, "data": true, "env": true, "ports": true}
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

	valuesRef := fmt.Sprintf(".Values.%s", valuesPath)
	if scope.RootRef == "$" {
		valuesRef = fmt.Sprintf("$.Values.%s", valuesPath)
	}
	newLine := fmt.Sprintf("%s%s: {{ %s | default %s }}", indent, key, valuesRef, quoteDefault(value))

	kind := lsp.RefactorExtract
	actions = append(actions, lsp.CodeAction{
		Title: fmt.Sprintf("Extract → %s (global, same for all)", valuesRef),
		Kind:  kind,
		Edit:  makeEdit(uri, lineIdx, len(line), newLine),
	})

	if scope.InRange && scope.RangeValVar != "" {
		perKeyRef := fmt.Sprintf("%s.%s", scope.RangeValVar, sanitizedKey)
		perKeyLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, perKeyRef)
		actions = append(actions, lsp.CodeAction{
			Title: fmt.Sprintf("Extract → %s (per-item from loop)", perKeyRef),
			Kind:  kind,
			Edit:  makeEdit(uri, lineIdx, len(line), perKeyLine),
		})
	}

	if scope.InWith && !scope.InRange {
		withRef := fmt.Sprintf(".%s", sanitizedKey)
		withLine := fmt.Sprintf("%s%s: {{ %s }}", indent, key, withRef)
		actions = append(actions, lsp.CodeAction{
			Title: fmt.Sprintf("Extract → %s (from with context)", withRef),
			Kind:  kind,
			Edit:  makeEdit(uri, lineIdx, len(line), withLine),
		})
	}
	return actions
}

func quoteWrapActions(line string, lineIdx int, uri string) []lsp.CodeAction {
	var actions []lsp.CodeAction
	matches := quoteRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}
	expr := line[matches[2]:matches[3]]
	if strings.Contains(expr, "| quote") || strings.Contains(expr, "| squote") || strings.Contains(expr, "| toYaml") {
		return nil
	}
	kind := lsp.QuickFix
	insertPos := matches[4]
	newText := line[:insertPos] + " | quote" + line[insertPos:]
	actions = append(actions, lsp.CodeAction{
		Title: "Add | quote to template expression",
		Kind:  kind,
		Edit:  makeEdit(uri, lineIdx, len(line), newText),
	})
	return actions
}

func nindentActions(line string, lineIdx int, uri string) []lsp.CodeAction {
	var actions []lsp.CodeAction
	matches := indentPipeRegex.FindStringSubmatchIndex(line)
	if matches == nil {
		return nil
	}
	newLine := line[:matches[0]] + line[matches[2]:matches[3]] + "| nindent " + line[matches[4]:matches[5]] + " " + line[matches[6]:matches[7]] + line[matches[7]:]
	kind := lsp.QuickFix
	actions = append(actions, lsp.CodeAction{
		Title: "Use nindent instead of indent",
		Kind:  kind,
		Edit:  makeEdit(uri, lineIdx, len(line), newLine),
	})
	return actions
}

func toYamlNindentActions(line string, lineIdx int, uri string) []lsp.CodeAction {
	var actions []lsp.CodeAction
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
	kind := lsp.QuickFix
	insertPos := matches[4]
	newText := line[:insertPos] + fmt.Sprintf(" | nindent %d", indentLevel) + line[insertPos:]
	actions = append(actions, lsp.CodeAction{
		Title: fmt.Sprintf("Add | nindent %d to toYaml", indentLevel),
		Kind:  kind,
		Edit:  makeEdit(uri, lineIdx, len(line), newText),
	})
	return actions
}
