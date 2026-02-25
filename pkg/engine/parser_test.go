package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseChart(t *testing.T) {
	// Create a temporary valid chart
	tempDir := t.TempDir()
	chartDir := filepath.Join(tempDir, "mychart")
	err := os.Mkdir(chartDir, 0755)
	if err != nil {
		t.Fatalf("failed to create chart dir: %v", err)
	}

	chartYaml := []byte(`apiVersion: v2
name: mychart
description: A Helm chart for testing
version: 0.1.0
appVersion: "1.16.0"
`)
	err = os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), chartYaml, 0644)
	if err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid chart directory",
			path:    chartDir,
			wantErr: false,
		},
		{
			name:    "invalid path",
			path:    filepath.Join(tempDir, "nonexistent"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := ParseChart(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseChart() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && ch == nil {
				t.Errorf("ParseChart() returned nil chart for valid input")
			}
			if !tt.wantErr && ch != nil && ch.Metadata.Name != "mychart" {
				t.Errorf("expected chart name 'mychart', got '%s'", ch.Metadata.Name)
			}
		})
	}
}
