package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindYamlPosition(t *testing.T) {
	yamlContent := []byte(`replicaCount: 1
image:
  repository: nginx
  tag: ""
  pullPolicy: IfNotPresent

nameOverride: ""
`)

	tests := []struct {
		name      string
		path      []string
		wantLine  int
		wantChar  int
		wantFound bool
	}{
		{
			name:      "top level key",
			path:      []string{"replicaCount"},
			wantLine:  0,
			wantChar:  0,
			wantFound: true,
		},
		{
			name:      "nested key",
			path:      []string{"image", "repository"},
			wantLine:  2,
			wantChar:  2, // indented by 2 spaces
			wantFound: true,
		},
		{
			name:      "nested empty key",
			path:      []string{"image", "tag"},
			wantLine:  3,
			wantChar:  2,
			wantFound: true,
		},
		{
			name:      "not found key",
			path:      []string{"image", "missing"},
			wantFound: false,
		},
		{
			name:      "invalid path (non mapping node)",
			path:      []string{"replicaCount", "nested"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, err := FindYamlPosition(yamlContent, tt.path)
			assert.NoError(t, err)

			if tt.wantFound {
				assert.NotNil(t, pos)
				assert.Equal(t, tt.wantLine, pos.Line, "Line mismatch")
				assert.Equal(t, tt.wantChar, pos.Character, "Character mismatch")
			} else {
				assert.Nil(t, pos)
			}
		})
	}
}
