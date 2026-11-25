package db

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// 回归测试：会话改行级存储后的 CRUD 与追加语义。
// 旧版 GET 详情把 TEXT Scan 进 json.RawMessage 曾 500；现在从
// ai_session_messages 组装，顺带验证展示字段与 clientMsgId 往返不丢。
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

	id, err := d.CreateAISession(t.Context(), orgID, uid, "test",
		`[{"role":"user","content":"hi","reasoning":"想了一下"}]`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	readMessages := func() []map[string]any {
		t.Helper()
		sess, exists, err := d.GetAISession(t.Context(), orgID, uid, id)
		if err != nil || !exists {
			t.Fatalf("get detail: exists=%v err=%v", exists, err)
		}
		var msgs []map[string]any
		if err := json.Unmarshal(sess.Messages, &msgs); err != nil {
			t.Fatalf("unmarshal messages: %v", err)
		}
		return msgs
	}

	msgs := readMessages()
	if len(msgs) != 1 || msgs[0]["role"] != "user" || msgs[0]["content"] != "hi" {
		t.Fatalf("messages mismatch: %+v", msgs)
	}
	if msgs[0]["clientMsgId"] != "idx:0" || msgs[0]["reasoning"] != "想了一下" {
		t.Fatalf("clientMsgId / extras lost: %+v", msgs[0])
	}

	list, err := d.ListAISessions(t.Context(), orgID, uid, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Title != "test" {
		t.Fatalf("list mismatch: %+v", list)
	}

	// 追加语义：提交空批次不删除已有消息
	if err := d.UpsertAISessionMessages(t.Context(), orgID, uid, id, `[]`, ""); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := readMessages(); len(got) != 1 {
		t.Fatalf("append-only violated: %+v", got)
	}

	// 带 clientMsgId 的新消息插入；同 id 重提是更新而非重复行
	reply := `[{"clientMsgId":"m2","role":"assistant","content":"hello"}]`
	if err := d.UpsertAISessionMessages(t.Context(), orgID, uid, id, reply, ""); err != nil {
		t.Fatalf("append: %v", err)
	}
	msgs = readMessages()
	if len(msgs) != 2 || msgs[1]["clientMsgId"] != "m2" || msgs[1]["content"] != "hello" {
		t.Fatalf("append mismatch: %+v", msgs)
	}
	reply = `[{"clientMsgId":"m2","role":"assistant","content":"hello v2"}]`
	if err := d.UpsertAISessionMessages(t.Context(), orgID, uid, id, reply, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	msgs = readMessages()
	if len(msgs) != 2 || msgs[1]["content"] != "hello v2" {
		t.Fatalf("upsert mismatch: %+v", msgs)
	}

	if err := d.DeleteAISession(t.Context(), orgID, uid, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, exists, _ := d.GetAISession(t.Context(), orgID, uid, id); exists {
		t.Fatal("session should be deleted")
	}
	// 子表无外键，删除会话必须显式清理，不能留孤儿消息
	var left int
	if err := d.conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM ai_session_messages WHERE session_id = ?`, id).Scan(&left); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if left != 0 {
		t.Fatalf("orphan messages left: %d", left)
	}
}

// 消息级写入：只提交增量、按 clientMsgId 更新、truncateAfter 截断旧回复。
func TestAISessionMessageUpsert(t *testing.T) {
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
	ctx := t.Context()

	id, err := d.CreateAISession(ctx, orgID, uid, "batch",
		`[{"clientMsgId":"u1","role":"user","content":"问题"}]`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	read := func() []map[string]any {
		t.Helper()
		sess, exists, err := d.GetAISession(ctx, orgID, uid, id)
		if err != nil || !exists {
			t.Fatalf("get detail: exists=%v err=%v", exists, err)
		}
		var msgs []map[string]any
		if err := json.Unmarshal(sess.Messages, &msgs); err != nil {
			t.Fatalf("unmarshal messages: %v", err)
		}
		return msgs
	}

	// 两次独立提交，只带本次变动的消息
	if err := d.UpsertAISessionMessages(ctx, orgID, uid, id,
		`[{"clientMsgId":"a1","role":"assistant","content":"旧回复","usage":{"totalTokens":10}}]`, ""); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if err := d.UpsertAISessionMessages(ctx, orgID, uid, id,
		`[{"clientMsgId":"a2","role":"assistant","content":"补充"}]`, ""); err != nil {
		t.Fatalf("append a2: %v", err)
	}
	msgs := read()
	if len(msgs) != 3 || msgs[0]["clientMsgId"] != "u1" || msgs[1]["clientMsgId"] != "a1" || msgs[2]["clientMsgId"] != "a2" {
		t.Fatalf("append order mismatch: %+v", msgs)
	}
	usage, ok := msgs[1]["usage"].(map[string]any)
	if !ok || usage["totalTokens"] != float64(10) {
		t.Fatalf("extras lost: %+v", msgs[1])
	}

	// regenerate：保留到 u1，删除其后的 a1/a2，再写入新回复
	if err := d.UpsertAISessionMessages(ctx, orgID, uid, id,
		`[{"clientMsgId":"a3","role":"assistant","content":"新回复"}]`, "u1"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	msgs = read()
	if len(msgs) != 2 || msgs[0]["clientMsgId"] != "u1" || msgs[1]["clientMsgId"] != "a3" {
		t.Fatalf("truncate mismatch: %+v", msgs)
	}

	// 锚点不存在必须报错，且不能误删已有消息
	if err := d.UpsertAISessionMessages(ctx, orgID, uid, id, `[]`, "missing"); err == nil {
		t.Fatal("expected error for missing truncate anchor")
	}
	if got := read(); len(got) != 2 {
		t.Fatalf("failed truncate changed data: %+v", got)
	}
}
