package cloudflare

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CaptionParams configures a Whisper-timed, ffmpeg-burned caption render.
type CaptionParams struct {
	Input        string
	Out          string
	Language     string
	Position     string // top | center | bottom
	Font         string
	FontSize     int
	FontColor    string // hex, e.g. #FFFFFF
	OutlineColor string // hex
	Shadow       string // none | min | mid | max
	MaxWords     int    // words per caption line
	SubtitlesOut string // optional path to also keep the .srt
}

// CaptionResult describes a completed caption render.
type CaptionResult struct {
	Model    string
	Endpoint string
	SRT      string
}

// Caption transcribes the input with Workers AI Whisper (word timestamps),
// groups words into short cues, and burns them in locally with ffmpeg.
func Caption(ctx context.Context, p CaptionParams) (*CaptionResult, error) {
	if p.Out == "" {
		return nil, fmt.Errorf("--out is required")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg is required for cloudflare captions: %w", err)
	}
	tr, err := Transcribe(ctx, TranscribeParams{File: p.Input, Language: p.Language, Format: "text"})
	if err != nil {
		return nil, err
	}
	srt := SegmentsToSRT(WordCues(tr.Output.Segments, p.MaxWords))
	if strings.TrimSpace(srt) == "" {
		return nil, fmt.Errorf("no speech detected in %s; nothing to caption", p.Input)
	}
	dir, err := os.MkdirTemp("", "creative-caption-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	srtPath := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		return nil, err
	}
	if p.SubtitlesOut != "" {
		if err := os.WriteFile(p.SubtitlesOut, []byte(srt), 0o644); err != nil {
			return nil, err
		}
	}
	if dir := filepath.Dir(p.Out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	vf := fmt.Sprintf("subtitles=filename='%s':force_style='%s'", escapeFilter(srtPath), ForceStyle(p))
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", p.Input,
		"-vf", vf, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", p.Out)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg burn captions: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return &CaptionResult{Model: tr.Model, Endpoint: tr.Endpoint, SRT: srt}, nil
}

// WordCues regroups Whisper segments into cues of at most maxWords words,
// splitting on word timestamps. Segments without word timing pass through.
func WordCues(segs []Segment, maxWords int) []Segment {
	if maxWords <= 0 {
		maxWords = 5
	}
	var cues []Segment
	for _, s := range segs {
		if len(s.Words) == 0 {
			cues = append(cues, s)
			continue
		}
		for i := 0; i < len(s.Words); i += maxWords {
			end := i + maxWords
			if end > len(s.Words) {
				end = len(s.Words)
			}
			group := s.Words[i:end]
			parts := make([]string, 0, len(group))
			for _, w := range group {
				parts = append(parts, strings.TrimSpace(w.Word))
			}
			cues = append(cues, Segment{Start: group[0].Start, End: group[len(group)-1].End, Text: strings.Join(parts, " ")})
		}
	}
	return cues
}

// ForceStyle builds the libass force_style string from caption options.
func ForceStyle(p CaptionParams) string {
	size := p.FontSize
	if size <= 0 {
		size = 18
	}
	style := []string{fmt.Sprintf("FontSize=%d", size), "BorderStyle=1", "Outline=2"}
	if p.Font != "" {
		style = append(style, "FontName="+p.Font)
	}
	if c, ok := assColor(p.FontColor); ok {
		style = append(style, "PrimaryColour="+c)
	}
	if c, ok := assColor(p.OutlineColor); ok {
		style = append(style, "OutlineColour="+c)
	}
	switch p.Position {
	case "top":
		style = append(style, "Alignment=8", "MarginV=40")
	case "center":
		style = append(style, "Alignment=5")
	default:
		style = append(style, "Alignment=2", "MarginV=40")
	}
	switch p.Shadow {
	case "none":
		style = append(style, "Shadow=0")
	case "min":
		style = append(style, "Shadow=1")
	case "max":
		style = append(style, "Shadow=4")
	default:
		style = append(style, "Shadow=2")
	}
	return strings.Join(style, ",")
}

// assColor converts #RRGGBB to libass &H00BBGGRR.
func assColor(hex string) (string, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return "", false
	}
	return "&H00" + strings.ToUpper(h[4:6]+h[2:4]+h[0:2]), true
}

// escapeFilter escapes a path for use inside an ffmpeg filtergraph value.
func escapeFilter(p string) string {
	r := strings.NewReplacer(`\`, `\\\\`, `'`, `\\\'`, `:`, `\\:`)
	return r.Replace(p)
}
