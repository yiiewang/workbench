package server

// 列表/详情分离（content 瘦身）的接口行为测试：
//   1. GET /api/tasks 列表不携带 content 全文，只带截断的 contentPreview；
//   2. GET /api/tasks/{id} 详情返回完整任务（含 content）；
//   3. PATCH 请求体 task.content 缺失 → 保留服务端现值（防快捷修改清空正文）；
//      显式传空串 → 按传入值清空（用户主动清空正文）。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yiiewang/workbench/internal/db"
)

func TestHandler_TaskListSlimAndDetail(t *testing.T) {
	e, d, secret, _ := setupTestServer(t)
	tok := createUser(t, d, secret, "org1", "alice", "p")

	longContent := strings.Repeat("正文内容段落。", 50) // 350 字，远超 80 字预览

	// 造数据：POST 带 content（revision 1 → 2）
	e.POST("/api/tasks/t1").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{
			"task":         map[string]any{"id": "t1", "title": "first", "status": "todo", "content": longContent, "sortOrder": 1},
			"baseRevision": 1,
		}).Expect().Status(http.StatusOK)

	// 1. 列表瘦身：任务对象无 content 字段、有 contentPreview（截断 + 省略号，按 rune）
	listTask := e.GET("/api/tasks").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("orgs").Object().
		Value("1").Object().Value("1").Object().Value("tasks").Array().First().Object()
	listTask.NotContainsKey("content")
	preview := listTask.Value("contentPreview").String().Raw()
	if !strings.HasPrefix(preview, "正文内容段落。") || !strings.HasSuffix(preview, "…") {
		t.Fatalf("contentPreview = %q, want truncated with ellipsis", preview)
	}
	if rc := len([]rune(preview)); rc != 81 { // 80 字 + 省略号
		t.Fatalf("contentPreview rune count = %d, want 81", rc)
	}

	// 2. 详情：完整 content
	detailTask := e.GET("/api/tasks/t1").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusOK).
		JSON().Object().Value("data").Object().Value("task").Object()
	detailTask.Value("content").Equal(longContent)
	detailTask.NotContainsKey("contentPreview")

	// 详情不存在/他人任务 → 404
	e.GET("/api/tasks/nope").WithHeader("Authorization", authHeader(tok)).
		Expect().Status(http.StatusNotFound)

	// 3. PATCH 不带 content（看板拖拽/勾选完成路径）→ 正文保留
	e.PATCH("/api/tasks/t1").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{
			"task":         map[string]any{"id": "t1", "title": "first", "status": "done", "progress": 100, "sortOrder": 1},
			"baseRevision": 2,
		}).Expect().Status(http.StatusOK)
	kept, _, _ := d.GetTaskByID(t.Context(), 1, 1, "t1")
	if kept.Content != longContent {
		t.Fatalf("content after content-less PATCH = %q, want original", kept.Content)
	}
	if kept.Status != "done" {
		t.Fatalf("status = %q, want done", kept.Status)
	}

	// 4. PATCH 显式传空串 → 主动清空
	e.PATCH("/api/tasks/t1").WithHeader("Authorization", authHeader(tok)).
		WithJSON(map[string]any{
			"task":         map[string]any{"id": "t1", "title": "first", "status": "done", "content": "", "sortOrder": 1},
			"baseRevision": 3,
		}).Expect().Status(http.StatusOK)
	cleared, _, _ := d.GetTaskByID(t.Context(), 1, 1, "t1")
	if cleared.Content != "" {
		t.Fatalf("content after explicit empty PATCH = %q, want empty", cleared.Content)
	}

	// 5. db.GetTaskByID 归属校验：别的用户查不到
	_, exists, err := d.GetTaskByID(t.Context(), 1, 999, "t1")
	if err != nil || exists {
		t.Fatalf("GetTaskByID for other user: exists=%v err=%v, want false/nil", exists, err)
	}
}

// 兼容性：空正文的任务在列表响应中 content/contentPreview 均省略（与旧版响应兼容）
func TestTaskItemJSON_OmitEmpty(t *testing.T) {
	b, err := json.Marshal(db.TaskItem{ID: "x", Title: "t", Status: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "content") || strings.Contains(s, "contentPreview") {
		t.Fatalf("empty content should be omitted, got %s", s)
	}
}
