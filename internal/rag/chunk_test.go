package rag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIndexable(t *testing.T) {
	cases := []struct {
		name string
		size int64
		want bool
	}{
		{"notes.md", 100, true},
		{"app.go", 100, true},
		{"photo.png", 100, false},   // 非文本
		{"README.MD", 100, true},    // 大小写不敏感
		{"huge.md", 3 << 20, false}, // 超 2MB
		{"edge.md", 2 << 20, true},  // 恰好 2MB
		{"noext", 100, false},       // 无扩展名
		{".env", 100, false},        // 隐藏文件不索引
		{".gitignore", 100, false},  // 隐藏文件不索引
	}
	for _, c := range cases {
		if got := Indexable(c.name, c.size); got != c.want {
			t.Errorf("Indexable(%q, %d) = %v, want %v", c.name, c.size, got, c.want)
		}
	}
}

func TestSplitChunks_ShortText(t *testing.T) {
	// 短文本：单块原样保留段落结构，行号从首段起算
	in := "第一段内容\n第二行\n\n第二段内容"
	got := SplitChunks(in)
	if len(got) != 1 {
		t.Fatalf("short text should be 1 chunk, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "第一段内容\n第二行") || !strings.Contains(got[0].Content, "第二段内容") {
		t.Fatalf("paragraph structure lost: %q", got[0].Content)
	}
	if got[0].StartLine != 1 {
		t.Fatalf("start line = %d, want 1", got[0].StartLine)
	}
}

func TestSplitChunks_LongText(t *testing.T) {
	// 多段落长文本：块数 >1、每块 ≤ target+overlap、相邻块有重叠、行号单调递增
	var paras []string
	for i := 0; i < 30; i++ {
		paras = append(paras, strings.Repeat("段", 100)+string(rune('A'+i%26)))
	}
	in := strings.Join(paras, "\n\n")
	got := SplitChunks(in)

	if len(got) < 2 {
		t.Fatalf("long text should split into multiple chunks, got %d", len(got))
	}
	for i, c := range got {
		n := utf8.RuneCountInString(c.Content)
		if n > chunkTargetRunes+chunkOverlapRunes {
			t.Errorf("chunk %d too long: %d runes", i, n)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].StartLine <= got[i-1].StartLine {
			t.Errorf("start line not increasing: chunk %d line %d, chunk %d line %d",
				i-1, got[i-1].StartLine, i, got[i].StartLine)
		}
		// 相邻块重叠：后一块开头应出现在前一块尾部
		tail := tailRunes(got[i-1].Content, chunkOverlapRunes)
		if !strings.HasPrefix(got[i].Content, tail) {
			t.Errorf("chunk %d missing overlap with previous", i)
		}
	}
	// 无内容丢失：拼接后所有段落标记仍在
	joined := ""
	for _, c := range got {
		joined += c.Content
	}
	for i := 0; i < 30; i += 7 {
		marker := string(rune('A' + i%26))
		if !strings.Contains(joined, marker) {
			t.Errorf("paragraph marker %q lost", marker)
		}
	}
	// 行号正确性：块起始行号应指向原文该行内容的前缀（“段”字）
	lines := strings.Split(in, "\n")
	for _, c := range got {
		if c.StartLine < 1 || c.StartLine > len(lines) {
			t.Fatalf("start line %d out of range", c.StartLine)
		}
		if !strings.HasPrefix(lines[c.StartLine-1], "段") {
			t.Errorf("start line %d points to %q, want a paragraph start", c.StartLine, lines[c.StartLine-1])
		}
	}
}

func TestSplitChunks_SingleHugeParagraph(t *testing.T) {
	// 无空行的超长段落：按句子边界硬切；chunk 0 吸收第 1 行的标题段（贪心合并），
	// 其余块都在第 3 行起的超长段内
	prefix := "标题行\n\n"
	in := prefix + strings.Repeat("这是一句话。", 400)
	got := SplitChunks(in)
	if len(got) < 2 {
		t.Fatalf("huge paragraph should be split, got %d", len(got))
	}
	for i, c := range got {
		if n := utf8.RuneCountInString(c.Content); n > chunkTargetRunes+chunkOverlapRunes+10 {
			t.Errorf("chunk %d too long after sentence split: %d", i, n)
		}
		want := 3
		if i == 0 {
			want = 1
		}
		if c.StartLine != want {
			t.Errorf("chunk %d start line = %d, want %d", i, c.StartLine, want)
		}
	}
}

func TestSplitChunks_EmptyAndTailMerge(t *testing.T) {
	if got := SplitChunks(""); len(got) != 0 {
		t.Fatalf("empty text should yield no chunks, got %d", len(got))
	}
	if got := SplitChunks("\n\n  \n\n"); len(got) != 0 {
		t.Fatalf("whitespace-only text should yield no chunks, got %d", len(got))
	}
	// 主体 + 极短尾段：尾段并入前一块，不单独成块
	main := strings.Repeat("正", 700)
	got := SplitChunks(main + "\n\n尾")
	if len(got) != 1 {
		t.Fatalf("tiny tail should merge into previous chunk, got %d chunks", len(got))
	}
	if !strings.Contains(got[0].Content, "尾") {
		t.Fatalf("tail content lost: %q", got[0].Content)
	}
}
