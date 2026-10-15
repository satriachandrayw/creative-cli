package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TranscribeModel maps OpenAI's whisper-1 default onto Workers AI Whisper.
func TranscribeModel(model string) string {
	switch model {
	case "", "whisper-1", "whisper", "whisper-large-v3-turbo":
		return "@cf/openai/whisper-large-v3-turbo"
	}
	return model
}

// TranscribeParams configures a Whisper run.
type TranscribeParams struct {
	File      string
	Model     string
	Format    string // text | json | srt | vtt | verbose_json
	Language  string
	Prompt    string
	Translate bool
}

// Segment is one timed Whisper segment.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	Words []Word  `json:"words"`
}

// Word is one timed word.
type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// WhisperOutput is the Workers AI Whisper result.
type WhisperOutput struct {
	Text     string    `json:"text"`
	VTT      string    `json:"vtt"`
	Segments []Segment `json:"segments"`
}

// TranscribeResult carries the formatted transcript plus the parsed output.
type TranscribeResult struct {
	Model      string
	Endpoint   string
	Transcript string
	Output     WhisperOutput
	Raw        []byte
}

// Transcribe runs Whisper over an audio or video file (local path or URL).
func Transcribe(ctx context.Context, p TranscribeParams) (*TranscribeResult, error) {
	audio, err := audioBytes(ctx, p.File)
	if err != nil {
		return nil, err
	}
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := TranscribeModel(p.Model)
	in := map[string]any{"audio": base64.StdEncoding.EncodeToString(audio)}
	if p.Translate {
		in["task"] = "translate"
	}
	putStr(in, "language", p.Language)
	putStr(in, "initial_prompt", p.Prompt)
	res, err := c.Run(ctx, model, in)
	if err != nil {
		return nil, err
	}
	var out WhisperOutput
	if err := json.Unmarshal(res.Result, &out); err != nil {
		return nil, fmt.Errorf("decode whisper result: %w", err)
	}
	transcript, err := FormatTranscript(out, res.Result, p.Format)
	if err != nil {
		return nil, err
	}
	return &TranscribeResult{Model: model, Endpoint: res.Endpoint, Transcript: transcript, Output: out, Raw: res.Raw}, nil
}

// FormatTranscript renders Whisper output in the requested --format.
func FormatTranscript(out WhisperOutput, result json.RawMessage, format string) (string, error) {
	switch format {
	case "", "text":
		return strings.TrimSpace(out.Text), nil
	case "json":
		b, err := json.Marshal(map[string]string{"text": strings.TrimSpace(out.Text)})
		return string(b), err
	case "verbose_json":
		return string(result), nil
	case "vtt":
		if out.VTT != "" {
			return out.VTT, nil
		}
		return SegmentsToVTT(out.Segments), nil
	case "srt":
		return SegmentsToSRT(out.Segments), nil
	}
	return "", fmt.Errorf("unknown --format %q (want text|json|srt|vtt|verbose_json)", format)
}

// SegmentsToSRT renders timed segments as SubRip.
func SegmentsToSRT(segs []Segment) string {
	var b strings.Builder
	n := 0
	for _, s := range segs {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, stamp(s.Start, ","), stamp(s.End, ","), text)
	}
	return b.String()
}

// SegmentsToVTT renders timed segments as WebVTT.
func SegmentsToVTT(segs []Segment) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, s := range segs {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n", stamp(s.Start, "."), stamp(s.End, "."), text)
	}
	return b.String()
}

func stamp(sec float64, sep string) string {
	if sec < 0 {
		sec = 0
	}
	ms := int64(math.Round(sec * 1000))
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", h, m, s, sep, ms%1000)
}

// audioBytes returns audio Whisper can decode. With ffmpeg available every
// input (including video containers) is normalized to 16 kHz mono MP3, which
// also keeps the base64 payload small; otherwise the file is sent as-is.
func audioBytes(ctx context.Context, input string) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		data, _, lerr := loadRef(ctx, input)
		return data, lerr
	}
	dir, err := os.MkdirTemp("", "creative-audio-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "audio.mp3")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", input,
		"-vn", "-ac", "1", "-ar", "16000", "-b:a", "64k", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract audio from %s: %v: %s", input, err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(dest)
}
