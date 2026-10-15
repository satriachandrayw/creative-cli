package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/satriachandrayw/creative-cli/internal/core"
)

// AnalyzeParams configures a video-understanding run.
type AnalyzeParams struct {
	Input           string
	Model           string // Gemini catalog id, e.g. google/gemini-3-flash
	Prompt          string // full instruction text
	Temperature     float64
	MaxOutputTokens int
	Mode            string // video (inline mp4) | frames (sampled JPEG frames + transcript)
	Frames          int
}

// AnalyzeResult carries the model's answer.
type AnalyzeResult struct {
	Model    string
	Endpoint string
	Text     string
	Usage    json.RawMessage
	Raw      []byte
}

// maxInlineVideo is Gemini's inline request ceiling; larger inputs fall back to
// frame sampling.
const maxInlineVideo = 20 << 20

// Analyze sends the video to a Gemini model through Cloudflare. In "video" mode
// the file is attached inline (Gemini's native video understanding); "frames"
// mode samples stills with ffmpeg and adds a Whisper transcript, for models or
// inputs where inline video is unavailable.
func Analyze(ctx context.Context, p AnalyzeParams) (*AnalyzeResult, error) {
	c, err := New()
	if err != nil {
		return nil, err
	}
	if strings.Contains(p.Input, "youtube.com/") || strings.Contains(p.Input, "youtu.be/") {
		return nil, fmt.Errorf("YouTube URLs need --provider fal; the cloudflare provider analyzes local files or direct video URLs")
	}
	model := p.Model
	if model == "" {
		model = "google/gemini-3-flash"
	}
	mode := p.Mode
	var parts []map[string]any
	if mode == "" || mode == "video" {
		data, mime, lerr := loadRef(ctx, p.Input)
		if lerr != nil {
			return nil, lerr
		}
		if len(data) > maxInlineVideo {
			if mode == "video" {
				return nil, fmt.Errorf("video is %d MB; inline limit is 20 MB (use --mode frames)", len(data)>>20)
			}
			mode = "frames"
		} else {
			if !strings.HasPrefix(mime, "video/") {
				mime = "video/mp4"
			}
			mode = "video"
			parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": base64.StdEncoding.EncodeToString(data)}})
		}
	}
	text := p.Prompt
	if mode == "frames" {
		frameParts, transcript, ferr := sampleFrames(ctx, p.Input, p.Frames)
		if ferr != nil {
			return nil, ferr
		}
		parts = append(parts, frameParts...)
		if transcript != "" {
			text += "\n\nAudio transcript (Whisper):\n" + transcript
		}
		text += "\n\nThe images above are frames sampled evenly through the video, in order."
	}
	parts = append(parts, map[string]any{"text": text})
	in := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig": map[string]any{
			"temperature":     p.Temperature,
			"maxOutputTokens": p.MaxOutputTokens,
		},
	}
	res, err := c.Run(ctx, model, in)
	if err != nil {
		return nil, err
	}
	out, usage, err := geminiText(res.Result)
	if err != nil {
		return nil, err
	}
	return &AnalyzeResult{Model: model, Endpoint: res.Endpoint + " (" + mode + ")", Text: out, Usage: usage, Raw: res.Raw}, nil
}

// geminiText pulls the answer from a native Gemini or chat-completions reply.
func geminiText(result json.RawMessage) (string, json.RawMessage, error) {
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		UsageMetadata json.RawMessage `json:"usageMetadata"`
		Usage         json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return "", nil, fmt.Errorf("decode gemini reply: %w", err)
	}
	var b strings.Builder
	for _, c := range r.Candidates {
		for _, part := range c.Content.Parts {
			if !part.Thought {
				b.WriteString(part.Text)
			}
		}
		break
	}
	if b.Len() == 0 && len(r.Choices) > 0 {
		b.WriteString(r.Choices[0].Message.Content)
	}
	usage := r.UsageMetadata
	if len(usage) == 0 {
		usage = r.Usage
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", usage, fmt.Errorf("gemini returned no text: %s", strings.TrimSpace(string(result)))
	}
	return strings.TrimSpace(b.String()), usage, nil
}

func sampleFrames(ctx context.Context, input string, n int) ([]map[string]any, string, error) {
	if n <= 0 {
		n = 12
	}
	src, err := ProbeSource(input)
	if err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp("", "creative-frames-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	duration := float64(src.Frames) / maxf(src.FrameRate, 1)
	fps := float64(n) / maxf(duration, 1)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", input,
		"-vf", fmt.Sprintf("fps=%.4f,scale=768:-2", fps), "-frames:v", fmt.Sprint(n), "-q:v", "4",
		filepath.Join(dir, "f%03d.jpg"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, "", fmt.Errorf("ffmpeg sample frames: %v: %s", err, strings.TrimSpace(string(out)))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "f*.jpg"))
	sort.Strings(files)
	parts := make([]map[string]any, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, "", err
		}
		parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data)}})
	}
	transcript := ""
	if tr, err := Transcribe(ctx, TranscribeParams{File: input, Format: "text"}); err == nil {
		transcript = tr.Transcript
	} else {
		core.Progress("transcript unavailable, analyzing frames only: %v", err)
	}
	return parts, transcript, nil
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
