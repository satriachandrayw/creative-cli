package cmd

import (
	"fmt"
	"time"

	"github.com/satriachandrayw/creative-cli/internal/core"
	"github.com/satriachandrayw/creative-cli/internal/provider/cloudflare"
	"github.com/satriachandrayw/creative-cli/internal/provider/falpipe"
	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		c := &cobra.Command{
			Use:   "upscale <input>",
			Short: "Upscale a video via Cloudflare AI (flux | topaz-fast | topaz-precise) or fal (bytedance | topaz | flashvsr | seedvr).",
			Args:  cobra.ExactArgs(1),
			RunE:  runUpscale,
		}
		c.Flags().String("model", "auto", "cloudflare: flux | topaz-fast | topaz-precise; fal: bytedance | topaz | flashvsr | seedvr (auto = flux / bytedance)")
		c.Flags().String("target", "1080p", "720p | 1080p | 2k | 4k")
		c.Flags().Int("fps", 30, "target fps (topaz: 16-60; others: 30 | 60)")
		c.Flags().String("bytedance-preset", "aigc", "general | ugc | short_series | aigc | old_film")
		c.Flags().String("bytedance-tier", "standard", "fast | standard | pro")
		c.Flags().String("bytedance-fidelity", "high", "high | medium")
		c.Flags().String("topaz-model", "Proteus", "Topaz model name, e.g. Proteus, \"Gaia HQ\", \"Starlight Fast 2\"")
		c.Flags().Float64("topaz-factor", 0, "Topaz explicit upscale factor 1.0-4.0 (overrides --target)")
		c.Flags().Float64("topaz-recover-detail", -1, "Topaz recover_detail 0-1 (default: model default)")
		c.Flags().Float64("factor", 0, "cloudflare: explicit upscale factor (overrides --target; flux 1.5-3, topaz-fast 1-8)")
		c.Flags().Bool("creative", false, "cloudflare flux: creative mode (adds detail; guided by --prompt)")
		c.Flags().String("prompt", "", "cloudflare flux creative-mode guidance")
		c.Flags().Int("sharpness", 5, "cloudflare topaz-precise sharpness 1-5")
		addOutFlag(c)
		addAsyncFlags(c)
		addProviderFlag(c, "cloudflare")
		root.AddCommand(c)
	})
}

func runUpscale(cmd *cobra.Command, args []string) error {
	common := readCommon(cmd)
	model := flagStr(cmd, "model")
	switch provider := flagStr(cmd, "provider"); provider {
	case "cloudflare":
		if flagBool(cmd, "no-wait") {
			return fmt.Errorf("--no-wait is not supported with the cloudflare provider (runs are synchronous)")
		}
		started := time.Now()
		core.Progress("upscaling %s with %s via cloudflare", args[0], cloudflare.UpscaleModel(model))
		res, err := cloudflare.Upscale(cmd.Context(), cloudflare.UpscaleParams{
			Input:     args[0],
			Model:     model,
			Target:    flagStr(cmd, "target"),
			Factor:    flagFloat(cmd, "factor"),
			Creative:  flagBool(cmd, "creative"),
			Prompt:    flagStr(cmd, "prompt"),
			Sharpness: flagInt(cmd, "sharpness"),
			Out:       flagStr(cmd, "out"),
		})
		if err != nil {
			return err
		}
		return emitMedia(common, started, res, "video", args[0], res.URL, flagStr(cmd, "out"))
	case "fal":
		if model == "auto" {
			model = "bytedance"
		}
	default:
		return fmt.Errorf("unknown upscale provider %q (want cloudflare|fal)", provider)
	}
	env, raw, err := falpipe.Upscale(falpipe.UpscaleOpts{
		Input:              args[0],
		Model:              model,
		Target:             flagStr(cmd, "target"),
		Fps:                flagInt(cmd, "fps"),
		Out:                flagStr(cmd, "out"),
		NoWait:             flagBool(cmd, "no-wait"),
		PollIntervalSecs:   flagInt(cmd, "poll-interval-secs"),
		MaxWaitSecs:        flagInt(cmd, "max-wait-secs"),
		BytedancePreset:    flagStr(cmd, "bytedance-preset"),
		BytedanceTier:      flagStr(cmd, "bytedance-tier"),
		BytedanceFidelity:  flagStr(cmd, "bytedance-fidelity"),
		TopazModel:         flagStr(cmd, "topaz-model"),
		TopazFactor:        flagFloat(cmd, "topaz-factor"),
		TopazRecoverDetail: flagFloat(cmd, "topaz-recover-detail"),
	}, common.pretty, common.raw)
	if err != nil {
		return err
	}
	return core.Emit(env, raw, common.pretty, common.raw)
}
