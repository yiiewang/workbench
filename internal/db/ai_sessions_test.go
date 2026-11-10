package db

import (
	"path/filepath"
	"testing"
)

// 回归测试：ai_sessions CRUD（尤其详情接口的 TEXT → json.RawMessage Scan
// 需经 string 中转，直接 Scan 曾报 "unsupported Scan ... *json.RawMessage"
// 导致 GET /api/ai/sessions/{id} 500）
func TestAISessionCRUD(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	orgID, err := d.EnsureOrg(t.Context(), "cm")
	if err != nil {
		t.Fatalf("ensure org: %v", err)
	}
	uid, err := d.UpsertUser(t.Context(), orgID, "u1", "x", 2)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}

	id, err := d.CreateAISession(t.Context(), orgID, uid, "test", `[{"role":"user","content":"hi"}]`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sess, exists, err := d.GetAISession(t.Context(), orgID, uid, id)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if !exists {
		t.Fatal("session not found")
	}
	if string(sess.Messages) != `[{"role":"user","content":"hi"}]` {
		t.Fatalf("messages mismatch: %s", string(sess.Messages))
	}

	list, err := d.ListAISessions(t.Context(), orgID, uid, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Title != "test" {
		t.Fatalf("list mismatch: %+v", list)
	}

	if err := d.UpdateAISession(t.Context(), orgID, uid, id, "t2", `[]`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := d.DeleteAISession(t.Context(), orgID, uid, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, exists, _ := d.GetAISession(t.Context(), orgID, uid, id); exists {
		t.Fatal("session should be deleted")
	}
}
