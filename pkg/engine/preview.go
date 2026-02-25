package engine

import (
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/action"
)

// RenderPreview renders a Helm chart in memory and returns only the rendered
// output for the specified template file. This is used by the Live Preview feature.
func RenderPreview(chartRoot string, uri string, content string) (string, error) {
	ch, err := ParseChart(chartRoot)
	if err != nil {
		return "", fmt.Errorf("failed to parse chart: %w", err)
	}

	// Overwrite the active template with the current editor content
	targetPath := UriToPath(uri)
	matchedTemplateName := ""
	for _, t := range ch.Templates {
		if strings.HasSuffix(targetPath, t.Name) {
			t.Data = []byte(content)
			matchedTemplateName = t.Name
			break
		}
	}

	if matchedTemplateName == "" {
		return "", fmt.Errorf("template not found in chart for URI: %s", uri)
	}

	// Load and merge values
	additionalVals, err := LoadAdditionalValues(chartRoot)
	if err != nil {
		additionalVals = map[string]interface{}{}
	}
	mergedVals, err := MergeValues(ch, additionalVals)
	if err != nil {
		return "", fmt.Errorf("failed to merge values: %w", err)
	}

	// Render
	renderEngine := action.NewInstall(&action.Configuration{})
	renderEngine.ClientOnly = true
	renderEngine.DryRun = true
	renderEngine.ReleaseName = "lsp-preview"
	renderEngine.Namespace = "default"

	rel, err := renderEngine.Run(ch, mergedVals)
	if err != nil {
		return "", fmt.Errorf("render error: %w", err)
	}

	if rel == nil || rel.Manifest == "" {
		return "# No output rendered for this template\n", nil
	}

	// Filter to only show the output from the active template
	var result strings.Builder
	docs := strings.Split(rel.Manifest, "---")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		lines := strings.SplitN(doc, "\n", 2)
		if len(lines) > 0 && strings.HasPrefix(lines[0], "# Source:") {
			sourceLine := strings.TrimSpace(lines[0])
			if !strings.HasSuffix(sourceLine, matchedTemplateName) {
				continue
			}
		}

		if result.Len() > 0 {
			result.WriteString("\n---\n")
		}
		result.WriteString(doc)
	}

	if result.Len() == 0 {
		return "# No output rendered for this template (all blocks are conditionally disabled)\n", nil
	}

	return result.String(), nil
}
