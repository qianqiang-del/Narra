package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"narra/pkg/config"
)

func TestEmbedReturnsVectorsInInputOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer embedding-secret" {
			t.Errorf("Authorization = %q, want API key", authorization)
		}

		var body embeddingsRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "BAAI/bge-m3" || len(body.Input) != 2 || body.Input[0] != "first" || body.Input[1] != "second" {
			t.Errorf("unexpected request body: %#v", body)
		}

		_ = json.NewEncoder(response).Encode(embeddingsResponse{Data: []embeddingData{
			{Index: 1, Embedding: []float32{2, 2, 2}},
			{Index: 0, Embedding: []float32{1, 1, 1}},
		}})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/v1", 3)
	vectors, err := client.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if vectors[0][0] != 1 || vectors[1][0] != 2 {
		t.Errorf("vectors = %#v, want input order", vectors)
	}
}

func TestEmbedRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(embeddingsResponse{Data: []embeddingData{
			{Index: 0, Embedding: []float32{1, 2}},
		}})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 3)
	_, err := client.Embed(context.Background(), []string{"text"})
	if err == nil || !strings.Contains(err.Error(), "want 3") {
		t.Fatalf("Embed() error = %v, want dimension validation error", err)
	}
}

func TestEmbedRejectsEmptyInputBeforeRequest(t *testing.T) {
	client := &Client{httpClient: &http.Client{Timeout: time.Second}}
	_, err := client.Embed(context.Background(), []string{""})
	if err == nil || !strings.Contains(err.Error(), "index 0") {
		t.Fatalf("Embed() error = %v, want empty-input validation error", err)
	}
}

func newTestClient(t *testing.T, baseURL string, dimensions int) *Client {
	t.Helper()

	client, err := NewClient(config.EmbeddingConfig{
		Enabled:    true,
		APIKey:     "embedding-secret",
		BaseURL:    baseURL,
		Model:      "BAAI/bge-m3",
		Timeout:    time.Second,
		Dimensions: config.BGEM3Dimensions,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.dimensions = dimensions
	return client
}
