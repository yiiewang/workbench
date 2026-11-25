package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/db"
)

// 消息级写入的请求体校验：增量提交必须每条都带 clientMsgId。
func TestValidateAISessionMessagesBody(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"valid with extras", `{"messages":[{"clientMsgId":"m1","role":"user","content":"hi","reasoning":"x"}]}`, ""},
		{"empty batch with truncate", `{"messages":[],"truncateAfter":"u1"}`, ""},
		{"missing clientMsgId", `{"messages":[{"role":"user","content":"hi"}]}`, "clientMsgId"},
		{"empty clientMsgId", `{"messages":[{"clientMsgId":"","role":"user","content":"hi"}]}`, "clientMsgId"},
		{"long clientMsgId", `{"messages":[{"clientMsgId":"` + strings.Repeat("a", 65) + `","role":"user","content":"hi"}]}`, "clientMsgId"},
		{"missing role", `{"messages":[{"clientMsgId":"m1","content":"hi"}]}`, "role"},
		{"missing content", `{"messages":[{"clientMsgId":"m1","role":"user"}]}`, "content"},
		{"not an array", `{"messages":{}}`, "JSON array"},
		{"long truncateAfter", `{"messages":[],"truncateAfter":"` + strings.Repeat("a", 65) + `"}`, "truncateAfter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b aiSessionMessagesBody
			if err := json.Unmarshal([]byte(c.body), &b); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			got := validateAISessionMessagesBody(&b)
			if c.wantErr == "" && got != "" {
				t.Fatalf("unexpected error: %s", got)
			}
			if c.wantErr != "" && !strings.Contains(got, c.wantErr) {
				t.Fatalf("want error containing %q, got %q", c.wantErr, got)
			}
		})
	}
}

// 端到端：POST 建会话 → PUT 增量追加/更新 → truncateAfter 截断 → GET 详情，
// 并覆盖鉴权、请求体校验、锚点缺失、归属隔离与旧全量路由已移除。
func TestHandler_AISessionMessages(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	ctx := context.Background()
	hash, err := HashPassword("p")
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := d.EnsureOrg(ctx, "org1")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := d.UpsertUser(ctx, orgID, "alice", hash, db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}
	auth := authHeader(GenerateToken(userID, secret, 30))

	e.POST("/api/ai/sessions").WithHeader("Authorization", auth).
		WithJSON(map[string]any{
			"title": "会话",
			"messages": []map[string]any{
				{"clientMsgId": "u1", "role": "user", "content": "问题", "reasoning": "想了一下"},
			},
		}).Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)

	list, err := d.ListAISessions(ctx, orgID, userID, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	id := list[0].ID
	base := "/api/ai/sessions/" + strconv.FormatInt(id, 10)
	msgURL := base + "/messages"

	read := func() []map[string]any {
		t.Helper()
		sess, exists, err := d.GetAISession(ctx, orgID, userID, id)
		if err != nil || !exists {
			t.Fatalf("get detail: exists=%v err=%v", exists, err)
		}
		var msgs []map[string]any
		if err := json.Unmarshal(sess.Messages, &msgs); err != nil {
			t.Fatal(err)
		}
		return msgs
	}

	e.GET(base).WithHeader("Authorization", auth).
		Expect().Status(http.StatusOK).JSON().Object().Value("code").Equal(0)

	// 只提交新增的两条，展示字段 usage 要能原样往返
	e.PUT(msgURL).WithHeader("Authorization", auth).
		WithJSON(map[string]any{"messages": []map[string]any{
			{"clientMsgId": "a1", "role": "assistant", "content": "旧回复"},
			{"clientMsgId": "a2", "role": "assistant", "content": "补充", "usage": map[string]any{"totalTokens": 10}},
		}}).Expect().Status(http.StatusOK)

	msgs := read()
	if len(msgs) != 3 || msgs[0]["clientMsgId"] != "u1" || msgs[1]["clientMsgId"] != "a1" || msgs[2]["clientMsgId"] != "a2" {
		t.Fatalf("append order mismatch: %+v", msgs)
	}
	if msgs[0]["reasoning"] != "想了一下" {
		t.Fatalf("user extras lost: %+v", msgs[0])
	}
	usage, ok := msgs[2]["usage"].(map[string]any)
	if !ok || usage["totalTokens"] != float64(10) {
		t.Fatalf("assistant extras lost: %+v", msgs[2])
	}

	// 同 clientMsgId 重提是更新，不产生重复行
	e.PUT(msgURL).WithHeader("Authorization", auth).
		WithJSON(map[string]any{"messages": []map[string]any{
			{"clientMsgId": "a2", "role": "assistant", "content": "补充v2"},
		}}).Expect().Status(http.StatusOK)
	msgs = read()
	if len(msgs) != 3 || msgs[2]["content"] != "补充v2" {
		t.Fatalf("upsert mismatch: %+v", msgs)
	}

	// regenerate：截断到 u1，删掉 a1/a2，再写入 a3
	e.PUT(msgURL).WithHeader("Authorization", auth).
		WithJSON(map[string]any{
			"messages":      []map[string]any{{"clientMsgId": "a3", "role": "assistant", "content": "新回复"}},
			"truncateAfter": "u1",
		}).Expect().Status(http.StatusOK)
	msgs = read()
	if len(msgs) != 2 || msgs[0]["clientMsgId"] != "u1" || msgs[1]["clientMsgId"] != "a3" {
		t.Fatalf("truncate mismatch: %+v", msgs)
	}

	// 缺 clientMsgId → 400；锚点不存在 → 404 且数据不变
	e.PUT(msgURL).WithHeader("Authorization", auth).
		WithJSON(map[string]any{"messages": []map[string]any{{"role": "user", "content": "x"}}}).
		Expect().Status(http.StatusBadRequest)
	e.PUT(msgURL).WithHeader("Authorization", auth).
		WithJSON(map[string]any{"messages": []any{}, "truncateAfter": "missing"}).
		Expect().Status(http.StatusNotFound)
	if got := read(); len(got) != 2 {
		t.Fatalf("failed truncate changed data: %+v", got)
	}

	// 旧全量覆盖路由已移除
	e.PUT(base).WithHeader("Authorization", auth).
		WithJSON(map[string]any{"messages": []any{}}).
		Expect().Status(http.StatusNotFound)

	// 归属隔离：同组织另一个用户不能往该会话写消息
	bobID, err := d.UpsertUser(ctx, orgID, "bob", hash, db.RoleIDUser)
	if err != nil {
		t.Fatal(err)
	}
	bobAuth := authHeader(GenerateToken(bobID, secret, 30))
	e.PUT(msgURL).WithHeader("Authorization", bobAuth).
		WithJSON(map[string]any{"messages": []map[string]any{
			{"clientMsgId": "x1", "role": "user", "content": "篡改"},
		}}).Expect().Status(http.StatusNotFound)
	if got := read(); len(got) != 2 {
		t.Fatalf("cross-user write changed data: %+v", got)
	}
	e.PUT(msgURL).WithJSON(map[string]any{"messages": []any{}}).
		Expect().Status(http.StatusUnauthorized)
}
