package server

// 通用触发规则：cron 校验、目标类型白名单与目标归属（装配层职责）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kataras/iris/v12"
	"github.com/yiiewang/workbench/internal/db"
)

func TestValidateScheduleTriggerCron(t *testing.T) {
	disabled := false
	cases := []struct {
		name string
		body scheduleTriggerBody
		ok   bool
	}{
		{"empty cron", scheduleTriggerBody{}, false},
		{"bad cron", scheduleTriggerBody{CronExpr: "not a cron"}, false},
		{"valid cron", scheduleTriggerBody{CronExpr: " 0 9 * * * "}, true},
		{"explicit disabled", scheduleTriggerBody{CronExpr: "0 9 * * 1", Enabled: &disabled}, true},
	}
	for _, c := range cases {
		b := c.body
		msg := validateScheduleTriggerCron(&b)
		if (msg == "") != c.ok {
			t.Errorf("%s: msg=%q want ok=%v", c.name, msg, c.ok)
		}
		if c.ok {
			if b.CronExpr != strings.TrimSpace(c.body.CronExpr) {
				t.Errorf("%s: cron not trimmed: %q", c.name, b.CronExpr)
			}
			if b.Enabled == nil {
				t.Errorf("%s: enabled default missing", c.name)
			}
		}
	}
	valid := scheduleTriggerBody{CronExpr: "0 9 * * *"}
	validateScheduleTriggerCron(&valid)
	if !*valid.Enabled {
		t.Fatal("enabled should default to true")
	}
	explicit := scheduleTriggerBody{CronExpr: "0 9 * * *", Enabled: &disabled}
	validateScheduleTriggerCron(&explicit)
	if *explicit.Enabled {
		t.Fatal("explicit false should be preserved")
	}
}

// 目标归属由 server 装配层校验：schedule 包不认识 pipeline，
// 但 API 必须拒绝指向别人模板 / 未注册类型 / 非法 id 的规则。
func TestValidateTriggerTarget(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "triggers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	id, err := d.CreatePipelineTemplate(context.Background(), 1, 2, "日报", `{"steps":[]}`)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{db: d, app: iris.New()}
	if err := s.app.Build(); err != nil {
		t.Fatal(err)
	}
	makeCtx := func(orgID, userID int64) iris.Context {
		ictx := s.app.ContextPool.Acquire(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		ictx.Values().Set("orgID", orgID)
		ictx.Values().Set("userID", userID)
		return ictx
	}

	cases := []struct {
		name       string
		orgID      int64
		userID     int64
		targetType string
		targetID   int64
		ok         bool
	}{
		{"owned template", 1, 2, "pipeline_template", id, true},
		{"other user", 1, 9, "pipeline_template", id, false},
		{"other org", 9, 2, "pipeline_template", id, false},
		{"unknown target type", 1, 2, "mystery", id, false},
		{"invalid id", 1, 2, "pipeline_template", 0, false},
		{"missing template", 1, 2, "pipeline_template", id + 999, false},
	}
	for _, c := range cases {
		ictx := makeCtx(c.orgID, c.userID)
		ok, err := s.validateTriggerTarget(ictx, c.targetType, c.targetID)
		s.app.ContextPool.Release(ictx)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.ok)
		}
	}
	if !supportedTriggerTargets["pipeline_template"] {
		t.Fatal("pipeline_template must be registered as a trigger target")
	}
}
