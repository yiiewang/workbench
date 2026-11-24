// Package mcpclient 是 MCP Streamable HTTP 的最小客户端：
// initialize / notifications/initialized / tools/list / tools/call。
//
// 只支持无状态 HTTP 服务：每个请求独立 POST，不维护会话生命周期（无重连、
// 无保活、无子进程管理）；initialize 返回的 Mcp-Session-Id 仅原样透传。
// 响应兼容 application/json 与 text/event-stream（取第一条 data 消息）。
package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion  = "2025-06-18"
	maxResponseBytes = 1 << 20
	requestTimeout   = 30 * time.Second
)

// Tool MCP 工具描述（tools/list 返回项）
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// Client 单个 MCP 服务的客户端；并发安全（id 自增与 session 透传）。
type Client struct {
	name    string
	url     string
	headers map[string]string
	http    *http.Client
	mu      sync.Mutex
	nextID  int64
	session string
}

// New 构造客户端（headers 的值在每次请求时用 os.ExpandEnv 展开）。
func New(name, url string, headers map[string]string) *Client {
	return &Client{
		name:    name,
		url:     url,
		headers: headers,
		http:    &http.Client{Timeout: requestTimeout},
		nextID:  1, // id=0 会被 omitempty 省略，退化成通知
	}
}

// ListTools 握手后拉取工具列表；服务端不可达或协议错误直接返回。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var initialized json.RawMessage
	if err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "workbench", "version": "1.0"},
	}, &initialized); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	// notifications/initialized 是通知（无 id），服务端通常回 202 且无响应体
	_ = c.notify(ctx, "notifications/initialized")

	var listed struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.rpc(ctx, "tools/list", map[string]any{}, &listed); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return listed.Tools, nil
}

// CallTool 调用工具并返回文本内容；isError=true 时作为错误返回。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	var res callResult
	if err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", err
	}
	var text strings.Builder
	for _, item := range res.Content {
		if item.Type == "text" {
			text.WriteString(item.Text)
		}
	}
	if res.IsError {
		return "", fmt.Errorf("mcp %s tool %s: %s", c.name, name, text.String())
	}
	return text.String(), nil
}

// rpc 发送带 id 的 JSON-RPC 请求，解析 result 到 out（out 可为 nil）。
func (c *Client) rpc(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.session = sid
		c.mu.Unlock()
	}
	data, err := readResponse(resp)
	if err != nil {
		return err
	}

	var parsed rpcResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("mcp %s: invalid JSON-RPC response: %w", c.name, err)
	}
	if parsed.Error != nil {
		return fmt.Errorf("mcp %s %s: %s (code %d)", c.name, method, parsed.Error.Message, parsed.Error.Code)
	}
	if out != nil && len(parsed.Result) > 0 {
		return json.Unmarshal(parsed.Result, out)
	}
	return nil
}

// notify 发送无 id 的通知，不解析响应体。
func (c *Client) notify(ctx context.Context, method string) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method})
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, body)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	return resp.Body.Close()
}

// send 组装请求头并发出 POST；返回的响应体由调用方关闭。
func (c *Client) send(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	for k, v := range c.headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", c.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("mcp %s: HTTP %d", c.name, resp.StatusCode)
	}
	return resp, nil
}

// readResponse 读取并关闭响应体：JSON 原样返回，SSE 取第一条 data 消息。
func readResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return bytes.TrimSpace(raw), nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if len(data) > 0 && string(data) != "[DONE]" {
			return data, nil
		}
	}
	return nil, fmt.Errorf("mcp: empty SSE response")
}
