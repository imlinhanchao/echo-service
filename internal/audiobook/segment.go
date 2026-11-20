package audiobook

import (
	"strings"
	"unicode"
)

const defaultSegmentRunes = 80

var sentenceTerminators = map[rune]bool{
	'。': true,
	'！': true,
	'？': true,
	'；': true,
	'…': true,
}

var closingMarks = map[rune]bool{
	'”':  true,
	'’':  true,
	'」':  true,
	'』':  true,
	'）':  true,
	'】':  true,
	'》':  true,
	'〉':  true,
	'〕':  true,
	'"':  true,
	'\'': true,
}

var secondaryMarks = map[rune]bool{
	'，': true,
	'、': true,
	'：': true,
}

// SegmentText splits Chinese prose into TTS-sized sentences. Primary
// boundaries are 。！？；… together with trailing closing quotes; sentences
// longer than maxRunes are split again on ，、：, and hard-split as a last
// resort. Pure punctuation/whitespace fragments are dropped. Original
// punctuation is preserved.
func SegmentText(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = defaultSegmentRunes
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.Join(strings.Fields(para), " ")
		if para == "" {
			continue
		}
		for _, sentence := range splitSentences(para) {
			for _, piece := range splitLongSentence(sentence, maxRunes) {
				piece = strings.TrimSpace(piece)
				if isMeaningful(piece) {
					out = append(out, piece)
				}
			}
		}
	}
	return out
}

func splitSentences(s string) []string {
	runes := []rune(s)
	var out []string
	var b strings.Builder
	flush := func() {
		if v := strings.TrimSpace(b.String()); v != "" {
			out = append(out, v)
		}
		b.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		b.WriteRune(r)
		if !sentenceTerminators[r] {
			continue
		}
		// Keep trailing closing quotes and repeated terminators ("……", "！？").
		for i+1 < len(runes) {
			next := runes[i+1]
			if sentenceTerminators[next] || closingMarks[next] {
				i++
				b.WriteRune(next)
				continue
			}
			break
		}
		flush()
	}
	flush()
	return out
}

func splitLongSentence(s string, maxRunes int) []string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return []string{s}
	}

	var clauses []string
	var b strings.Builder
	for _, r := range runes {
		b.WriteRune(r)
		if secondaryMarks[r] {
			clauses = append(clauses, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		clauses = append(clauses, b.String())
	}

	var out []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if curLen > 0 {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			curLen = 0
		}
	}
	for _, clause := range clauses {
		cr := []rune(clause)
		if len(cr) > maxRunes {
			flush()
			for len(cr) > maxRunes {
				out = append(out, string(cr[:maxRunes]))
				cr = cr[maxRunes:]
			}
			if len(cr) > 0 {
				out = append(out, string(cr))
			}
			continue
		}
		if curLen > 0 && curLen+len(cr) > maxRunes {
			flush()
		}
		cur.WriteString(clause)
		curLen += len(cr)
	}
	flush()
	return out
}

func isMeaningful(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}
