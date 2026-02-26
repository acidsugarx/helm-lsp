package languagefeatures

import (
	"testing"
)

func TestFormatHelmYAML(t *testing.T) {
	tests := []struct {
		name    string
		content string
		enabled bool
		want    string
	}{
		{
			name:    "Disabled formatter returns content unmodified",
			content: "foo:  bar\n  baz: qux\n",
			enabled: false,
			want:    "foo:  bar\n  baz: qux\n",
		},
		{
			name: "Basic indentation correction",
			content: `foo:
    bar:
baz: qux`,
			enabled: true,
			want: `foo:
  bar:
baz: qux`,
		},
		{
			name: "Array formatting",
			content: `list:
- item1:
    value1: 1
- item2:
    value2: 2`,
			enabled: true,
			want: `list:
- item1:
  value1: 1
- item2:
  value2: 2`,
		},
		{
			name:    "Preserve nindent and indent",
			content: "foo:\n  {{- include \"helper\" . | nindent 4 }}\n  bar: \n    {{- toYaml .Values.bar | indent 4 }}",
			enabled: true,
			want:    "foo:\n  {{- include \"helper\" . | nindent 4 }}\n  bar:\n    {{- toYaml .Values.bar | indent 4 }}",
		},
		{
			name: "Align pure template blocks",
			content: `foo:
  {{ if .Values.enabled }}
  bar:
  {{ end }}`,
			enabled: true,
			want: `foo:
  {{ if .Values.enabled }}
  bar:
    {{ end }}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatHelmYAML(tt.content, tt.enabled); got != tt.want {
				t.Errorf("FormatHelmYAML() =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}
