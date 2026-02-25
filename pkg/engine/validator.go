package engine

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"helm.sh/helm/v3/pkg/action"
)

// Pre-compiled regexes for hot-loop checkers (avoid recompiling per line)
var (
	singleBraceVarRegex = regexp.MustCompile(`(?:^|[^{])\{-?\s+\$`)
	singleBraceDotRegex = regexp.MustCompile(`(?:^|[^{])\{-?\s+\.`)
	undefVarRegex       = regexp.MustCompile(`undefined variable "(\$.*?)"`)
)

// ValidateChart performs a virtual `helm template` render in memory.
// It parses the Helm errors (if any) and converts them to LSP Diagnostics.
func ValidateChart(chartRoot string, uri string, content string) ([]protocol.Diagnostic, error) {
	// We want to validate the chart with the current unsaved file content.
	// We parse the chart from disk, but overwrite the active template in memory.
	ch, err := ParseChart(chartRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to parse chart for validation: %w", err)
	}

	// Find the matching template in the loaded chart and overwrite it with active Editor content.
	// This makes validation "live" as you type, without needing to save the file.
	targetPath := UriToPath(uri)
	matchedTemplateName := ""
	for _, t := range ch.Templates {
		// Helm template names are usually "templates/file.yaml"
		// If the absolute path ends with the template name, it's a match.
		if strings.HasSuffix(targetPath, t.Name) {
			t.Data = []byte(content)
			matchedTemplateName = t.Name
			break
		}
	}

	// We load values including any auxiliary files (values.1.yaml, etc)
	additionalVals, err := LoadAdditionalValues(chartRoot)
	if err != nil {
		additionalVals = map[string]interface{}{}
	}
	mergedVals, err := MergeValues(ch, additionalVals)
	if err != nil {
		return nil, err
	}

	// Actually RENDER the chart in memory (Virtual Render)
	// We use the same engine `helm template` uses internally.
	renderEngine := action.NewInstall(&action.Configuration{})
	renderEngine.ClientOnly = true
	renderEngine.DryRun = true
	renderEngine.ReleaseName = "lsp-preview"
	renderEngine.Namespace = "default"

	rel, err := renderEngine.Run(ch, mergedVals)

	var diagnostics []protocol.Diagnostic

	// Pre-render: scan for broken template brackets (single { where {{ expected)
	// This catches typos like `{ include ...` or `{- $var :=` that Helm happily renders as raw text
	bracketDiags := checkBrokenBrackets(content)
	diagnostics = append(diagnostics, bracketDiags...)

	// Pre-render: scan for common K8s field name typos in the raw template text
	// This catches `ind:` instead of `kind:`, `namespacexx:` instead of `namespace:`, etc.
	// Works even when the template block is conditionally disabled and doesn't render.
	fieldDiags := checkK8sFieldNames(content)
	diagnostics = append(diagnostics, fieldDiags...)

	// If there's an error during rendering, parse it and highlight the line!
	if err != nil {
		diags := parseHelmError(err.Error(), uri, content)
		if len(diags) > 0 {
			diagnostics = append(diagnostics, diags...)
		}
	} else if rel != nil && rel.Manifest != "" {
		// Rendering succeeded! Next, run OpenAPI schema validation on the manifest output.
		log.Printf("Schema validator: render succeeded, validating manifest for %s", matchedTemplateName)
		schemaDiags := ValidateSchematic(rel.Manifest, content, matchedTemplateName)
		diagnostics = append(diagnostics, schemaDiags...)
	}

	return diagnostics, nil
}

// UriToPath converts a file:// URI back to an absolute local path.
func UriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

// parseHelmError tries to extract the line number and message from Helm's engine errors.
// Example: "parse error at (test-chart/templates/deployment.yaml:22): unclosed action"
// Example: "execution error at (test-chart/templates/deployment.yaml:14:18): function "foobar" not defined"
func parseHelmError(errMsg string, uri string, content string) []protocol.Diagnostic {
	// We only care about errors for the current active file
	targetPath := UriToPath(uri)
	fileName := ""
	parts := strings.Split(targetPath, "/")
	if len(parts) > 0 {
		fileName = parts[len(parts)-1]
	}

	if fileName == "" {
		return nil
	}

	// Regex 1: standard Helm template execution/parse error: (path/to/file.yaml:LINE:COL) or (path/to/file.yaml:LINE)
	errRegex := regexp.MustCompile(fmt.Sprintf(`\((.*?%s):(\d+)(?::(\d+))?\):\s*(.*)`, regexp.QuoteMeta(fileName)))
	matches := errRegex.FindStringSubmatch(errMsg)

	// Regex 2: YAML parse error: YAML parse error on path/to/file.yaml: error ... yaml: line LINE: ...
	yamlRegex := regexp.MustCompile(fmt.Sprintf(`YAML parse error on .*?%s:\s*.*?[yY]aml:\s*line\s*(\d+):\s*(.*)`, regexp.QuoteMeta(fileName)))
	yamlMatches := yamlRegex.FindStringSubmatch(errMsg)

	if len(yamlMatches) > 0 {
		lineStr := yamlMatches[1]
		msg := yamlMatches[2]

		lineNum, err := strconv.Atoi(lineStr)
		if err != nil || lineNum < 1 {
			lineNum = 1
		}

		lineIdx := uint32(lineNum - 1)
		severity := protocol.DiagnosticSeverityError
		source := "helm-yaml"
		return []protocol.Diagnostic{
			{
				Range: protocol.Range{
					Start: protocol.Position{Line: lineIdx, Character: 0},
					End:   protocol.Position{Line: lineIdx, Character: 500},
				},
				Severity: &severity,
				Source:   &source,
				Message:  msg,
			},
		}
	} else if len(matches) > 0 {
		lineStr := matches[2]
		msg := matches[4]

		lineNum, err := strconv.Atoi(lineStr)
		if err != nil || lineNum < 1 {
			lineNum = 1
		}

		// LSP is 0-indexed, Helm errors are 1-indexed
		lineIdx := uint32(lineNum - 1)

		lines := strings.Split(content, "\n")
		// "unclosed action" or "unexpected EOF" usually points to EOF because text/template parses till the end.
		// Let's try to find the actual unclosed `{{` tag by scanning backwards.
		if (strings.Contains(msg, "unclosed action") || strings.Contains(msg, "unexpected EOF")) && int(lineIdx) >= len(lines)-2 {
			openTags := 0
			// scan from start of file to find the unclosed tag
			for i := 0; i < len(lines); i++ {
				// extremely simple heuristic: count {{ vs }}
				opens := strings.Count(lines[i], "{{")
				closes := strings.Count(lines[i], "}}")
				openTags += (opens - closes)
				if openTags > 0 {
					lineIdx = uint32(i)
					break
				}
			}
		}

		var diagnostics []protocol.Diagnostic

		// If it's an "undefined variable" error, the user might have made a typo during assignment (-{ instead of {{-).
		// We can scan the document for the variable assignment and throw a hint.
		if strings.Contains(msg, "undefined variable") {
			if m := undefVarRegex.FindStringSubmatch(msg); len(m) > 1 {
				varName := m[1]
				for i, line := range lines {
					if strings.Contains(line, varName) && strings.Contains(line, ":=") && !strings.Contains(line, "{{") {
						warnSeverity := protocol.DiagnosticSeverityWarning
						warnSource := "helm-lsp-hint"
						warnMsg := fmt.Sprintf("Possible typo: variable %s is assigned here but missing '{{' brackets", varName)
						diagnostics = append(diagnostics, protocol.Diagnostic{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(i), Character: 0},
								End:   protocol.Position{Line: uint32(i), Character: 500},
							},
							Severity: &warnSeverity,
							Source:   &warnSource,
							Message:  warnMsg,
						})
					}
				}
			}
		}

		severity := protocol.DiagnosticSeverityError
		source := "helm-template"
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{Line: lineIdx, Character: 0},
				End:   protocol.Position{Line: lineIdx, Character: 500}, // Highlight the whole line roughly
			},
			Severity: &severity,
			Source:   &source,
			Message:  msg,
		})

		return diagnostics
	}

	// Fallback if we couldn't parse the line number, just show the error at the top of the file
	severity := protocol.DiagnosticSeverityError
	source := "helm-template"
	return []protocol.Diagnostic{
		{
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 0},
			},
			Severity: &severity,
			Source:   &source,
			Message:  errMsg,
		},
	}
}

// checkBrokenBrackets scans template source lines for single-brace patterns
// that look like broken Go template syntax: e.g. `{- end` or `{ include`.
// These are silently treated as raw text by text/template but almost always indicate a typo.
func checkBrokenBrackets(content string) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic
	lines := strings.Split(content, "\n")

	// Match lines that have `{-` or `{ ` followed by template keywords,
	// but NOT preceded by another `{` (i.e., not `{{`)
	templateKeywords := []string{"if", "else", "end", "range", "with", "define", "template", "include", "block"}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for opening single brace: `{-` or `{ keyword`
		for _, kw := range templateKeywords {
			// Pattern: single { followed by - or space, then a keyword
			// But NOT preceded by { (negative lookbehind equivalent)
			patterns := []string{
				"{- " + kw,
				"{ " + kw,
			}
			for _, pat := range patterns {
				idx := strings.Index(trimmed, pat)
				if idx >= 0 {
					// Make sure it's NOT preceded by another { (which would make it {{)
					if idx == 0 || trimmed[idx-1] != '{' {
						severity := protocol.DiagnosticSeverityWarning
						source := "helm-syntax"
						msg := fmt.Sprintf("Possible broken template bracket: found '%s' — did you mean '{{- %s'?", pat, kw)
						diagnostics = append(diagnostics, protocol.Diagnostic{
							Range: protocol.Range{
								Start: protocol.Position{Line: uint32(i), Character: 0},
								End:   protocol.Position{Line: uint32(i), Character: 500},
							},
							Severity: &severity,
							Source:   &source,
							Message:  msg,
						})
					}
				}
			}
		}

		// Check for single-brace variable assignment: `{- $var :=`
		if singleBraceVarRegex.MatchString(trimmed) {
			if !strings.Contains(trimmed, "{{") {
				severity := protocol.DiagnosticSeverityWarning
				source := "helm-syntax"
				msg := "Possible broken template bracket: variable assignment with single '{' — did you mean '{{'?"
				diagnostics = append(diagnostics, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: 500},
					},
					Severity: &severity,
					Source:   &source,
					Message:  msg,
				})
			}
		}

		// Check for single-brace dot-expression: `{ .field` or `{- .field`
		// This catches `replicas: { .replicas | default 1 }}`
		if singleBraceDotRegex.MatchString(trimmed) {
			if !strings.Contains(trimmed, "{{") || strings.Count(trimmed, "{")-strings.Count(trimmed, "{{") > 0 {
				// There's a single { followed by a dot — likely a broken template expression
				severity := protocol.DiagnosticSeverityWarning
				source := "helm-syntax"
				msg := "Possible broken template bracket: found '{ .expr' — did you mean '{{ .expr'?"
				diagnostics = append(diagnostics, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: 500},
					},
					Severity: &severity,
					Source:   &source,
					Message:  msg,
				})
			}
		}
	}

	return diagnostics
}

// checkK8sFieldNames scans raw template text for YAML keys that look like
// misspelled Kubernetes field names. This works even when template blocks don't render.
func checkK8sFieldNames(content string) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic
	lines := strings.Split(content, "\n")

	// Well-known top-level and common nested K8s field names
	knownFields := []string{
		"apiVersion", "kind", "metadata", "spec", "status",
		"name", "namespace", "labels", "annotations",
		"replicas", "selector", "template", "containers",
		"ports", "env", "envFrom", "volumeMounts", "volumes",
		"image", "imagePullPolicy", "command", "args",
		"resources", "limits", "requests",
		"matchLabels", "matchExpressions",
		"strategy", "type", "rollingUpdate",
		"restartPolicy", "serviceAccountName", "serviceAccount",
		"nodeSelector", "tolerations", "affinity",
		"initContainers", "hostNetwork", "dnsPolicy",
		"schedule", "concurrencyPolicy", "jobTemplate",
		"successfulJobsHistoryLimit", "failedJobsHistoryLimit",
		"backoffLimit", "activeDeadlineSeconds", "ttlSecondsAfterFinished",
		"data", "binaryData", "stringData",
		"rules", "tls", "ingressClassName",
		"clusterIP", "externalTrafficPolicy", "sessionAffinity",
		"targetPort", "protocol", "appProtocol",
		"readinessProbe", "livenessProbe", "startupProbe",
		"configMapKeyRef", "secretKeyRef", "fieldRef",
		"persistentVolumeClaim", "claimName", "storageClassName",
		"accessModes", "storage",
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines, comments, and template-only lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "{{") {
			continue
		}

		// Extract the YAML key (everything before the first `:`)
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx <= 0 {
			continue
		}

		key := strings.TrimSpace(trimmed[:colonIdx])
		// Skip keys that contain template expressions (they're dynamic)
		if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
			continue
		}
		// Skip keys with dashes (likely custom labels/annotations values, not K8s fields)
		if strings.Contains(key, "/") {
			continue
		}
		// Skip if key starts with - (YAML list item)
		if strings.HasPrefix(key, "-") {
			key = strings.TrimSpace(strings.TrimPrefix(key, "-"))
			if key == "" {
				continue
			}
		}

		// Check against known fields using edit distance
		for _, known := range knownFields {
			if key == known {
				break // Exact match, no typo
			}
			dist := levenshtein(key, known)
			// Only flag if edit distance is 1-2 AND the key is long enough to not be a false positive
			if dist >= 1 && dist <= 2 && len(key) >= 4 && len(known) >= 4 {
				severity := protocol.DiagnosticSeverityWarning
				source := "k8s-field-hint"
				msg := fmt.Sprintf("Possible K8s field typo: '%s' — did you mean '%s'?", key, known)
				diagnostics = append(diagnostics, protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: uint32(colonIdx)},
					},
					Severity: &severity,
					Source:   &source,
					Message:  msg,
				})
				break // Only report the closest match
			}
		}
	}

	return diagnostics
}

// levenshtein calculates the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	matrix := make([][]int, la+1)
	for i := range matrix {
		matrix[i] = make([]int, lb+1)
		matrix[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min3(
				matrix[i-1][j]+1,
				matrix[i][j-1]+1,
				matrix[i-1][j-1]+cost,
			)
		}
	}
	return matrix[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
