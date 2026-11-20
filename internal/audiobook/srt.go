package audiobook

import (
	"fmt"
	"math"
	"os"
	"strings"
)

type srtCue struct {
	Start float64
	End   float64
	Text  string
}

// formatSRTTime renders seconds as HH:MM:SS,mmm.
func formatSRTTime(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	totalMS := int64(math.Round(seconds * 1000))
	h := totalMS / 3600000
	totalMS %= 3600000
	m := totalMS / 60000
	totalMS %= 60000
	s := totalMS / 1000
	ms := totalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

func writeSRTFile(path string, cues []srtCue) error {
	var b strings.Builder
	for i, cue := range cues {
		text := strings.TrimSpace(cue.Text)
		text = strings.ReplaceAll(text, "\r\n", " ")
		text = strings.ReplaceAll(text, "\n", " ")
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, formatSRTTime(cue.Start), formatSRTTime(cue.End), text)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
