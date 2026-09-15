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

func TestLoadTTSConfigUsesEnvironmentAPIKey(t *testing.T) {
	configPath := writeTestConfig(t, `
tts:
  enabled: true
  provider: qwen
  base_url: https://dashscope.aliyuncs.com/api/v1
  timeout: 60s
`)
	t.Setenv("TTS_API_KEY", "tts-secret")

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !config.TTS.Enabled {
		t.Error("TTS.Enabled = false, want true")
	}
	if config.TTS.Provider != TTSProviderQwen {
		t.Errorf("TTS.Provider = %q, want %q", config.TTS.Provider, TTSProviderQwen)
	}
	if config.TTS.APIKey != "tts-secret" {
		t.Errorf("TTS.APIKey = %q, want environment value", config.TTS.APIKey)
	}
	if config.TTS.Timeout != 60*time.Second {
		t.Errorf("TTS.Timeout = %v, want 60s", config.TTS.Timeout)
	}
}

func TestLoadLegacyConfigUsesTTSDefaults(t *testing.T) {
	configPath := writeTestConfig(t, `app:
  name: narra
`)

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.TTS.Enabled {
		t.Error("TTS.Enabled = true, want disabled by default")
	}
	if config.TTS.Timeout != 60*time.Second {
		t.Errorf("TTS.Timeout = %v, want default 60s", config.TTS.Timeout)
	}
	if config.TTS.Model != "qwen3-tts-flash" {
		t.Errorf("TTS.Model = %q, want default model", config.TTS.Model)
	}
	// provider 故意不给默认值：它是路由决策，写错了不会当场报错。老配置里没有 tts 段时
	// 它就该是空的，一旦有人把 enabled 打开却忘了写 provider，Validate 会拦下来。
	if config.TTS.Provider != "" {
		t.Errorf("TTS.Provider = %q, want empty (no default)", config.TTS.Provider)
	}
}

func TestTTSConfigValidate(t *testing.T) {
	// 校验顺序是 provider → model → base_url → timeout。想测后面几项，前面的必须合法，
	// 所以下面每个用例都把「不该出错的那几项」写全，只有被测的那一项是坏的。
	testCases := []struct {
		name    string
		config  TTSConfig
		wantErr string
	}{
		{
			name:   "disabled configuration needs nothing",
			config: TTSConfig{},
		},
		{
			name: "enabled valid configuration",
			config: TTSConfig{
				Enabled:  true,
				Provider: TTSProviderQwen,
				BaseURL:  "https://dashscope.aliyuncs.com/api/v1",
				Model:    "qwen3-tts-flash",
				Timeout:  60 * time.Second,
			},
		},
		{
			name: "missing provider when enabled",
			config: TTSConfig{
				Enabled: true,
				BaseURL: "https://dashscope.aliyuncs.com/api/v1",
				Model:   "qwen3-tts-flash",
				Timeout: 60 * time.Second,
			},
			wantErr: "tts.provider",
		},
		{
			// 没实现的那家必须被拦下，而不是静默放行到一个不存在的客户端分支上。
			name: "unimplemented provider when enabled",
			config: TTSConfig{
				Enabled:  true,
				Provider: "azure",
				BaseURL:  "https://eastasia.tts.speech.microsoft.com",
				Model:    "qwen3-tts-flash",
				Timeout:  60 * time.Second,
			},
			wantErr: "tts.provider",
		},
		{
			name: "blank model when enabled",
			config: TTSConfig{
				Enabled:  true,
				Provider: TTSProviderQwen,
				BaseURL:  "https://dashscope.aliyuncs.com/api/v1",
				Model:    "  ",
				Timeout:  60 * time.Second,
			},
			wantErr: "tts.model",
		},
		{
			name: "missing URL when enabled does not expose API key",
			config: TTSConfig{
				Enabled:  true,
				Provider: TTSProviderQwen,
				APIKey:   "tts-secret",
				Model:    "qwen3-tts-flash",
				Timeout:  60 * time.Second,
			},
			wantErr: "tts.base_url",
		},
		{
			name: "non HTTP URL when enabled",
			config: TTSConfig{
				Enabled:  true,
				Provider: TTSProviderQwen,
				BaseURL:  "ftp://tts.example.com",
				Model:    "qwen3-tts-flash",
				Timeout:  60 * time.Second,
			},
			wantErr: "tts.base_url",
		},
		{
			name: "zero timeout when enabled",
			config: TTSConfig{
				Enabled:  true,
				Provider: TTSProviderQwen,
				BaseURL:  "https://dashscope.aliyuncs.com/api/v1",
				Model:    "qwen3-tts-flash",
			},
			wantErr: "tts.timeout",
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
