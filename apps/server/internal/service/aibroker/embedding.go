package aibroker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"memstation/apps/server/internal/aicall"
	"memstation/apps/server/internal/domain"
)

func (c *Client) Embed(ctx context.Context, in domain.EmbeddingInput) (out domain.EmbeddingResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			aicall.RecordFailure(ctx, "broker_embedding_failed")
		}
	}()
	identity, err := c.ledger.EmbeddingIdentity(ctx, in.UserID, in.GenerationID, in.EmbeddingVersion, c.config.Embedding)
	if err != nil {
		return out, err
	}
	raw, headers, err := c.post(ctx, in.UserID, "embedding", "/embeddings", map[string]any{"model": c.config.Roles["embedding"], "input": in.Text, "dimensions": identity.Dimensions}, map[string]string{"X-MemStation-Embedding-Fingerprint": identity.Fingerprint, "X-MemStation-Model-Version": identity.ModelVersion})
	if err != nil {
		return out, err
	}
	var result struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			Prompt int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Data) != 1 || result.Data[0].Index != 0 || len(result.Data[0].Embedding) != identity.Dimensions || result.Model != identity.ModelVersion || headers.Get("X-MemStation-Embedding-Fingerprint") != identity.Fingerprint || headers.Get("X-MemStation-Model-Version") != identity.ModelVersion {
		return out, errors.New("broker embedding identity mismatch; generation rebuild required")
	}
	for _, v := range result.Data[0].Embedding {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return out, errors.New("invalid broker vector")
		}
	}
	return domain.EmbeddingResult{Vector: result.Data[0].Embedding, Model: "broker:" + identity.Fingerprint + ":" + strconv.Itoa(identity.Dimensions), Dimensions: identity.Dimensions, PromptTokens: result.Usage.Prompt}, nil
}

func (c *Client) Rerank(ctx context.Context, in domain.RerankInput) (out []domain.RerankResult, resultErr error) {
	defer func() {
		if resultErr != nil {
			aicall.RecordFailure(ctx, "broker_rerank_failed")
		}
	}()
	if len(in.Documents) == 0 {
		return nil, nil
	}
	// This is a versioned MemStation broker extension, not an assumed OpenAI route.
	raw, _, err := c.post(ctx, in.UserID, "rerank", "/memory/rerank/v1", map[string]any{"version": 1, "model": c.config.Roles["rerank"], "query": in.Query, "documents": in.Documents, "top_n": len(in.Documents)}, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Version int `json:"version"`
		Results []struct {
			DocumentID string   `json:"document_id"`
			Score      *float64 `json:"score"`
		} `json:"results"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Version != 1 || len(result.Results) != len(in.Documents) {
		return nil, errors.New("invalid broker rerank batch")
	}
	indexes := map[string]int{}
	for i, d := range in.Documents {
		if _, ok := indexes[d.ID]; ok {
			return nil, errors.New("duplicate rerank input")
		}
		indexes[d.ID] = i
	}
	out = []domain.RerankResult{}
	seen := map[string]bool{}
	for _, r := range result.Results {
		i, ok := indexes[r.DocumentID]
		if !ok || seen[r.DocumentID] || r.Score == nil || math.IsNaN(*r.Score) || math.IsInf(*r.Score, 0) || *r.Score < 0 || *r.Score > 1 {
			return nil, errors.New("invalid broker rerank result")
		}
		seen[r.DocumentID] = true
		out = append(out, domain.RerankResult{DocumentID: r.DocumentID, Index: i, Score: *r.Score})
	}
	return out, nil
}
