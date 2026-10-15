package cloudflare

import (
	"context"
	"fmt"
	"strings"
)

// VideoParams configures a text/reference-to-video run.
type VideoParams struct {
	Prompt        string
	Model         string
	Images        []string
	Videos        []string
	Audios        []string
	FirstFrame    string
	LastFrame     string
	Duration      int
	DurationSet   bool
	Resolution    string
	AspectRatio   string
	GenerateAudio bool
	Seed          int
	Out           string
}

// MediaResult describes a completed run whose output is a single media file.
type MediaResult struct {
	Model    string
	Endpoint string
	URL      string
	Raw      []byte
}

// VideoModel maps CLI model aliases onto catalog ids.
func VideoModel(model string) string {
	switch model {
	case "", "auto", "2.5", "seedance", "seedance-2.5":
		return "bytedance/seedance-2.5"
	case "seedance-2.0", "2.0":
		return "bytedance/seedance-2.0"
	case "omni", "omni-flash":
		return "google/gemini-omni-flash"
	case "omni-1.1-flash":
		return "google/gemini-omni-1.1-flash"
	}
	return model
}

func isOmni(model string) bool { return strings.HasPrefix(model, "google/gemini-omni") }

// VideoInput builds the model-specific input object. Exported for tests.
func VideoInput(p VideoParams) (map[string]any, error) {
	model := VideoModel(p.Model)
	if p.LastFrame != "" && p.FirstFrame == "" {
		return nil, fmt.Errorf("--last-frame requires --first-frame")
	}
	if p.FirstFrame != "" && len(p.Images) > 0 {
		return nil, fmt.Errorf("--first-frame cannot be combined with --image")
	}
	first, err := optionalURI(p.FirstFrame)
	if err != nil {
		return nil, err
	}
	last, err := optionalURI(p.LastFrame)
	if err != nil {
		return nil, err
	}
	images, err := refURIs(p.Images)
	if err != nil {
		return nil, err
	}
	videos, err := refURIs(p.Videos)
	if err != nil {
		return nil, err
	}
	audios, err := refURIs(p.Audios)
	if err != nil {
		return nil, err
	}

	in := map[string]any{}
	if isOmni(model) {
		if len(videos) > 1 || len(audios) > 1 {
			return nil, fmt.Errorf("%s accepts at most one --video and one --audio reference", model)
		}
		if p.Seed >= 0 {
			return nil, fmt.Errorf("%s does not support --seed", model)
		}
		putStr(in, "text", p.Prompt)
		putStr(in, "image", first)
		putStr(in, "last_frame", last)
		if len(images) > 0 {
			in["reference_images"] = images
		}
		if len(videos) == 1 {
			in["video"] = videos[0]
		}
		if len(audios) == 1 {
			in["audio"] = audios[0]
		}
		if p.AspectRatio != "16:9" && p.AspectRatio != "9:16" {
			return nil, fmt.Errorf("%s supports --aspect-ratio 16:9 or 9:16", model)
		}
		in["aspect_ratio"] = p.AspectRatio
		putStr(in, "resolution", p.Resolution)
		return in, nil
	}

	// Seedance (bytedance/*) schema.
	putStr(in, "prompt", p.Prompt)
	putStr(in, "image", first)
	putStr(in, "last_frame_image", last)
	if len(images) > 0 {
		in["reference_images"] = images
	}
	if len(videos) > 0 {
		in["reference_videos"] = videos
	}
	if len(audios) > 0 {
		in["reference_audios"] = audios
	}
	in["duration"] = p.Duration
	putStr(in, "resolution", p.Resolution)
	if p.FirstFrame == "" {
		putStr(in, "aspect_ratio", p.AspectRatio)
	}
	in["generate_audio"] = p.GenerateAudio
	if p.Seed >= 0 {
		in["seed"] = p.Seed
	}
	return in, nil
}

func optionalURI(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	return refURI(ref)
}

// GenerateVideo runs a video model synchronously and saves the result.
func GenerateVideo(ctx context.Context, p VideoParams) (*MediaResult, error) {
	in, err := VideoInput(p)
	if err != nil {
		return nil, err
	}
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := VideoModel(p.Model)
	res, err := c.Run(ctx, model, in)
	if err != nil {
		return nil, err
	}
	url, err := writeOrDownload(ctx, res, "video", p.Out)
	if err != nil {
		return nil, err
	}
	return &MediaResult{Model: model, Endpoint: res.Endpoint, URL: url, Raw: res.Raw}, nil
}
