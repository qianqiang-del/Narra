package discussion

import (
	"context"
	"errors"
	"testing"
)

type contractStreamingModel struct{}

func (contractStreamingModel) Generate(context.Context, GenerationRequest) (GenerationResponse, error) {
	return GenerationResponse{}, nil
}

func (contractStreamingModel) GenerateStream(context.Context, GenerationRequest) (<-chan GenerationChunk, error) {
	chunks := make(chan GenerationChunk, 2)
	chunks <- GenerationChunk{Delta: "第一段"}
	chunks <- GenerationChunk{Delta: "第二段", NextAction: "end", InputTokens: 7, OutputTokens: 9, Done: true}
	close(chunks)
	return chunks, nil
}

var _ StreamingModel = contractStreamingModel{}

func TestStreamingModelContractPreservesChunksAndFinalMetadata(t *testing.T) {
	stream, err := (contractStreamingModel{}).GenerateStream(context.Background(), GenerationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []GenerationChunk
	for chunk := range stream {
		got = append(got, chunk)
	}
	if len(got) != 2 || got[0].Delta+got[1].Delta != "第一段第二段" {
		t.Fatalf("chunks = %#v, want ordered deltas", got)
	}
	if !got[1].Done || got[1].NextAction != "end" || got[1].InputTokens != 7 || got[1].OutputTokens != 9 {
		t.Fatalf("final chunk = %#v, want final metadata", got[1])
	}
}

func TestStreamingModelContractCarriesErrors(t *testing.T) {
	want := errors.New("stream interrupted")
	stream := make(chan GenerationChunk, 1)
	stream <- GenerationChunk{Err: want}
	close(stream)
	chunk := <-stream
	if !errors.Is(chunk.Err, want) {
		t.Fatalf("chunk error = %v, want %v", chunk.Err, want)
	}
}
