package audiobook

import (
	"strings"
	"testing"
)

func TestSegmentText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  []string
	}{
		{
			name:  "primary punctuation",
			input: "你好。世界！",
			max:   80,
			want:  []string{"你好。", "世界！"},
		},
		{
			name:  "closing quote stays with sentence",
			input: "他说：“走吧！”然后离开。",
			max:   80,
			want:  []string{"他说：“走吧！”", "然后离开。"},
		},
		{
			name:  "ellipsis is one terminator",
			input: "这是……什么？",
			max:   80,
			want:  []string{"这是……", "什么？"},
		},
		{
			name:  "semicolon splits",
			input: "甲；乙；丙。",
			max:   80,
			want:  []string{"甲；", "乙；", "丙。"},
		},
		{
			name:  "pure punctuation dropped",
			input: "。。。 ！！！ ……",
			max:   80,
			want:  nil,
		},
		{
			name:  "whitespace normalized",
			input: "  第一句。   第二句。 ",
			max:   80,
			want:  []string{"第一句。", "第二句。"},
		},
		{
			name:  "paragraph break splits",
			input: "第一段没有句号\n第二段也没有",
			max:   80,
			want:  []string{"第一段没有句号", "第二段也没有"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SegmentText(tc.input, tc.max)
			if len(got) != len(tc.want) {
				t.Fatalf("SegmentText(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("segment %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSegmentTextMaxRunesHardSplit(t *testing.T) {
	input := strings.Repeat("字", 100)
	got := SegmentText(input, 80)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2: %#v", len(got), got)
	}
	if len([]rune(got[0])) != 80 || len([]rune(got[1])) != 20 {
		t.Fatalf("lengths = %d,%d, want 80,20", len([]rune(got[0])), len([]rune(got[1])))
	}
	if strings.Join(got, "") != input {
		t.Fatalf("hard split lost text")
	}
}

func TestSegmentTextSecondarySplit(t *testing.T) {
	input := "一二三四五六七八九十一二三四五六七八九十一二三四五六七八九十，二二三四五六七八九十一二三四五六七八九十一二三四五六七八九十。"
	got := SegmentText(input, 20)
	if len(got) < 2 {
		t.Fatalf("segments = %d, want several: %#v", len(got), got)
	}
	for _, s := range got {
		if n := len([]rune(s)); n > 20 {
			t.Errorf("segment %q has %d runes, max is 20", s, n)
		}
	}
	if strings.Join(got, "") != input {
		t.Errorf("secondary split changed text: %q", strings.Join(got, ""))
	}
}

func TestSegmentTextDefaultMax(t *testing.T) {
	got := SegmentText(strings.Repeat("字", 100), 0)
	if len(got) != 2 {
		t.Fatalf("segments = %d, want 2", len(got))
	}
	if len([]rune(got[0])) > defaultSegmentRunes {
		t.Fatalf("default max not applied: %d", len([]rune(got[0])))
	}
}
