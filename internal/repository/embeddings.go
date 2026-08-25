package repository

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// EmbeddingProvider is optional. Indexing and lexical queries never depend on
// it, and a provider failure leaves deterministic data usable.
type EmbeddingProvider interface {
	Model() string
	Embed(context.Context, []string) ([][]float32, error)
}

type OpenAICompatibleEmbeddings struct {
	Endpoint  string
	ModelName string
	APIKey    string
	Client    *http.Client
}

func (p OpenAICompatibleEmbeddings) Model() string { return p.ModelName }

func (p OpenAICompatibleEmbeddings) Embed(ctx context.Context, input []string) ([][]float32, error) {
	if p.Endpoint == "" || p.ModelName == "" || p.APIKey == "" {
		return nil, errors.New("semantic embeddings are not configured")
	}
	body, err := json.Marshal(map[string]any{"model": p.ModelName, "input": input})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.Endpoint, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+p.APIKey)
	request.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned %s", response.Status)
	}
	var decoded struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Data) != len(input) {
		return nil, errors.New("embedding endpoint returned an incomplete batch")
	}
	vectors := make([][]float32, len(input))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(vectors) || len(item.Embedding) == 0 {
			return nil, errors.New("embedding endpoint returned an invalid vector")
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(data[index*4:], math.Float32bits(value))
	}
	return data
}

func decodeVector(data []byte, dimensions int) ([]float32, bool) {
	if dimensions <= 0 || len(data) != dimensions*4 {
		return nil, false
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[index*4:]))
	}
	return vector, true
}

func cosine(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return 0
	}
	var dot, leftSize, rightSize float64
	for index := range left {
		dot += float64(left[index] * right[index])
		leftSize += float64(left[index] * left[index])
		rightSize += float64(right[index] * right[index])
	}
	if leftSize == 0 || rightSize == 0 {
		return 0
	}
	return dot / math.Sqrt(leftSize*rightSize)
}
