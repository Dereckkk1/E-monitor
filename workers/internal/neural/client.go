package neural

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// Client communicates with the CLAP neural verifier sidecar.
type Client struct {
	url  string
	http *http.Client
}

// New creates a new Client pointing at the given base URL (e.g. "http://clap-verifier:8080").
func New(url string) *Client {
	return &Client{url: url, http: &http.Client{Timeout: 5 * time.Second}}
}

type embedRequest struct {
	PCMB64 string `json:"pcm_b64"`
	SR     int    `json:"sr"`
}

type embedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed sends PCM float32 samples to the clap-verifier and returns the embedding vector.
// The samples are serialised as raw IEEE-754 little-endian bytes, then base64-encoded.
func (c *Client) Embed(ctx context.Context, pcm []float32) ([]float64, error) {
	// Convert []float32 to raw little-endian bytes without heap allocations per sample.
	raw := make([]byte, len(pcm)*4)
	for i, s := range pcm {
		bits := math.Float32bits(s)
		raw[i*4] = byte(bits)
		raw[i*4+1] = byte(bits >> 8)
		raw[i*4+2] = byte(bits >> 16)
		raw[i*4+3] = byte(bits >> 24)
	}

	b64 := base64.StdEncoding.EncodeToString(raw)
	body, err := json.Marshal(embedRequest{PCMB64: b64, SR: 16000})
	if err != nil {
		return nil, fmt.Errorf("neural: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("neural: status %d", resp.StatusCode)
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embedding, nil
}

// CosineSimilarity returns the cosine similarity between two equal-length vectors.
// Returns 0 for mismatched lengths, empty inputs, or zero-magnitude vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
