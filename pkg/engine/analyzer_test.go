package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWordAtPosition(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		charIndex int
		want      string
	}{
		{
			name:      "cursor inside word",
			line:      "{{ .Values.image.repository }}",
			charIndex: 12, // inside '.Values'
			want:      ".Values.image.repository",
		},
		{
			name:      "cursor at end of word",
			line:      "{{ .Values.image.repository }}",
			charIndex: 27, // right after 'repository'
			want:      ".Values.image.repository",
		},
		{
			name:      "cursor on whitespace",
			line:      "{{ .Values.image.repository }}",
			charIndex: 2, // on space before .Values
			want:      "",
		},
		{
			name:      "cursor inside incomplete typing with dot",
			line:      "{{ .Values.image. }}",
			charIndex: 17,              // right after '.image.'
			want:      ".Values.image", // trailing dot should be trimmed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WordAtPosition(tt.line, tt.charIndex)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractValuesPath(t *testing.T) {
	tests := []struct {
		name     string
		word     string
		wantPath []string
		wantOk   bool
	}{
		{
			name:     "valid values path",
			word:     ".Values.abc.def",
			wantPath: []string{"abc", "def"},
			wantOk:   true,
		},
		{
			name:     "just values",
			word:     ".Values.",
			wantPath: nil,
			wantOk:   true,
		},
		{
			name:     "not a values path",
			word:     ".Release.Name",
			wantPath: nil,
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, ok := ExtractValuesPath(tt.word)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}
