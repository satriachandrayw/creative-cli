package cloudflare

import (
	"context"
	"strings"
)

// SpeechModel maps ElevenLabs API model ids (eleven_v3) onto catalog ids
// (elevenlabs/eleven-v3); catalog and @cf/ ids pass through.
func SpeechModel(model string) string {
	if model == "" {
		return "elevenlabs/eleven-v3"
	}
	if strings.Contains(model, "/") {
		return model
	}
	return "elevenlabs/" + strings.ReplaceAll(model, "_", "-")
}

// SpeechParams configures a text-to-speech run.
type SpeechParams struct {
	Text         string
	VoiceID      string
	Model        string
	OutputFormat string
	LanguageCode string
	Out          string
}

// Speech synthesizes text and saves the audio.
func Speech(ctx context.Context, p SpeechParams) (*MediaResult, error) {
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := SpeechModel(p.Model)
	in := map[string]any{"text": p.Text}
	switch {
	case strings.HasPrefix(model, "elevenlabs/"):
		in["voice_id"] = p.VoiceID
		putStr(in, "output_format", p.OutputFormat)
		putStr(in, "language_code", p.LanguageCode)
	case strings.HasPrefix(model, "@cf/deepgram/aura"):
		putStr(in, "speaker", p.VoiceID)
	case strings.HasPrefix(model, "@cf/"):
		putStr(in, "lang", p.LanguageCode)
	default:
		putStr(in, "voice_id", p.VoiceID)
	}
	res, err := c.Run(ctx, model, in)
	if err != nil {
		return nil, err
	}
	url, err := writeOrDownload(ctx, res, "audio", p.Out)
	if err != nil {
		return nil, err
	}
	return &MediaResult{Model: model, Endpoint: res.Endpoint, URL: url, Raw: res.Raw}, nil
}

// MusicParams configures a music run.
type MusicParams struct {
	Prompt            string
	Model             string
	MusicLengthMs     int
	OutputFormat      string
	ForceInstrumental bool
	Out               string
}

// Music composes a track and saves the audio.
func Music(ctx context.Context, p MusicParams) (*MediaResult, error) {
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := p.Model
	if model == "" {
		model = "elevenlabs/music-v2"
	}
	in := map[string]any{"prompt": p.Prompt}
	if p.MusicLengthMs > 0 {
		in["music_length_ms"] = p.MusicLengthMs
	}
	putStr(in, "output_format", p.OutputFormat)
	if p.ForceInstrumental {
		in["force_instrumental"] = true
	}
	res, err := c.Run(ctx, model, in)
	if err != nil {
		return nil, err
	}
	url, err := writeOrDownload(ctx, res, "audio", p.Out)
	if err != nil {
		return nil, err
	}
	return &MediaResult{Model: model, Endpoint: res.Endpoint, URL: url, Raw: res.Raw}, nil
}
