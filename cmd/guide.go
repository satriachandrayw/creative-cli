package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// agentGuide is shown by `creative --help` and `creative guide`. It is written
// for a consuming AI agent: it explains the mental model, the verb catalog, the
// credentials, the machine-readable output contract, and — most usefully — the
// DAG pipeline recipes that chain verbs into finished creatives.
const agentGuide = `creative — one binary to make, transform, and analyze media, then hand off to publishing.

Designed for AGENT CONSUMPTION. Non-interactive: every input is a flag, every
result is JSON on stdout. There is no prompt, menu, or confirmation. Progress and
errors go to stderr; stdout is pure JSON so you can pipe it into jq.

MENTAL MODEL
  Pick a verb by the ASSET you want out. Providers are an implementation detail
  selected by a smart --provider default you can override.

    MAKE                              TRANSFORM                    ANALYZE
    creative image    text->image     creative upscale  video->    creative transcribe audio/video->text
    creative video    text/ref->video creative caption  burn text  creative analyze    video->brief
    creative speech   text->voiceover creative dub      re-language
    creative music    text->music
    creative sfx      text->sound fx
    creative voice    clone/design

VERB CATALOG (default provider · alternatives)
  Cloudflare AI is the default wherever its catalog has a model; one Cloudflare
  credential (or a wrangler login) replaces the per-vendor keys for those verbs.
  image      cloudflare openai/gpt-image-2   --model flux-2-klein-9b|flux-2-dev|<catalog id>; --image REF edits · --provider openai
  video      cloudflare bytedance/seedance-2.5  --model omni|omni-1.1-flash|seedance-2.0 · --provider segmind|fal
  speech     cloudflare elevenlabs/eleven-v3    --text --voice-id --model · --provider elevenlabs
  music      cloudflare elevenlabs/music-v2     --prompt (--duration-seconds, --instrumental) · --provider elevenlabs
  sfx        ElevenLabs (no Cloudflare model)   ELEVENLABS_API_KEY  --prompt (--duration-seconds)
  voice      ElevenLabs (no Cloudflare model)   ELEVENLABS_API_KEY  --audio sample(s) --name (clone) | --prompt (design)
  upscale    cloudflare flux-video-upscale      INPUT; --model flux|topaz-fast|topaz-precise (topaz needs an https URL) --target 720p|1080p|2k|4k · --provider fal
  caption    cloudflare Whisper + local ffmpeg  INPUT --out; --position --font --font-color --max-words · --provider veed|zapcap
  dub        ElevenLabs (no Cloudflare model)   ELEVENLABS_API_KEY  INPUT --language <iso>
  transcribe cloudflare @cf/openai/whisper-large-v3-turbo  INPUT; --format text|srt|vtt|json|verbose_json · --provider openai
  analyze    cloudflare google/gemini-3-flash   INPUT (file/URL; --mode video|frames) -> brief · --provider fal (YouTube URLs)

CLOUDFLARE CREDENTIALS
  CLOUDFLARE_API_TOKEN (Workers AI Read + AI Gateway permissions), else the
  active wrangler session via 'wrangler auth token' (run 'wrangler login' once;
  WRANGLER_BIN overrides how wrangler is invoked). CLOUDFLARE_ACCOUNT_ID is
  optional when the token sees a single account. Requests go through AI Gateway
  CLOUDFLARE_AI_GATEWAY_ID (default "default"; "none" omits it). Third-party
  models (openai/, google/, elevenlabs/, bytedance/, black-forest-labs/, topaz/)
  bill against prepaid Unified Billing credits on the Cloudflare account.
  Cloudflare runs are synchronous: --no-wait is rejected for --provider cloudflare.

OTHER CREDENTIALS  (resolved: env var first, then /run/secrets/<NAME>)
  OPENAI_API_KEY  CODEX_LB_API_KEY  SEGMIND_API_KEY  ELEVENLABS_API_KEY  FAL_KEY  ZAPCAP_API_KEY

IMAGE BACKENDS (--provider openai)
  Set CODEX_LB_BASE_URL (including /v1) + CODEX_LB_API_KEY to prefer codex-lb.
  Partial Codex configuration is an error. With neither, use OPENAI_API_KEY and
  OPENAI_BASE_URL (default https://api.openai.com/v1; alias OPENAI_API_URL).
  CODEX_LB_EXHAUSTION_CODES explicitly allowlists structured pool errors that
  permit OpenAI fallback. Never retry another backend on timeout/network loss.
  Missing key error format: "<NAME> missing; export it or provide /run/secrets/<NAME>".

OUTPUT CONTRACT
  stdout: one JSON object (the "envelope"): {provider, endpoint, request_id, model,
          input, output, usage, estimated_cost, elapsed_seconds, out}. --pretty to
          indent, --raw to emit the untouched provider payload instead.
  stderr: human progress (upload / queued / polling / done in Xs).
  Binary assets (image/audio/video) require --out. Text results print to stdout
  (inside output) or write to --out.
  Async verbs (video, upscale, caption, dub, analyze) accept
  --poll-interval-secs, --max-wait-secs, and --no-wait (submit only, returns request_id).

DAG PIPELINE RECIPES (chain verbs; feed one --out into the next input)
  Static ad from a product photo:
    creative image  --prompt "matte black bottle on wet concrete, softbox" --out hero.png

  Talking-avatar UGC ad (image -> video -> voiceover -> burned captions):
    creative image  --prompt "founder headshot, studio light" --out face.png
    creative video  --prompt "founder talks to camera, subtle nod" --image face.png --out talk.mp4
    creative speech --text "This changed how we ship." --voice-id <id> --out vo.mp3
    ffmpeg -i talk.mp4 -i vo.mp3 -map 0:v -map 1:a -c:v copy -shortest talk_vo.mp4
    creative caption talk_vo.mp4 --out ad.mp4      # Whisper timing + burned subtitles
    # then normalize and hand to meta-ads

  Reference-driven video, then finish to 4K social:
    creative video   --prompt "slow dolly across the product" --image hero.png --out clip.mp4
    creative upscale clip.mp4 --target 4k --out clip_4k.mp4
    creative caption clip_4k.mp4 --out final.mp4

  Low-cost Gemini Omni Flash draft with synchronized audio:
    creative video --model omni --prompt "handheld product demo" --resolution 360p --out draft.mp4

  Study a viral reference, then recreate it:
    creative analyze reference.mp4 | jq -r .output > brief.txt
    creative analyze https://youtube.com/watch?v=... --provider fal   # YouTube needs fal
    # use the brief as the --prompt basis for creative video

  Sound design bed:
    creative music --prompt "upbeat driving synth, 120bpm" --out track.mp3
    creative sfx   --prompt "whoosh transition" --out whoosh.mp3

OUT OF SCOPE (by design): local ffmpeg stitching/normalization and publishing
  (the cloudflare caption burn is the one built-in ffmpeg step).
  Assemble with system ffmpeg (H.264 yuv420p + AAC + +faststart for Meta), then
  upload via the meta-ads CLI (paid) or post organically.

Run 'creative <verb> --help' for a verb's exact flags.`

func init() {
	register(func(root *cobra.Command) {
		root.AddCommand(&cobra.Command{
			Use:   "guide",
			Short: "Print the full agent usage guide (verbs, credentials, output contract, pipeline recipes).",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(agentGuide)
				return nil
			},
		})
	})
}
