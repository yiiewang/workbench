package schedule

// 手写 cron 引擎单测：表达式解析（通配/步进/区间/列表/别名）与时刻匹配。

import (
	"testing"
	"time"
)

func mustCron(t *testing.T, expr string) *CronSpec {
	t.Helper()
	s, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	return s
}

// at 固定时区构造时刻（避免测试环境时区影响）
func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestCronParse_Errors(t *testing.T) {
	bad := []string{
		"",                       // 空串
		"* * * *",                // 4 段
		"60 * * * *",             // 分钟越界
		"* 24 * * *",             // 小时越界
		"* * 0 * *",              // 日为 0
		"* * * 13 *",             // 月越界
		"*/0 * * * *",            // 步进为 0
		"a * * * *",              // 非法值
		"5-1 * * * *",            // 区间倒置
		"1,,2 * * * *",           // 空列表项
	}
	for _, expr := range bad {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("ParseCron(%q) should fail", expr)
		}
	}
}

func TestCronMatches(t *testing.T) {
	cases := []struct {
		expr string
		t    time.Time
		want bool
	}{
		// 每 5 分钟：分钟命中 0/5/10…
		{"*/5 * * * *", at(2026, 11, 11, 10, 0), true},
		{"*/5 * * * *", at(2026, 11, 11, 10, 7), false},
		// 指定分钟列表
		{"10,20,30 * * * *", at(2026, 11, 11, 8, 20), true},
		{"10,20,30 * * * *", at(2026, 11, 11, 8, 25), false},
		// 区间 + 步进
		{"1-10/3 * * * *", at(2026, 11, 11, 8, 4), true}, // 1,4,7,10
		{"1-10/3 * * * *", at(2026, 11, 11, 8, 5), false},
		// 每天固定时刻
		{"30 9 * * *", at(2026, 11, 11, 9, 30), true},
		{"30 9 * * *", at(2026, 11, 11, 10, 30), false},
		// 月份限制
		{"* * * 11 *", at(2026, 11, 11, 0, 0), true},
		{"* * * 11 *", at(2026, 12, 11, 0, 0), false},
		// 月别名
		{"* * * nov *", at(2026, 11, 11, 0, 0), true},
		// 周别名：2026-11-11 是周三
		{"* * * * wed", at(2026, 11, 11, 0, 0), true},
		{"* * * * thu", at(2026, 11, 11, 0, 0), false},
		// 0 与 7 均为周日：2026-11-08 周日
		{"* * * * 0", at(2026, 11, 8, 0, 0), true},
		{"* * * * 7", at(2026, 11, 8, 0, 0), true},
		// 日与周均受限 → 并集语义（Vixie cron）
		{"0 0 11 * wed", at(2026, 11, 11, 0, 0), true},  // 日命中（11 号）
		{"0 0 11 * thu", at(2026, 11, 11, 0, 0), true},  // 日命中即触发（并集）
		{"0 0 12 * thu", at(2026, 11, 11, 0, 0), false}, // 日未中（12≠11）且周未中（周三≠thu）
		{"0 0 12 * wed", at(2026, 11, 11, 0, 0), true},  // 日未中但周命中（并集）
		{"0 0 * * wed", at(2026, 11, 11, 0, 0), true},   // 日通配 → 只看周
	}
	for _, c := range cases {
		s := mustCron(t, c.expr)
		if got := s.Matches(c.t); got != c.want {
			t.Errorf("%q matches %v = %v, want %v", c.expr, c.t, got, c.want)
		}
	}
}

func TestCronNext(t *testing.T) {
	s := mustCron(t, "15 8 * * *") // 每天 08:15
	next := s.Next(at(2026, 11, 11, 10, 0))
	want := at(2026, 11, 12, 8, 15)
	if !next.Equal(want) {
		t.Fatalf("Next = %v, want %v", next, want)
	}
}

func TestDaysBetween(t *testing.T) {
	if d := daysBetween("2026-11-11", "2026-11-11"); d != 0 {
		t.Errorf("same day = %d, want 0", d)
	}
	if d := daysBetween("2026-11-08", "2026-11-11"); d != 3 {
		t.Errorf("3 days = %d, want 3", d)
	}
	if d := daysBetween("bad", "2026-11-11"); d != 0 {
		t.Errorf("invalid input = %d, want 0", d)
	}
}
