package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadEmbeddingConfigUsesEnvironmentAPIKey(t *testing.T) {
	configPath := writeTestConfig(t, `
embedding:
  enabled: true
  base_url: http://localhost:8000/v1
  model: BAAI/bge-m3
  timeout: 30s
  dimensions: 1024
`)
	t.Setenv("EMBEDDING_API_KEY", "embedding-secret")

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !config.Embedding.Enabled {
		t.Error("Embedding.Enabled = false, want true")
	}
	if config.Embedding.APIKey != "embedding-secret" {
		t.Errorf("Embedding.APIKey = %q, want environment value", config.Embedding.APIKey)
	}
	if config.Embedding.Timeout != 30*time.Second {
		t.Errorf("Embedding.Timeout = %v, want 30s", config.Embedding.Timeout)
	}
}

func TestLoadLegacyConfigUsesEmbeddingDefaults(t *testing.T) {
	configPath := writeTestConfig(t, `app:
  name: narra
`)

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Embedding.Model != "BAAI/bge-m3" {
		t.Errorf("Embedding.Model = %q, want default model", config.Embedding.Model)
	}
	if config.Embedding.Dimensions != BGEM3Dimensions {
		t.Errorf("Embedding.Dimensions = %d, want %d", config.Embedding.Dimensions, BGEM3Dimensions)
	}
	if config.Embedding.Enabled {
		t.Error("Embedding.Enabled = true, want disabled by default")
	}
}

func TestEmbeddingConfigValidate(t *testing.T) {
	validConfig := EmbeddingConfig{
		Model:      "BAAI/bge-m3",
		Dimensions: BGEM3Dimensions,
	}

	testCases := []struct {
		name    string
		config  EmbeddingConfig
		wantErr string
	}{
		{
			name:   "disabled configuration only needs model and dimensions",
			config: validConfig,
		},
		{
			name: "enabled valid configuration",
			config: EmbeddingConfig{
				Enabled:    true,
				BaseURL:    "https://embedding.example.com/v1",
				Model:      "BAAI/bge-m3",
				Timeout:    30 * time.Second,
				Dimensions: BGEM3Dimensions,
			},
		},
		{
			name:    "missing model",
			config:  EmbeddingConfig{Dimensions: BGEM3Dimensions},
			wantErr: "embedding.model",
		},
		{
			name: "invalid dimensions do not expose API key",
			config: EmbeddingConfig{
				APIKey:     "embedding-secret",
				Model:      "BAAI/bge-m3",
				Dimensions: BGEM3Dimensions - 1,
			},
			wantErr: "embedding.dimensions",
		},
		{
			name: "missing URL when enabled",
			config: EmbeddingConfig{
				Enabled:    true,
				Model:      "BAAI/bge-m3",
				Timeout:    30 * time.Second,
				Dimensions: BGEM3Dimensions,
			},
			wantErr: "embedding.base_url",
		},
		{
			name: "non HTTP URL when enabled",
			config: EmbeddingConfig{
				Enabled:    true,
				BaseURL:    "ftp://embedding.example.com",
				Model:      "BAAI/bge-m3",
				Timeout:    30 * time.Second,
				Dimensions: BGEM3Dimensions,
			},
			wantErr: "embedding.base_url",
		},
		{
			name: "zero timeout when enabled",
			config: EmbeddingConfig{
				Enabled:    true,
				BaseURL:    "http://localhost:8000",
				Model:      "BAAI/bge-m3",
				Dimensions: BGEM3Dimensions,
			},
			wantErr: "embedding.timeout",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Errorf("Validate() error = %q, want it to contain %q", err, testCase.wantErr)
			}
			if testCase.config.APIKey != "" && strings.Contains(err.Error(), testCase.config.APIKey) {
				t.Errorf("Validate() error leaked API key: %q", err)
			}
		})
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return configPath
}
