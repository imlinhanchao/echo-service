package audiobook

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteMasteredWAVStream(t *testing.T) {
	dir := t.TempDir()
	makeSeg := func(name string, amp float32) string {
		samples := make([]float32, 12000) // 0.5s @ 24k
		for i := range samples {
			samples[i] = amp * float32(math.Sin(2*math.Pi*440*float64(i)/24000))
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, encodeWAV(samples, 24000), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	seg1 := makeSeg("1.wav", 0.1)
	seg2 := makeSeg("2.wav", 0.2)

	out := filepath.Join(dir, "final.wav")
	frames, err := writeMasteredWAVStream(out, []string{seg1, seg2}, gapSeconds, targetSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	wantFrames := int64(12000*2 + int(gapSeconds*targetSampleRate))
	if frames != wantFrames {
		t.Fatalf("frames = %d, want %d", frames, wantFrames)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	dur, err := wavDurationSeconds(data)
	if err != nil {
		t.Fatal(err)
	}
	wantDur := float64(wantFrames) / float64(targetSampleRate)
	if math.Abs(dur-wantDur) > 0.001 {
		t.Fatalf("duration = %.4f, want %.4f", dur, wantDur)
	}
	wd, err := decodeWAV(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(wd.Samples) != int(wantFrames) {
		t.Fatalf("samples = %d, want %d", len(wd.Samples), wantFrames)
	}
	peak := float32(0)
	for _, s := range wd.Samples {
		if a := float32(math.Abs(float64(s))); a > peak {
			peak = a
		}
	}
	if peak > 0.98 {
		t.Fatalf("peak = %.3f，超过限幅", peak)
	}
}
