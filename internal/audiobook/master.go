package audiobook

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

const (
	targetSampleRate = 24000
	gapSeconds       = 0.3
	targetRMS        = 0.12589254117941673 // -18 dBFS
	silenceRMS       = 0.001               // -60 dBFS
	peakLimit        = 0.97
	fadeSeconds      = 0.008
)

// wavData is decoded PCM audio as mono float32 samples in [-1, 1].
type wavData struct {
	Samples    []float32
	SampleRate int
}

type wavHeader struct {
	Format     uint16
	Channels   uint16
	SampleRate uint32
	Bits       uint16
	DataOffset int
	DataLen    int
}

func parseWAVHeader(data []byte) (wavHeader, error) {
	var h wavHeader
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return h, errors.New("不是有效的 WAV 数据")
	}
	offset := 12
	foundFmt := false
	foundData := false
	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		end := start + size
		if size < 0 || end < start || end > len(data) {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if size >= 16 && start+16 <= len(data) {
				h.Format = binary.LittleEndian.Uint16(data[start : start+2])
				h.Channels = binary.LittleEndian.Uint16(data[start+2 : start+4])
				h.SampleRate = binary.LittleEndian.Uint32(data[start+4 : start+8])
				h.Bits = binary.LittleEndian.Uint16(data[start+14 : start+16])
				if h.Format == 0xFFFE && size >= 26 && start+26 <= len(data) {
					h.Format = binary.LittleEndian.Uint16(data[start+24 : start+26])
				}
				foundFmt = true
			}
		case "data":
			h.DataOffset = start
			h.DataLen = end - start
			foundData = true
		}
		offset = end
		if size%2 == 1 {
			offset++
		}
	}
	if !foundFmt {
		return h, errors.New("WAV 缺少 fmt 块")
	}
	if !foundData {
		return h, errors.New("WAV 缺少 data 块")
	}
	if h.Channels == 0 || h.SampleRate == 0 || h.Bits == 0 {
		return h, errors.New("WAV 格式信息不完整")
	}
	return h, nil
}

// decodeWAV decodes a RIFF/WAVE file to mono float32 samples. PCM 8/16/24/32
// bit and IEEE float32 are supported; multi-channel input is downmixed by
// averaging.
func decodeWAV(data []byte) (wavData, error) {
	h, err := parseWAVHeader(data)
	if err != nil {
		return wavData{}, err
	}
	bytesPerSample := int(h.Bits) / 8
	if bytesPerSample <= 0 {
		return wavData{}, fmt.Errorf("不支持的 WAV 位深 %d", h.Bits)
	}
	frameSize := bytesPerSample * int(h.Channels)
	if frameSize <= 0 {
		return wavData{}, errors.New("WAV 帧大小无效")
	}
	pcm := data[h.DataOffset : h.DataOffset+h.DataLen]
	frames := len(pcm) / frameSize
	if frames == 0 {
		return wavData{}, errors.New("WAV 中没有采样数据")
	}

	out := make([]float32, frames)
	for f := 0; f < frames; f++ {
		base := f * frameSize
		var sum float64
		for c := 0; c < int(h.Channels); c++ {
			off := base + c*bytesPerSample
			var v float64
			switch {
			case h.Format == 1 && h.Bits == 8:
				v = (float64(pcm[off]) - 128) / 128
			case h.Format == 1 && h.Bits == 16:
				v = float64(int16(binary.LittleEndian.Uint16(pcm[off:]))) / 32768
			case h.Format == 1 && h.Bits == 24:
				u := int32(pcm[off]) | int32(pcm[off+1])<<8 | int32(pcm[off+2])<<16
				if u&0x800000 != 0 {
					u -= 1 << 24
				}
				v = float64(u) / 8388608
			case h.Format == 1 && h.Bits == 32:
				v = float64(int32(binary.LittleEndian.Uint32(pcm[off:]))) / 2147483648
			case h.Format == 3 && h.Bits == 32:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[off:])))
			default:
				return wavData{}, fmt.Errorf("不支持的 WAV 编码 format=%d bits=%d", h.Format, h.Bits)
			}
			sum += v
		}
		out[f] = float32(sum / float64(h.Channels))
	}
	return wavData{Samples: out, SampleRate: int(h.SampleRate)}, nil
}

func readWAVFile(path string) (wavData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wavData{}, err
	}
	return decodeWAV(data)
}

// wavDurationSeconds reads only the WAV header and returns the duration.
func wavDurationSeconds(data []byte) (float64, error) {
	h, err := parseWAVHeader(data)
	if err != nil {
		return 0, err
	}
	bytesPerSample := int(h.Bits) / 8
	frameSize := bytesPerSample * int(h.Channels)
	if frameSize <= 0 || h.DataLen <= 0 {
		return 0, errors.New("WAV 没有采样数据")
	}
	frames := h.DataLen / frameSize
	return float64(frames) / float64(h.SampleRate), nil
}

// encodeWAV writes mono PCM16 WAV bytes.
func encodeWAV(samples []float32, sampleRate int) []byte {
	if sampleRate <= 0 {
		sampleRate = targetSampleRate
	}
	dataLen := len(samples) * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
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
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(int16(v)))
	}
	return buf
}

func writeWAVFile(path string, samples []float32, sampleRate int) error {
	return os.WriteFile(path, encodeWAV(samples, sampleRate), 0o644)
}

// resampleLinear converts a mono sample slice to another sample rate using
// linear interpolation.
func resampleLinear(in []float32, inRate, outRate int) []float32 {
	if len(in) == 0 || inRate <= 0 || outRate <= 0 || inRate == outRate {
		return in
	}
	ratio := float64(outRate) / float64(inRate)
	outLen := int(math.Round(float64(len(in)) * ratio))
	if outLen < 1 {
		outLen = 1
	}
	out := make([]float32, outLen)
	for i := 0; i < outLen; i++ {
		pos := float64(i) / ratio
		idx := int(pos)
		if idx >= len(in)-1 {
			out[i] = in[len(in)-1]
			continue
		}
		frac := float32(pos - float64(idx))
		out[i] = in[idx] + frac*(in[idx+1]-in[idx])
	}
	return out
}

// masterSegments resamples every segment to rate, normalizes its RMS to
// -18 dBFS, applies a peak limit and 8 ms fades, then concatenates the
// segments with gapS seconds of silence between them.
func masterSegments(segments []wavData, gapS float64, rate int) ([]float32, error) {
	if rate <= 0 {
		rate = targetSampleRate
	}
	gap := int(math.Round(gapS * float64(rate)))
	if gap < 0 {
		gap = 0
	}

	processed := make([][]float32, len(segments))
	total := 0
	for i, seg := range segments {
		if seg.SampleRate <= 0 {
			return nil, fmt.Errorf("第 %d 段采样率无效", i+1)
		}
		samples := seg.Samples
		if seg.SampleRate != rate {
			samples = resampleLinear(samples, seg.SampleRate, rate)
		}
		samples = append([]float32(nil), samples...)
		normalizeSegment(samples, rate)
		processed[i] = samples
		total += len(samples)
	}
	if len(processed) > 1 {
		total += gap * (len(processed) - 1)
	}

	out := make([]float32, 0, total)
	for i, samples := range processed {
		if i > 0 && gap > 0 {
			out = append(out, make([]float32, gap)...)
		}
		out = append(out, samples...)
	}
	return out, nil
}

func normalizeSegment(samples []float32, rate int) {
	if len(samples) == 0 {
		return
	}
	var sumSquares float64
	for _, s := range samples {
		sumSquares += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSquares / float64(len(samples)))
	if rms >= silenceRMS {
		gain := targetRMS / rms
		for i, s := range samples {
			v := float64(s) * gain
			if v > peakLimit {
				v = peakLimit
			} else if v < -peakLimit {
				v = -peakLimit
			}
			samples[i] = float32(v)
		}
	} else {
		for i, s := range samples {
			if s > peakLimit {
				samples[i] = peakLimit
			} else if s < -peakLimit {
				samples[i] = -peakLimit
			}
		}
	}
	applyFades(samples, rate)
}

func applyFades(samples []float32, rate int) {
	n := int(math.Round(fadeSeconds * float64(rate)))
	if n < 1 || len(samples) == 0 {
		return
	}
	if len(samples) < 2*n {
		n = len(samples) / 2
	}
	if n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		g := float32(0.5 * (1 - math.Cos(math.Pi*float64(i)/float64(n))))
		samples[i] *= g
		samples[len(samples)-1-i] *= g
	}
}

// writeMasteredWAVStream writes the mastered timeline to a WAV file one segment
// at a time. It applies the same per-segment RMS normalization, peak limiting
// and fades as masterSegments, but never holds the whole book in memory: a
// 14-hour audiobook would otherwise need ~5 GB of float32 samples.
func writeMasteredWAVStream(path string, segPaths []string, gapS float64, rate int) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Write(make([]byte, 44)); err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)

	gapFrames := int(math.Round(gapS * float64(rate)))
	var totalFrames int64
	zeros := make([]byte, 8192)
	pcm := make([]byte, 8192)

	for i, p := range segPaths {
		if i > 0 && gapFrames > 0 {
			remaining := gapFrames * 2
			for remaining > 0 {
				n := remaining
				if n > len(zeros) {
					n = len(zeros)
				}
				if _, err := bw.Write(zeros[:n]); err != nil {
					return 0, err
				}
				remaining -= n
			}
			totalFrames += int64(gapFrames)
		}

		wd, err := readWAVFile(p)
		if err != nil {
			return 0, fmt.Errorf("解码 %s: %w", p, err)
		}
		samples := wd.Samples
		if wd.SampleRate != rate {
			samples = resampleLinear(samples, wd.SampleRate, rate)
		}
		normalizeSegment(samples, rate)
		applyFades(samples, rate)
		totalFrames += int64(len(samples))

		for len(samples) > 0 {
			n := len(samples)
			if n > len(pcm)/2 {
				n = len(pcm) / 2
			}
			for j := 0; j < n; j++ {
				v := math.Round(float64(samples[j]) * 32767)
				if v > 32767 {
					v = 32767
				} else if v < -32768 {
					v = -32768
				}
				binary.LittleEndian.PutUint16(pcm[j*2:], uint16(int16(v)))
			}
			if _, err := bw.Write(pcm[:n*2]); err != nil {
				return 0, err
			}
			samples = samples[n:]
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	if _, err := f.WriteAt(wavHeaderBytes(totalFrames, rate), 0); err != nil {
		return 0, err
	}
	return totalFrames, nil
}

// wavHeaderBytes builds a 44-byte mono PCM16 WAV header for frames frames.
func wavHeaderBytes(frames int64, rate int) []byte {
	dataLen := frames * 2
	if dataLen > math.MaxUint32-36 {
		dataLen = math.MaxUint32 - 36
	}
	buf := make([]byte, 44)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(rate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	return buf
}
