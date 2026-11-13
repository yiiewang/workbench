// Package rag RAG 语义检索：文件索引（切分 → 向量化 → vec0 存储）与在线检索。
// 离线侧由 server 的写钩子（上传/AI 写文件/fsops）触发单文件索引，
// 在线侧在 AI 对话前置检索，命中片段拼进上下文。
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/config"
)

// embedBatchSize 单次 /embeddings 请求最大文本条数（批太大会撞网关 body 上限）
const embedBatchSize = 16

// embedHTTPTimeout 单次 embedding 请求超时
const embedHTTPTimeout = 60 * time.Second

// Embedder OpenAI 兼容 /embeddings 客户端（vLLM / Ollama / 商用网关均可）。
// key 可为空（本地部署无鉴权）。
type Embedder struct {
	endpoint string // 完整 /embeddings URL
	apiKey   string
	model    string
	dim      int // Matryoshka 降维目标，>0 时随请求下发 dimensions 参数
	client   *http.Client
}

// NewEmbedder 由配置构造。base_url 兼容两种写法：网关根（…/v1，自动追加
// /embeddings）或完整端点（…/v1/embeddings，原样使用）——与聊天网关同一套约定。
func NewEmbedder(cfg config.EmbeddingConfig) *Embedder {
	u := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if !strings.HasSuffix(u, "/embeddings") {
		u += "/embeddings"
	}
	return &Embedder{
		endpoint: u,
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		dim:      cfg.Dim,
		client:   &http.Client{Timeout: embedHTTPTimeout},
	}
}

// embedResponse OpenAI 兼容响应（只解析需要的字段）
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 批量向量化：内部按 embedBatchSize 分批，返回顺序与输入一致。
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed batch [%d:%d]: %w", start, end, err)
		}
		out = append(out, batch...)
	}
	return out, nil
}

// embedBatch 单批次请求。响应按 index 归位，不依赖服务端返回顺序。
func (e *Embedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	payload := map[string]any{
		"model": e.model,
		"input": texts,
	}
	if e.dim > 0 {
		payload["dimensions"] = e.dim
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding upstream %s: %s", resp.Status, truncate(string(respBody), 200))
	}

	var out embedResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: want %d got %d", len(texts), len(out.Data))
	}
	vecs := make([][]float32, len(texts))
	for _, item := range out.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("embedding index out of range: %d", item.Index)
		}
		switch {
		case len(item.Embedding) < e.dim:
			return nil, fmt.Errorf("embedding dim mismatch: want %d got %d", e.dim, len(item.Embedding))
		case len(item.Embedding) > e.dim:
			// 网关不支持 dimensions 参数（返回原生维度，如 Qwen3-Embedding 的 4096）：
			// 按 Matryoshka 语义取前 dim 维——但截断后范数 < 1，须重归一化，
			// 否则 L2 距离与余弦相似度不再单调等价，KNN 排序失真。
			vecs[item.Index] = truncateNormalize(item.Embedding, e.dim)
		default:
			vecs[item.Index] = item.Embedding
		}
	}
	return vecs, nil
}

// truncateNormalize Matryoshka 截断：取前 dim 维并 L2 重归一化
func truncateNormalize(v []float32, dim int) []float32 {
	out := make([]float32, dim)
	copy(out, v[:dim])
	var norm float64
	for _, x := range out {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return out
	}
	inv := 1 / math.Sqrt(norm)
	for i := range out {
		out[i] = float32(float64(out[i]) * inv)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
