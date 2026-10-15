package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// UpscaleModel maps CLI aliases onto catalog ids.
func UpscaleModel(model string) string {
	switch model {
	case "", "auto", "flux":
		return "black-forest-labs/flux-video-upscale"
	case "topaz", "topaz-fast", "starlight-fast-2":
		return "topaz/starlight-fast-2"
	case "topaz-precise", "starlight-precise-2-6":
		return "topaz/starlight-precise-2-6"
	}
	return model
}

// UpscaleParams configures a video upscale run.
type UpscaleParams struct {
	Input     string
	Model     string
	Target    string  // 720p | 1080p | 2k | 4k
	Factor    float64 // explicit factor; 0 derives it from Target
	Creative  bool    // flux: creativity=1
	Prompt    string  // flux creative guidance
	Sharpness int     // topaz precise 1-5
	Out       string
}

// Probe holds the source metadata Topaz requires.
type Probe struct {
	Width     int
	Height    int
	Frames    int
	FrameRate float64
	Container string
}

// ProbeSource reads dimensions, frame count, and frame rate via ffprobe.
func ProbeSource(input string) (Probe, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return Probe{}, fmt.Errorf("ffprobe is required for video upscaling: %w", err)
	}
	raw, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-count_packets",
		"-show_entries", "stream=width,height,r_frame_rate,nb_read_packets:format=format_name,duration",
		"-of", "json", input).Output()
	if err != nil {
		return Probe{}, fmt.Errorf("ffprobe %s: %w", input, err)
	}
	return parseProbe(raw, input)
}

func parseProbe(raw []byte, input string) (Probe, error) {
	var r struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Rate      string `json:"r_frame_rate"`
			NbPackets string `json:"nb_read_packets"`
		} `json:"streams"`
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return Probe{}, err
	}
	if len(r.Streams) == 0 || r.Streams[0].Width <= 0 {
		return Probe{}, fmt.Errorf("ffprobe found no video stream in %s", input)
	}
	s := r.Streams[0]
	p := Probe{Width: s.Width, Height: s.Height}
	if num, den, ok := strings.Cut(s.Rate, "/"); ok {
		n, _ := strconv.ParseFloat(num, 64)
		d, _ := strconv.ParseFloat(den, 64)
		if d > 0 {
			p.FrameRate = math.Round(n/d*1000) / 1000
		}
	}
	p.Frames, _ = strconv.Atoi(s.NbPackets)
	if p.Frames == 0 {
		dur, _ := strconv.ParseFloat(r.Format.Duration, 64)
		p.Frames = int(math.Round(dur * p.FrameRate))
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(strings.Split(input, "?")[0])), ".")
	if ext != "" {
		p.Container = ext
	} else {
		p.Container = strings.Split(r.Format.Name, ",")[0]
	}
	return p, nil
}

func targetShortSide(target string) (int, error) {
	switch strings.ToLower(target) {
	case "720p":
		return 720, nil
	case "", "1080p":
		return 1080, nil
	case "2k", "1440p":
		return 1440, nil
	case "4k", "2160p":
		return 2160, nil
	}
	return 0, fmt.Errorf("unknown --target %q (want 720p|1080p|2k|4k)", target)
}

// UpscaleInput builds the model-specific input. Exported for tests.
func UpscaleInput(p UpscaleParams, src Probe, videoRef string) (map[string]any, error) {
	model := UpscaleModel(p.Model)
	short, err := targetShortSide(p.Target)
	if err != nil {
		return nil, err
	}
	srcShort := min(src.Width, src.Height)
	factor := p.Factor
	if factor <= 0 && srcShort > 0 {
		factor = float64(short) / float64(srcShort)
	}
	switch model {
	case "black-forest-labs/flux-video-upscale":
		factor = math.Max(1.5, math.Min(3, factor))
		in := map[string]any{"input_video": videoRef, "upscale_factor": math.Round(factor*100) / 100, "creativity": 0}
		if p.Creative {
			in["creativity"] = 1
		}
		putStr(in, "prompt", p.Prompt)
		return in, nil
	case "topaz/starlight-fast-2", "topaz/starlight-precise-2-6":
		if !strings.HasPrefix(videoRef, "https://") {
			return nil, fmt.Errorf("%s needs an https:// video URL input (local files are not accepted); host the file or use --model flux", model)
		}
		res := "1920x1080"
		if short > 1080 {
			res = "3840x2160"
		}
		in := map[string]any{
			"video_uri":         videoRef,
			"source_container":  src.Container,
			"source_width":      src.Width,
			"source_height":     src.Height,
			"frame_count":       src.Frames,
			"frame_rate":        src.FrameRate,
			"target_resolution": res,
		}
		if model == "topaz/starlight-fast-2" {
			in["upscaling_factor"] = math.Max(1, math.Min(8, math.Round(factor*100)/100))
		} else {
			sharp := p.Sharpness
			if sharp < 1 || sharp > 5 {
				sharp = 5
			}
			in["sharpness"] = sharp
		}
		return in, nil
	}
	return nil, fmt.Errorf("unknown cloudflare upscale model %q (want flux|topaz-fast|topaz-precise)", p.Model)
}

// Upscale runs a catalog video upscaler and saves the result.
func Upscale(ctx context.Context, p UpscaleParams) (*MediaResult, error) {
	src, err := ProbeSource(p.Input)
	if err != nil {
		return nil, err
	}
	ref, err := refURI(p.Input)
	if err != nil {
		return nil, err
	}
	in, err := UpscaleInput(p, src, ref)
	if err != nil {
		return nil, err
	}
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := UpscaleModel(p.Model)
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
