// Package rag 内 LLM 辅助调用：相关性预判（judge）与文件摘要（digest）
// 共用一套 OpenAI 兼容 /chat/completions 非流式客户端，均复用 embedding
// 网关的 base_url/api_key（同一网关通常同时提供 /embeddings 与对话端点）。
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/config"
)

// llmHTTPTimeout 单次 LLM 辅助调用超时（judge 含在 Search 的 8s 总预算内；
// digest 在单文件索引的 5min 预算内）
const llmHTTPTimeout = 20 * time.Second

// chatClient OpenAI 兼容非流式对话客户端
type chatClient struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// newChatClient 由 embedding 配置推导对话端点（剥掉尾部 /embeddings 后接
// /chat/completions）；model 为空返回 nil（对应功能关闭）
func newChatClient(cfg config.EmbeddingConfig, model string) *chatClient {
	if model == "" {
		return nil
	}
	u := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	u = strings.TrimSuffix(u, "/embeddings")
	if !strings.HasSuffix(u, "/chat/completions") {
		u += "/chat/completions"
	}
	return &chatClient{
		endpoint: u,
		apiKey:   cfg.APIKey,
		model:    model,
		client:   &http.Client{Timeout: llmHTTPTimeout},
	}
}

// complete 单轮补全：返回首个回复文本
func (c *chatClient) complete(ctx context.Context, prompt string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model":       c.model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm upstream %s: %s", resp.Status, truncateRunes(string(body), 200))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	return out.Choices[0].Message.Content, nil
}

// ============================================================
// LLM 相关性预判（judge）
// ============================================================

// judgeSnippetRunes 预判 prompt 中每个片段的截断长度（控制调用成本）
const judgeSnippetRunes = 300

// RelevanceJudge LLM 相关性预判器：距离阈值只能挡"语义远离"的命中，
// 中文 embedding 空间各向异性下无关命中常落在阈值内——由 LLM 批量判断
// 候选片段与问题的实际相关性。一次调用判完全部候选（结构化输出相关编号），
// 失败/超时降级保留原命中（预判是锦上添花，不能拖垮对话可用性）。
type RelevanceJudge struct {
	cc *chatClient
}

func newRelevanceJudge(cfg config.EmbeddingConfig) *RelevanceJudge {
	cc := newChatClient(cfg, cfg.JudgeModel)
	if cc == nil {
		return nil
	}
	return &RelevanceJudge{cc: cc}
}

// judgeJSON 提取响应中的 JSON 对象（模型可能带 markdown 代码块等包装）
var judgeJSON = regexp.MustCompile(`\{[^{}]*\}`)

// Filter 批量过滤候选命中：一次调用返回相关编号，按编号保留命中。
// 任何失败（网络/解析）一律返回原 candidates（降级）；
// 模型明确判定全不相关时返回空切片（这是有效结论，不是失败）。
func (j *RelevanceJudge) Filter(ctx context.Context, query string, candidates []Hit) []Hit {
	if len(candidates) == 0 {
		return candidates
	}
	var b strings.Builder
	b.WriteString("判断下列编号文本片段与用户问题的相关性。")
	b.WriteString("标准：片段包含能回答问题的实质内容才算相关；仅主题相近、词面重合但答非所问的不算。\n")
	b.WriteString("用户问题：")
	b.WriteString(query)
	b.WriteString("\n")
	for i, h := range candidates {
		fmt.Fprintf(&b, "\n[%d] %s\n%s\n", i+1, h.Path, truncateRunes(h.Content, judgeSnippetRunes))
	}
	b.WriteString(`\n只输出 JSON，不要其他内容：{"relevant":[相关编号列表]}，如 {"relevant":[1,3]}；全不相关输出 {"relevant":[]}`)

	content, err := j.cc.complete(ctx, b.String())
	if err != nil {
		return candidates // 超时/网络失败：降级
	}

	// 提取 {"relevant":[...]}（模型输出可能裹 markdown 代码块或多余文字）
	m := judgeJSON.FindString(content)
	if m == "" {
		return candidates
	}
	var verdict struct {
		Relevant []int `json:"relevant"`
	}
	if err := json.Unmarshal([]byte(m), &verdict); err != nil {
		return candidates
	}

	keep := make(map[int]bool)
	for _, n := range verdict.Relevant {
		if n >= 1 && n <= len(candidates) {
			keep[n-1] = true
		}
	}
	filtered := make([]Hit, 0, len(candidates))
	for i, h := range candidates {
		if keep[i] {
			filtered = append(filtered, h)
		}
	}
	return filtered
}

// ============================================================
// 文件级摘要（digest）
// ============================================================

// digestInputRunes 摘要输入截断（文件正文喂给 LLM 的上限，控制成本；
// 摘要只需把握主旨，不需要全文）
const digestInputRunes = 3000

// Digester 文件级摘要生成器：索引时为每个文件生成语义摘要卡（是什么/
// 核心内容/关键点），作为特殊块与原文块一起向量化入库。
type Digester struct {
	cc *chatClient
}

func newDigester(cfg config.EmbeddingConfig) *Digester {
	cc := newChatClient(cfg, cfg.DigestModel)
	if cc == nil {
		return nil
	}
	return &Digester{cc: cc}
}

// Generate 生成文件摘要（一段话，80~150 字）。失败由调用方降级。
func (d *Digester) Generate(ctx context.Context, path, text string) (string, error) {
	var b strings.Builder
	b.WriteString("为下列文件生成语义摘要卡，将用于语义检索（用户会用自然语言问题搜到它）。\n")
	b.WriteString("要求：一段话（80~150 字），说明这是什么、核心内容或功能、关键技术点/主题词；")
	b.WriteString("不要寒暄、不要列表、不要复述文件名，直接输出摘要内容。\n")
	b.WriteString("文件路径：")
	b.WriteString(path)
	b.WriteString("\n文件内容（可能截断）：\n")
	b.WriteString(truncateRunes(text, digestInputRunes))

	content, err := d.cc.complete(ctx, b.String())
	if err != nil {
		return "", err
	}
	// 模型可能裹 markdown/引号：剥掉首尾空白与引号
	digest := strings.TrimSpace(content)
	digest = strings.Trim(digest, "`\"'“”")
	if digest == "" {
		return "", fmt.Errorf("empty digest")
	}
	return digest, nil
}

// truncateRunes rune 数截断（LLM prompt 瘦身）
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
