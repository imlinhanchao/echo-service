package audiobook

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildWAVBytes encodes float samples as PCM16 WAV with the given channel
// count (channels are duplicated).
func buildWAVBytes(samples []float32, sampleRate, channels int) []byte {
	if channels < 1 {
		channels = 1
	}
	dataLen := len(samples) * channels * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	for i, s := range samples {
		v := math.Round(float64(s) * 32767)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		for c := 0; c < channels; c++ {
			off := 44 + (i*channels+c)*2
			binary.LittleEndian.PutUint16(buf[off:], uint16(int16(v)))
		}
	}
	return buf
}

func makeSineWAV(sampleRate int, seconds, amplitude float64, channels int) []byte {
	n := int(math.Round(seconds * float64(sampleRate)))
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(amplitude * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}
	return buildWAVBytes(samples, sampleRate, channels)
}

func rmsDB(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(rms)
}

func peakAbs(samples []float32) float64 {
	peak := 0.0
	for _, s := range samples {
		if v := math.Abs(float64(s)); v > peak {
			peak = v
		}
	}
	return peak
}

func TestDecodeWAVMono(t *testing.T) {
	data := makeSineWAV(24000, 0.5, 0.5, 1)
	wd, err := decodeWAV(data)
	if err != nil {
		t.Fatalf("decodeWAV: %v", err)
	}
	if wd.SampleRate != 24000 {
		t.Errorf("sample rate = %d, want 24000", wd.SampleRate)
	}
	if len(wd.Samples) != 12000 {
		t.Errorf("samples = %d, want 12000", len(wd.Samples))
	}
	if got := peakAbs(wd.Samples); math.Abs(got-0.5) > 0.01 {
		t.Errorf("peak = %.3f, want ~0.5", got)
	}
}

func TestDecodeWAVStereoAndResample(t *testing.T) {
	data := makeSineWAV(48000, 0.5, 0.5, 2)
	wd, err := decodeWAV(data)
	if err != nil {
		t.Fatalf("decodeWAV: %v", err)
	}
	if wd.SampleRate != 48000 {
		t.Errorf("sample rate = %d, want 48000", wd.SampleRate)
	}
	if len(wd.Samples) != 24000 {
		t.Errorf("samples = %d, want 24000", len(wd.Samples))
	}
	resampled := resampleLinear(wd.Samples, 48000, 24000)
	if len(resampled) != 12000 {
		t.Errorf("resampled length = %d, want 12000", len(resampled))
	}
	if got := peakAbs(resampled); math.Abs(got-0.5) > 0.02 {
		t.Errorf("resampled peak = %.3f, want ~0.5", got)
	}
}

func TestWAVDurationSeconds(t *testing.T) {
	d, err := wavDurationSeconds(makeSineWAV(24000, 0.75, 0.2, 1))
	if err != nil {
		t.Fatalf("wavDurationSeconds: %v", err)
	}
	if math.Abs(d-0.75) > 0.001 {
		t.Errorf("duration = %.4f, want 0.75", d)
	}
	if _, err := wavDurationSeconds([]byte("not a wav")); err == nil {
		t.Error("expected error for invalid WAV")
	}
}

func TestMasterSegments(t *testing.T) {
	specs := []struct {
		duration float64
		amp      float64
	}{
		{0.5, 0.1},
		{0.4, 0.3},
		{0.6, 0.02},
	}
	segments := make([]wavData, 0, len(specs))
	for _, spec := range specs {
		wd, err := decodeWAV(makeSineWAV(24000, spec.duration, spec.amp, 1))
		if err != nil {
			t.Fatalf("decodeWAV: %v", err)
		}
		segments = append(segments, wd)
	}

	mastered, err := masterSegments(segments, gapSeconds, targetSampleRate)
	if err != nil {
		t.Fatalf("masterSegments: %v", err)
	}
	gap := int(math.Round(gapSeconds * targetSampleRate))
	wantLen := 12000 + 9600 + 14400 + 2*gap
	if len(mastered) != wantLen {
		t.Fatalf("mastered length = %d, want %d", len(mastered), wantLen)
	}
	if got := peakAbs(mastered); got > peakLimit+1e-6 {
		t.Errorf("peak = %.4f, must be <= %.2f", got, peakLimit)
	}
	offsets := []int{0, 12000 + gap, 12000 + gap + 9600 + gap}
	lengths := []int{12000, 9600, 14400}
	for i := range offsets {
		db := rmsDB(mastered[offsets[i] : offsets[i]+lengths[i]])
		if math.Abs(db-(-18)) > 0.5 {
			t.Errorf("segment %d RMS = %.2f dBFS, want ~-18", i, db)
		}
	}
	wantDuration := float64(wantLen) / targetSampleRate
	if math.Abs(float64(len(mastered))/targetSampleRate-wantDuration) > 1e-9 {
		t.Errorf("duration mismatch")
	}

	path := filepath.Join(t.TempDir(), "master.wav")
	if err := writeWAVFile(path, mastered, targetSampleRate); err != nil {
		t.Fatalf("writeWAVFile: %v", err)
	}
	roundTrip, err := readWAVFile(path)
	if err != nil {
		t.Fatalf("readWAVFile: %v", err)
	}
	if len(roundTrip.Samples) != wantLen {
		t.Errorf("round-trip samples = %d, want %d", len(roundTrip.Samples), wantLen)
	}
	if got := peakAbs(roundTrip.Samples); got > peakLimit+1e-3 {
		t.Errorf("round-trip peak = %.4f, must be <= %.2f", got, peakLimit)
	}
}

func TestSilentSegmentSkipped(t *testing.T) {
	silent := wavData{Samples: make([]float32, 2400), SampleRate: 24000}
	seg := wavData{Samples: make([]float32, 2400), SampleRate: 24000}
	for i := range seg.Samples {
		seg.Samples[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/24000))
	}
	out, err := masterSegments([]wavData{silent, seg}, gapSeconds, targetSampleRate)
	if err != nil {
		t.Fatalf("masterSegments: %v", err)
	}
	if got := peakAbs(out[:2400]); got != 0 {
		t.Errorf("silent segment peak = %f, want 0", got)
	}
}

func TestWriteSRTFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subtitles.srt")
	cues := []srtCue{
		{Start: 0, End: 0.5, Text: "第一段。"},
		{Start: 0.8, End: 1.2, Text: "第二段。"},
		{Start: 1.5, End: 2.1, Text: "第三段。"},
	}
	if err := writeSRTFile(path, cues); err != nil {
		t.Fatalf("writeSRTFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	got := string(data)
	if n := strings.Count(got, "-->"); n != len(cues) {
		t.Fatalf("cue count = %d, want %d\n%s", n, len(cues), got)
	}
	for _, want := range []string{
		"1\n00:00:00,000 --> 00:00:00,500\n第一段。",
		"2\n00:00:00,800 --> 00:00:01,200\n第二段。",
		"3\n00:00:01,500 --> 00:00:02,100\n第三段。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SRT missing %q:\n%s", want, got)
		}
	}
}

func TestFormatSRTTime(t *testing.T) {
	cases := map[float64]string{
		0:        "00:00:00,000",
		0.5:      "00:00:00,500",
		61.25:    "00:01:01,250",
		3661.001: "01:01:01,001",
	}
	for in, want := range cases {
		if got := formatSRTTime(in); got != want {
			t.Errorf("formatSRTTime(%v) = %q, want %q", in, got, want)
		}
	}
}
