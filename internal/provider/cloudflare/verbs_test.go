package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockAI emulates POST /accounts/acc/ai/run for every model the verbs use and
// records each request input so tests can assert on the wire format.
type mockAI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	inputs map[string]map[string]any
}

var (
	mock      *mockAI
	sampleMP4 string
)

func TestMain(m *testing.M) {
	mock = &mockAI{inputs: map[string]map[string]any{}}
	mock.srv = httptest.NewServer(http.HandlerFunc(mock.handle))
	os.Setenv("CLOUDFLARE_API_BASE_URL", mock.srv.URL)
	os.Setenv("CLOUDFLARE_API_TOKEN", "tok")
	os.Setenv("CLOUDFLARE_ACCOUNT_ID", "acc")
	dir, _ := os.MkdirTemp("", "cf-verbs-")
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		sampleMP4 = filepath.Join(dir, "sample.mp4")
		out, err := exec.Command("ffmpeg", "-v", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=size=320x240:rate=24:duration=2",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", sampleMP4).CombinedOutput()
		if err != nil {
			panic(string(out))
		}
	}
	code := m.Run()
	mock.srv.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

func (m *mockAI) handle(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/media/") {
		w.Write([]byte("MEDIA:" + strings.TrimPrefix(r.URL.Path, "/media/")))
		return
	}
	multipart := strings.HasPrefix(r.URL.Path, "/accounts/acc/ai/run/@cf/")
	if r.Header.Get("Authorization") != "Bearer tok" || (r.Header.Get("cf-aig-gateway-id") != "default") != multipart {
		http.Error(w, `{"errors":[{"code":10000,"message":"auth"}]}`, 401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/accounts/acc/ai/run/@cf/black-forest-labs/flux-2") {
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("prompt") == "" {
			http.Error(w, `{"errors":[{"code":1,"message":"multipart prompt required"}]}`, 400)
			return
		}
		m.record("flux-2", map[string]any{"prompt": r.FormValue("prompt"), "width": r.FormValue("width"), "refs": len(r.MultipartForm.File)})
		io.WriteString(w, `{"success":true,"result":{"image":"`+base64.StdEncoding.EncodeToString([]byte("PNGDATA"))+`"}}`)
		return
	}
	var body struct {
		Model string         `json:"model"`
		Input map[string]any `json:"input"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	m.record(body.Model, body.Input)
	media := func(field, name string) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"state": "Completed", "result": map[string]any{field: m.srv.URL + "/media/" + name}}})
	}
	switch body.Model {
	case "openai/gpt-image-2":
		media("image", "image.png")
	case "bytedance/seedance-2.5", "google/gemini-omni-flash":
		media("video", "video.mp4")
	case "elevenlabs/eleven-v3":
		media("audio", "speech.mp3")
	case "elevenlabs/music-v2":
		media("audio", "music.mp3")
	case "black-forest-labs/flux-video-upscale", "topaz/starlight-fast-2":
		media("video", "upscaled.mp4")
	case "@cf/openai/whisper-large-v3-turbo":
		io.WriteString(w, `{"success":true,"result":{"text":" hello brave new world ","vtt":"WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nhello brave new world\n\n","segments":[{"start":0,"end":1.5,"text":" hello brave new world","words":[{"word":"hello","start":0,"end":0.4},{"word":"brave","start":0.4,"end":0.8},{"word":"new","start":0.8,"end":1.1},{"word":"world","start":1.1,"end":1.5}]}]}}`)
	case "google/gemini-3-flash":
		io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"thinking","thought":true},{"text":"BRIEF: a test pattern"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":42},"gatewayMetadata":{"keySource":"Unified"}}`)
	default:
		w.WriteHeader(400)
		io.WriteString(w, `{"errors":[{"code":5007,"message":"No such model"}]}`)
	}
}

func (m *mockAI) record(model string, in map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs[model] = in
}

func (m *mockAI) input(model string) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inputs[model]
}

func readOut(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func needFFmpeg(t *testing.T) {
	if sampleMP4 == "" {
		t.Skip("ffmpeg not installed")
	}
}

func TestImageGPTWithReference(t *testing.T) {
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.png")
	os.WriteFile(ref, []byte("REF"), 0o644)
	out := filepath.Join(dir, "o.png")
	res, err := GenerateImage(context.Background(), ImageParams{Prompt: "cube", Model: "gpt-image-2", Size: "auto", Quality: "low", OutputFormat: "png", Seed: -1, Refs: []string{ref}, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "openai/gpt-image-2" || readOut(t, out) != "MEDIA:image.png" {
		t.Fatalf("model=%s out=%s", res.Model, readOut(t, out))
	}
	in := mock.input("openai/gpt-image-2")
	imgs, _ := in["images"].([]any)
	if len(imgs) != 1 || imgs[0] != "data:image/png;base64,UkVG" || in["quality"] != "low" {
		t.Fatalf("input = %v", in)
	}
}

func TestImageFlux2Multipart(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f.png")
	if _, err := GenerateImage(context.Background(), ImageParams{Prompt: "fox", Model: "flux-2-klein-9b", Size: "1024x768", Seed: -1, Out: out}); err != nil {
		t.Fatal(err)
	}
	if readOut(t, out) != "PNGDATA" {
		t.Fatalf("out = %q", readOut(t, out))
	}
	if in := mock.input("flux-2"); in["prompt"] != "fox" || in["width"] != "1024" {
		t.Fatalf("form = %v", in)
	}
}

func TestVideoSeedanceAndOmni(t *testing.T) {
	out := filepath.Join(t.TempDir(), "v.mp4")
	res, err := GenerateVideo(context.Background(), VideoParams{Prompt: "pan", Model: "auto", Duration: 5, Resolution: "720p", AspectRatio: "9:16", GenerateAudio: true, Seed: 7, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.URL == "" || readOut(t, out) != "MEDIA:video.mp4" {
		t.Fatalf("res=%+v", res)
	}
	in := mock.input("bytedance/seedance-2.5")
	if in["duration"] != float64(5) || in["aspect_ratio"] != "9:16" || in["seed"] != float64(7) || in["generate_audio"] != true {
		t.Fatalf("seedance input = %v", in)
	}
	if _, err := GenerateVideo(context.Background(), VideoParams{Prompt: "fish", Model: "omni", AspectRatio: "16:9", Resolution: "360p", Seed: -1, Out: out}); err != nil {
		t.Fatal(err)
	}
	if in := mock.input("google/gemini-omni-flash"); in["text"] != "fish" || in["prompt"] != nil {
		t.Fatalf("omni input = %v", in)
	}
	if _, err := VideoInput(VideoParams{Model: "omni", AspectRatio: "1:1", Seed: -1}); err == nil {
		t.Fatal("omni accepted 1:1")
	}
}

func TestSpeechAndMusic(t *testing.T) {
	dir := t.TempDir()
	if _, err := Speech(context.Background(), SpeechParams{Text: "hi", VoiceID: "JBFqnCBsd6RMkjVDRZzb", Model: "eleven_v3", OutputFormat: "mp3_44100_128", Out: filepath.Join(dir, "s.mp3")}); err != nil {
		t.Fatal(err)
	}
	if in := mock.input("elevenlabs/eleven-v3"); in["voice_id"] != "JBFqnCBsd6RMkjVDRZzb" || in["text"] != "hi" {
		t.Fatalf("speech input = %v", in)
	}
	if _, err := Music(context.Background(), MusicParams{Prompt: "lofi", MusicLengthMs: 10000, ForceInstrumental: true, Out: filepath.Join(dir, "m.mp3")}); err != nil {
		t.Fatal(err)
	}
	if in := mock.input("elevenlabs/music-v2"); in["music_length_ms"] != float64(10000) || in["force_instrumental"] != true {
		t.Fatalf("music input = %v", in)
	}
	if readOut(t, filepath.Join(dir, "m.mp3")) != "MEDIA:music.mp3" {
		t.Fatal("music not downloaded")
	}
}

func TestTranscribeFormats(t *testing.T) {
	needFFmpeg(t)
	for format, want := range map[string]string{
		"text": "hello brave new world",
		"srt":  "1\n00:00:00,000 --> 00:00:01,500\nhello brave new world\n\n",
		"vtt":  "WEBVTT",
		"json": `{"text":"hello brave new world"}`,
	} {
		res, err := Transcribe(context.Background(), TranscribeParams{File: sampleMP4, Model: "whisper-1", Format: format, Translate: true})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(res.Transcript, want) {
			t.Errorf("%s: got %q", format, res.Transcript)
		}
	}
	in := mock.input("@cf/openai/whisper-large-v3-turbo")
	audio, _ := base64.StdEncoding.DecodeString(in["audio"].(string))
	if len(audio) < 1000 || in["task"] != "translate" {
		t.Fatalf("whisper input: %d audio bytes, task=%v", len(audio), in["task"])
	}
}

func TestCaptionBurnsSubtitles(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "captioned.mp4")
	srt := filepath.Join(dir, "c.srt")
	res, err := Caption(context.Background(), CaptionParams{Input: sampleMP4, Out: out, MaxWords: 2, FontColor: "#FFCC00", Position: "top", SubtitlesOut: srt})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.SRT, "hello brave") || !strings.Contains(res.SRT, "new world") {
		t.Fatalf("srt = %q", res.SRT)
	}
	p, err := ProbeSource(out)
	if err != nil || p.Width != 320 {
		t.Fatalf("captioned output probe = %+v err=%v", p, err)
	}
	if readOut(t, srt) != res.SRT {
		t.Fatal("srt-out mismatch")
	}
}

func TestUpscaleFluxAndTopaz(t *testing.T) {
	needFFmpeg(t)
	out := filepath.Join(t.TempDir(), "up.mp4")
	if _, err := Upscale(context.Background(), UpscaleParams{Input: sampleMP4, Target: "720p", Out: out}); err != nil {
		t.Fatal(err)
	}
	in := mock.input("black-forest-labs/flux-video-upscale")
	if in["upscale_factor"] != float64(3) || !strings.HasPrefix(in["input_video"].(string), "data:video/mp4;base64,") {
		t.Fatalf("flux input factor=%v", in["upscale_factor"])
	}
	src, err := ProbeSource(sampleMP4)
	if err != nil || src.Frames != 48 || src.FrameRate != 24 || src.Container != "mp4" {
		t.Fatalf("probe = %+v err=%v", src, err)
	}
	tin, err := UpscaleInput(UpscaleParams{Model: "topaz-fast", Target: "1080p"}, src, "https://x/v.mp4")
	if err != nil || tin["upscaling_factor"] != 4.5 || tin["target_resolution"] != "1920x1080" || tin["frame_count"] != 48 {
		t.Fatalf("topaz input = %v err=%v", tin, err)
	}
	if _, err := UpscaleInput(UpscaleParams{Model: "topaz-fast"}, src, "data:video/mp4;base64,AA"); err == nil {
		t.Fatal("topaz accepted a non-https input")
	}
}

func TestAnalyzeVideoAndFrames(t *testing.T) {
	needFFmpeg(t)
	res, err := Analyze(context.Background(), AnalyzeParams{Input: sampleMP4, Prompt: "brief", Temperature: 1, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "BRIEF: a test pattern" || !strings.Contains(res.Endpoint, "video") {
		t.Fatalf("res = %+v", res)
	}
	parts := mock.input("google/gemini-3-flash")["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["inlineData"].(map[string]any)["mimeType"] != "video/mp4" {
		t.Fatalf("video parts = %d", len(parts))
	}
	res, err = Analyze(context.Background(), AnalyzeParams{Input: sampleMP4, Prompt: "brief", Mode: "frames", Frames: 4, MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	parts = mock.input("google/gemini-3-flash")["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	text := parts[len(parts)-1].(map[string]any)["text"].(string)
	if len(parts) != 5 || !strings.Contains(text, "hello brave new world") {
		t.Fatalf("frames parts=%d text=%q", len(parts), text)
	}
}
