package db

import (
	"strings"
	"testing"
)

// 列表摘要生成：短正文不下发（避免列表携带详情内容），长正文按 rune 截断 + 省略号
func TestMakeContentPreview(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空正文", "", ""},
		{"短中文不下发", "买牛奶", ""},
		{"恰好 24 字不下发", strings.Repeat("字", 24), ""},
		{"25 字截断", strings.Repeat("字", 25), strings.Repeat("字", 24) + "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := makeContentPreview(c.in, taskPreviewLen); got != c.want {
				t.Fatalf("makeContentPreview(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
