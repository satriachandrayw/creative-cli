package cmd

import (
	"fmt"
	"time"

	"github.com/satriachandrayw/creative-cli/internal/core"
	"github.com/satriachandrayw/creative-cli/internal/provider/cloudflare"
	"github.com/satriachandrayw/creative-cli/internal/provider/falvideo"
	"github.com/satriachandrayw/creative-cli/internal/provider/segmind"
	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		c := &cobra.Command{
			Use:   "video",
			Short: "Generate a video via Cloudflare AI (Seedance 2.5, Gemini Omni Flash), Segmind, or fal.",
			Args:  cobra.NoArgs,
			RunE:  runVideo,
		}
		c.Flags().String("prompt", "", "text prompt (required)")
		c.Flags().String("model", "auto", "auto | cloudflare: seedance-2.5 | seedance-2.0 | omni | omni-1.1-flash | <catalog id>; segmind: mini | fast | standard | 2.5; fal: omni-1.1-flash")
		addRefFlags(c)
		c.Flags().String("first-frame", "", "starting frame image (path or url); cannot combine with --image")
		c.Flags().String("last-frame", "", "ending frame image (path or url); requires --first-frame")
		c.Flags().Int("duration-seconds", 5, "requested video duration in seconds")
		c.Flags().String("resolution", "720p", "360p | 480p | 720p | 1080p | 4k (provider-specific)")
		c.Flags().String("aspect-ratio", "16:9", "output aspect ratio, e.g. 16:9, 9:16, 1:1")
		c.Flags().Bool("no-generate-audio", false, "disable provider-generated synchronized audio")
		c.Flags().Int("seed", -1, "seed; -1 lets the provider choose")
		addOutFlag(c)
		addAsyncFlags(c)
		addProviderFlag(c, "cloudflare")
		root.AddCommand(c)
	})
}

func runVideo(cmd *cobra.Command, _ []string) error {
	common := readCommon(cmd)
	prompt := flagStr(cmd, "prompt")
	if prompt == "" {
		return fmt.Errorf("--prompt is required")
	}

	provider := flagStr(cmd, "provider")
	model, err := videoModel(provider, flagStr(cmd, "model"))
	if err != nil {
		return err
	}
	if provider == "cloudflare" {
		if flagBool(cmd, "no-wait") {
			return fmt.Errorf("--no-wait is not supported with the cloudflare provider (runs are synchronous)")
		}
		started := time.Now()
		core.Progress("generating video with %s via cloudflare", model)
		images := flagStrs(cmd, "image")
		res, err := cloudflare.GenerateVideo(cmd.Context(), cloudflare.VideoParams{
			Prompt:        prompt,
			Model:         model,
			Images:        images,
			Videos:        flagStrs(cmd, "video"),
			Audios:        flagStrs(cmd, "audio"),
			FirstFrame:    flagStr(cmd, "first-frame"),
			LastFrame:     flagStr(cmd, "last-frame"),
			Duration:      flagInt(cmd, "duration-seconds"),
			Resolution:    flagStr(cmd, "resolution"),
			AspectRatio:   flagStr(cmd, "aspect-ratio"),
			GenerateAudio: !flagBool(cmd, "no-generate-audio"),
			Seed:          flagInt(cmd, "seed"),
			Out:           flagStr(cmd, "out"),
		})
		if err != nil {
			return err
		}
		elapsed := roundSecs(started)
		core.Progress("done in %.3fs", elapsed)
		kind := "prompt"
		if len(images) > 0 || flagStr(cmd, "first-frame") != "" || len(flagStrs(cmd, "video")) > 0 {
			kind = "reference"
		}
		return core.Emit(&core.Envelope{
			Provider:       "cloudflare",
			Endpoint:       res.Endpoint,
			Model:          res.Model,
			Input:          &core.InputInfo{Kind: kind, Source: prompt},
			Output:         res.URL,
			ElapsedSeconds: elapsed,
			Out:            flagStr(cmd, "out"),
		}, res.Raw, common.pretty, common.raw)
	}
	if provider == "fal" {
		if flagInt(cmd, "seed") != -1 {
			return fmt.Errorf("fal omni-1.1-flash does not support --seed")
		}
		env, raw, err := falvideo.Generate(falvideo.Options{
			Prompt:           prompt,
			Model:            model,
			Images:           flagStrs(cmd, "image"),
			Videos:           flagStrs(cmd, "video"),
			Audios:           flagStrs(cmd, "audio"),
			FirstFrame:       flagStr(cmd, "first-frame"),
			LastFrame:        flagStr(cmd, "last-frame"),
			Duration:         flagInt(cmd, "duration-seconds"),
			Resolution:       flagStr(cmd, "resolution"),
			AspectRatio:      flagStr(cmd, "aspect-ratio"),
			GenerateAudio:    !flagBool(cmd, "no-generate-audio"),
			Out:              flagStr(cmd, "out"),
			NoWait:           flagBool(cmd, "no-wait"),
			PollIntervalSecs: flagInt(cmd, "poll-interval-secs"),
			MaxWaitSecs:      flagInt(cmd, "max-wait-secs"),
		})
		if err != nil {
			return err
		}
		return core.Emit(env, raw, common.pretty, common.raw)
	}

	env, raw, err := segmind.Generate(segmind.GenerateOpts{
		Prompt:           prompt,
		Model:            model,
		Images:           flagStrs(cmd, "image"),
		Videos:           flagStrs(cmd, "video"),
		Audios:           flagStrs(cmd, "audio"),
		FirstFrame:       flagStr(cmd, "first-frame"),
		LastFrame:        flagStr(cmd, "last-frame"),
		Duration:         flagInt(cmd, "duration-seconds"),
		Resolution:       flagStr(cmd, "resolution"),
		AspectRatio:      flagStr(cmd, "aspect-ratio"),
		GenerateAudio:    !flagBool(cmd, "no-generate-audio"),
		Seed:             flagInt(cmd, "seed"),
		Out:              flagStr(cmd, "out"),
		NoWait:           flagBool(cmd, "no-wait"),
		PollIntervalSecs: flagInt(cmd, "poll-interval-secs"),
		MaxWaitSecs:      flagInt(cmd, "max-wait-secs"),
	}, common.pretty, common.raw)
	if err != nil {
		return err
	}
	return core.Emit(env, raw, common.pretty, common.raw)
}

func videoModel(provider, model string) (string, error) {
	switch provider {
	case "cloudflare":
		return cloudflare.VideoModel(model), nil
	case "segmind":
		if model == "auto" {
			return "mini", nil
		}
		return model, nil
	case "fal":
		if model == "auto" {
			return "omni-1.1-flash", nil
		}
		return model, nil
	default:
		return "", fmt.Errorf("unknown video provider %q (want cloudflare|segmind|fal)", provider)
	}
}
