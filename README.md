# creative

One binary to **make, transform, and analyze** media — image, video, and audio —
then hand the finished asset off to publishing (Meta ads or organic).

Built for **agent consumption**: fully non-interactive, every input is a flag,
every result is JSON on stdout. There is no prompt, menu, or confirmation.
`stdout` is pure JSON (pipe it into `jq`); `stderr` carries human progress.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/satriachandrayw/creative-cli/main/scripts/install.sh | bash
```

Or build from source (Go 1.25+):

```bash
go build -o creative .
```

## Cloudflare-first

This fork routes every verb Cloudflare's AI catalog can serve through
**Cloudflare AI** (`POST /accounts/{id}/ai/run`), so a single Cloudflare
credential — or simply an active `wrangler login` — replaces most per-vendor
keys. Third-party models (OpenAI, Google, ElevenLabs, ByteDance, Black Forest
Labs, Topaz) run through AI Gateway and bill against prepaid **Unified Billing**
credits on your Cloudflare account; Workers AI models (`@cf/...`) bill as
Workers AI usage. Every original provider is still available via `--provider`.

```bash
wrangler login                      # once; or export CLOUDFLARE_API_TOKEN=...
creative image  --prompt "a red cube on white" --out cube.png
creative speech --text "Hello" --out hello.mp3
creative transcribe hello.mp3 --format srt
```

| Setting | Purpose |
|---------|---------|
| `CLOUDFLARE_API_TOKEN` | API token with **Workers AI Read** (+ AI Gateway) permission. Falls back to `wrangler auth token` when unset. |
| `CLOUDFLARE_ACCOUNT_ID` | Optional when the credential sees exactly one account. |
| `CLOUDFLARE_AI_GATEWAY_ID` | Gateway to route through (default `default`, auto-created; `none` omits the header). |
| `WRANGLER_BIN` | How to invoke wrangler (default: `wrangler` on `PATH`, else `npx -y wrangler`). |

Both variables also resolve from `/run/secrets/<NAME>`.
Cloudflare runs are synchronous, so `--no-wait` is rejected for `--provider cloudflare`.

## Verbs

| Verb | Default (Cloudflare model) | Other providers | What it does |
|------|----------------------------|-----------------|--------------|
| `creative image` | `openai/gpt-image-2`; `--model flux-2-klein-9b`, `flux-2-dev`, any catalog id | `openai` (codex-lb / OpenAI) | text→image; `--image REF` for edits |
| `creative video` | `bytedance/seedance-2.5`; `--model omni`, `omni-1.1-flash`, `seedance-2.0` | `segmind`, `fal` | text/reference→video with generated audio |
| `creative speech` | `elevenlabs/eleven-v3` (any `eleven_*` model id maps over) | `elevenlabs` | text→voiceover |
| `creative music` | `elevenlabs/music-v2` | `elevenlabs` | text→music |
| `creative sfx` | — *(no Cloudflare model)* | `elevenlabs` (`ELEVENLABS_API_KEY`) | text→sound effect |
| `creative voice` | — *(no Cloudflare model)* | `elevenlabs` (`ELEVENLABS_API_KEY`) | clone / design a voice |
| `creative upscale` | `black-forest-labs/flux-video-upscale`; `--model topaz-fast`, `topaz-precise` (https URL input) | `fal` | video→higher-resolution video |
| `creative caption` | `@cf/openai/whisper-large-v3-turbo` word timings + local ffmpeg burn | `veed`, `zapcap` | burn subtitles onto video |
| `creative dub` | — *(no Cloudflare model)* | `elevenlabs` (`ELEVENLABS_API_KEY`) | re-language a video |
| `creative transcribe` | `@cf/openai/whisper-large-v3-turbo` | `openai` | audio/video→text (text, json, srt, vtt, verbose_json) |
| `creative analyze` | `google/gemini-3-flash` (inline video ≤20 MB, else sampled frames + transcript) | `fal` (needed for YouTube URLs) | video→recreation brief |

`ffmpeg`/`ffprobe` on `PATH` are needed for `transcribe` (audio normalization),
`caption`, `upscale` and `analyze --mode frames`.

Non-Cloudflare credentials resolve from the environment first, then
`/run/secrets/<NAME>` — the same convention every sibling CLI uses.

### Verifying against your account

`scripts/smoke-cloudflare.sh` runs every Cloudflare-backed verb end to end and
checks the outputs with `ffprobe` (`SMOKE_SKIP="video upscale"` skips the
priciest ones). `go test ./...` covers the same flows against a mock `/ai/run`.

## Image backend selection (`--provider openai`)

Set both `CODEX_LB_BASE_URL` and `CODEX_LB_API_KEY` to prefer your load balancer.
The base URL includes the API prefix, for example `https://codex-lb.cakrawala.ai/v1`.
Do not use the text Responses prefix `/backend-api/codex` for Images API calls.
If only one Codex setting is present, the command reports a configuration error.

Without Codex settings, images use `OPENAI_API_KEY` and `OPENAI_BASE_URL`
(default `https://api.openai.com/v1`). The existing `OPENAI_API_URL` variable
remains a lower-priority alias for the image base URL. Other verbs are unchanged.

```bash
export CODEX_LB_BASE_URL=https://codex-lb.cakrawala.ai/v1
# Supply CODEX_LB_API_KEY through your secret manager.
creative image --prompt "a red cube on white" --out cube.png
```

Runtime fallback is disabled unless `CODEX_LB_EXHAUSTION_CODES` contains an
explicit comma-separated allowlist of your server's pool-exhaustion error codes.
Only a matching structured error can select direct OpenAI, which also requires
`OPENAI_API_KEY`. Timeouts, connection loss, and other errors never trigger a
second generation: the first server might still be working. Do not allowlist
generic rate-limit or server-error codes.

Image requests have a ten-minute total timeout, including downloads and fallback.
Use `--timeout 25m` when your image backend needs more time. Backend selection is
automatic; the image command does not need a `--provider` flag.

## Output contract

`stdout` is a single JSON envelope:

```json
{"provider":"openai","endpoint":"/v1/images/generations","model":"gpt-image-2",
 "input":{"kind":"prompt","source":"..."},"output":"image",
 "elapsed_seconds":4.12,"out":"hero.png"}
```

`--pretty` indents it; `--raw` emits the untouched provider payload instead.
Async verbs (`video`, `upscale`, `caption`, `dub`, `analyze`) accept
`--poll-interval-secs`, `--max-wait-secs`, and `--no-wait`.

## Video providers

Cloudflare Seedance 2.5 is the default (4–30 s, 480p/720p). `--model omni`
selects Gemini Omni Flash on Cloudflare (16:9 or 9:16, 360p–4K, no duration
control). Segmind Seedance (`--provider segmind`) and fal Gemini Omni Flash 1.1
(`--provider fal`) remain available; `--model auto` resolves per provider.

```bash
creative video --provider fal --prompt "handheld product demo" \
  --resolution 360p --duration-seconds 3 --out draft.mp4
```

fal Omni accepts 3–10 second output at 360p, 720p, 1080p, or 4K. With no references,
the command uses text-to-video. `--first-frame` selects image-to-video.
Repeatable `--image` and `--video` flags select multimodal reference-to-video.
Omni always generates synchronized audio and does not accept `--audio` or
`--seed`.

## Video upscaling

On Cloudflare, `flux` (default) accepts local files or URLs up to 20 s / 50 MB
and clamps the factor to 1.5–3×; `topaz-fast` / `topaz-precise` require an
`https://` input and send ffprobe-derived frame metadata. On fal, Topaz and
FlashVSR calculate their scale factor from the input dimensions, so
`--target 1080p` means 1080p instead of a fixed 2× resize. Video upscaling
requires `ffprobe` on `PATH`; Nix installations provide it automatically.

## Pipelines

`creative --help` (or `creative guide`) prints the full agent playbook, including
DAG recipes that chain verbs into finished creatives — e.g. image → video →
voiceover → burned captions, then normalize with ffmpeg and upload via the
`meta-ads` CLI.

## Scope

Local media assembly (ffmpeg stitching/normalization) and publishing are
deliberately **out of scope** — `creative` produces the assets; you assemble with
system `ffmpeg` (H.264 `yuv420p` + AAC + `+faststart` for Meta) and publish
separately.
