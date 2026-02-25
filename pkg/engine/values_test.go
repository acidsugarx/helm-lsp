package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

func TestMergeValues(t *testing.T) {
	// Create a dummy chart with default values
	ch := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "mychart",
			Version: "1.0.0",
		},
		Values: map[string]interface{}{
			"replicaCount": float64(1),
			"image": map[string]interface{}{
				"repository": "nginx",
				"tag":        "latest",
				"pullPolicy": "IfNotPresent",
			},
		},
	}

	// Create user-supplied values that override some defaults
	userValues := map[string]interface{}{
		"replicaCount": float64(3),
		"image": map[string]interface{}{
			"tag": "1.19.0",
		},
		"extra": "newKey",
	}

	expectedResult := chartutil.Values{
		"replicaCount": float64(3),
		"image": map[string]interface{}{
			"repository": "nginx",
			"tag":        "1.19.0",
			"pullPolicy": "IfNotPresent",
		},
		"extra": "newKey",
	}

	merged, err := MergeValues(ch, userValues)
	assert.NoError(t, err)

	// Since CoalesceValues wraps everything in a map where the top level keys are
	// global values and chart values, we should check if our merged map matches
	// actually chartutil.CoalesceValues returns values under the chart name namespace
	// NO, wait, CoalesceValues returns the raw coalesced values but it might not namespace if top level

	// Let's verify our specific user values are merged
	replicaCount, ok := merged["replicaCount"]
	assert.True(t, ok)
	assert.Equal(t, float64(3), replicaCount)

	imageRaw, ok := merged["image"]
	assert.True(t, ok)
	imageMap, ok := imageRaw.(map[string]interface{})
	assert.True(t, ok)

	assert.Equal(t, "nginx", imageMap["repository"])
	assert.Equal(t, "1.19.0", imageMap["tag"])
	assert.Equal(t, "IfNotPresent", imageMap["pullPolicy"])

	assert.Equal(t, "newKey", merged["extra"])

	// The actual map comparison might fail if chartutil adds some hidden fields (like global)
	// So checking specific fields is safer.
	_ = expectedResult
}
