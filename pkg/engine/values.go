package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

// MergeValues coalesces the default chart values with any additional user-supplied values.
// This also handles merging subchart values according to Helm's built-in resolution rules.
func MergeValues(ch *chart.Chart, userValues map[string]interface{}) (chartutil.Values, error) {
	err := chartutil.ProcessDependencies(ch, userValues)
	if err != nil {
		return nil, fmt.Errorf("failed to process chart dependencies: %w", err)
	}

	rawVals, err := chartutil.CoalesceValues(ch, userValues)
	if err != nil {
		return nil, fmt.Errorf("failed to coalesce values: %w", err)
	}

	return rawVals, nil
}

// LoadAdditionalValues reads all values*.y*ml files in the chart root (except the default values.yaml)
// and merges them into a single map to simulate Helm's -f overriding.
func LoadAdditionalValues(chartRoot string) (map[string]interface{}, error) {
	merged := make(map[string]interface{})

	files, err := filepath.Glob(filepath.Join(chartRoot, "values*.y*ml"))
	if err != nil {
		return merged, err
	}

	// Sort files to ensure deterministic merging (e.g. values.1.yaml before values.50.yaml)
	sort.Strings(files)

	for _, file := range files {
		// Skip default values.yaml because the chart loader already includes it
		if filepath.Base(file) == "values.yaml" || filepath.Base(file) == "values.yml" {
			continue
		}

		vals, err := chartutil.ReadValuesFile(file)
		if err == nil {
			// Merge into existing map
			merged = coalesceMaps(merged, vals)
		}
	}

	return merged, nil
}

// coalesceMaps does a shallow/deep merge of b into a, similar to Helm's merge.
// Helm uses dest/source logic.
func coalesceMaps(dest map[string]interface{}, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		if destVal, ok := dest[k]; ok {
			if destMap, okDest := destVal.(map[string]interface{}); okDest {
				if srcMap, okSrc := v.(map[string]interface{}); okSrc {
					dest[k] = coalesceMaps(destMap, srcMap)
					continue
				}
			}
		}
		dest[k] = v
	}
	return dest
}
