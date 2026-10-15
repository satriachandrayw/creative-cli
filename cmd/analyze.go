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
			Use:   "analyze <video>",
			Short: "Analyze a video into a recreation brief via Gemini on Cloudflare AI (default) or fal's OpenRouter video route.",
			Args:  cobra.ExactArgs(1),
			RunE:  runAnalyze,
		}
		c.Flags().String("model", "google/gemini-3-flash", "Gemini model id (Cloudflare catalog or OpenRouter)")
		c.Flags().Float64("temperature", 1.0, "sampling temperature (0-2)")
		c.Flags().Int("max-output-tokens", 4096, "max output tokens")
		c.Flags().String("prompt", "", "extra instruction appended to the brief prompt")
		c.Flags().String("mode", "auto", "cloudflare: auto | video (inline, <=20MB) | frames (sampled stills + Whisper transcript)")
		c.Flags().Int("frames", 12, "cloudflare frames mode: number of stills to sample")
		addAsyncFlags(c)
		addProviderFlag(c, "cloudflare")
		root.AddCommand(c)
	})
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	common := readCommon(cmd)
	temperature, _ := cmd.Flags().GetFloat64("temperature")
	switch provider := flagStr(cmd, "provider"); provider {
	case "cloudflare":
		mode := flagStr(cmd, "mode")
		if mode == "auto" {
			mode = ""
		}
		started := time.Now()
		core.Progress("analyzing video with %s via cloudflare", flagStr(cmd, "model"))
		res, err := cloudflare.Analyze(cmd.Context(), cloudflare.AnalyzeParams{
			Input:           args[0],
			Model:           flagStr(cmd, "model"),
			Prompt:          falpipe.BuildAnalyzePrompt(flagStr(cmd, "prompt")),
			Temperature:     temperature,
			MaxOutputTokens: flagInt(cmd, "max-output-tokens"),
			Mode:            mode,
			Frames:          flagInt(cmd, "frames"),
		})
		if err != nil {
			return err
		}
		elapsed := roundSecs(started)
		core.Progress("done in %.3fs", elapsed)
		return core.Emit(&core.Envelope{
			Provider:       "cloudflare",
			Endpoint:       res.Endpoint,
			Model:          res.Model,
			Input:          &core.InputInfo{Kind: "video", Source: args[0]},
			Output:         res.Text,
			Usage:          res.Usage,
			ElapsedSeconds: elapsed,
		}, res.Raw, common.pretty, common.raw)
	case "fal":
	default:
		return fmt.Errorf("unknown analyze provider %q (want cloudflare|fal)", provider)
	}
	env, raw, err := falpipe.Analyze(falpipe.AnalyzeOpts{
		Input:            args[0],
		Model:            flagStr(cmd, "model"),
		Temperature:      temperature,
		MaxOutputTokens:  flagInt(cmd, "max-output-tokens"),
		Prompt:           flagStr(cmd, "prompt"),
		NoWait:           flagBool(cmd, "no-wait"),
		PollIntervalSecs: flagInt(cmd, "poll-interval-secs"),
		MaxWaitSecs:      flagInt(cmd, "max-wait-secs"),
	}, common.pretty, common.raw)
	if err != nil {
		return err
	}
	return core.Emit(env, raw, common.pretty, common.raw)
}
