package engine

import (
	"fmt"
	"regexp"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
)

// ParseChart loads a Helm chart from a local filesystem path.
func ParseChart(chartRoot string) (*chart.Chart, error) {
	ch, err := loader.Load(chartRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart at %s: %w", chartRoot, err)
	}

	return ch, nil
}

// FindTemplatePosition searches through all templates in the chart for `define "templateName"`.
// Returns the file path (relative to chart or absolute) and the position.
func FindTemplatePosition(ch *chart.Chart, tmplName string) (string, *Position, error) {
	defineRegex := regexp.MustCompile(fmt.Sprintf(`(?m)^[ \t]*{{-?\s*define\s+"%s"\s*-?}}`, regexp.QuoteMeta(tmplName)))

	for _, t := range ch.Templates {
		content := string(t.Data)
		loc := defineRegex.FindStringIndex(content)
		if loc != nil {
			// loc[0] is the start index in the file byte array
			// We need to convert it to line and character
			lines := strings.Split(content[:loc[0]], "\n")
			lineIdx := len(lines) - 1
			charIdx := len(lines[len(lines)-1])

			// t.Name is usually "templates/_helpers.tpl"
			// To get absolute path, we could join it with chart root outside,
			// or we just return t.Name and let the caller join it.
			return t.Name, &Position{
				Line:      lineIdx,
				Character: charIdx,
			}, nil
		}
	}

	return "", nil, nil
}
