package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/satriachandrayw/creative-cli/internal/core"
	"github.com/satriachandrayw/creative-cli/internal/provider/cloudflare"
	"github.com/satriachandrayw/creative-cli/internal/provider/openai"
	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		c := &cobra.Command{
			Use:   "image",
			Short: "Generate or edit an image via Cloudflare AI (gpt-image-2, FLUX.2, …) or OpenAI directly (add --image REF for img2img).",
			Args:  cobra.NoArgs,
			RunE:  runImage,
		}
		c.Flags().String("prompt", "", "text prompt (required)")
		c.Flags().StringArray("image", nil, "reference image for img2img (path or url), repeatable")
		c.Flags().String("model", "gpt-image-2", "image model: gpt-image-2 | flux-2-klein-9b | flux-2-dev | any Cloudflare catalog/@cf id")
		c.Flags().String("size", "auto", "1024x1024 | 1536x1024 | 1024x1536 | auto")
		c.Flags().String("quality", "auto", "low | medium | high | auto")
		c.Flags().String("background", "", "transparent | opaque | auto")
		c.Flags().String("output-format", "png", "png | jpeg | webp")
		c.Flags().Int("n", 1, "number of images (the first is written to --out)")
		c.Flags().Int("seed", -1, "seed for @cf models; -1 lets the provider choose")
		c.Flags().Duration("timeout", 10*time.Minute, "total image request timeout, including fallback and downloads")
		addOutFlag(c)
		addProviderFlag(c, "cloudflare")
		root.AddCommand(c)
	})
}

func runImage(cmd *cobra.Command, _ []string) error {
	common := readCommon(cmd)
	prompt := flagStr(cmd, "prompt")
	if prompt == "" {
		return fmt.Errorf("--prompt is required")
	}
	out := flagStr(cmd, "out")
	if out == "" {
		return fmt.Errorf("--out is required")
	}
	refs := flagStrs(cmd, "image")
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil || timeout <= 0 {
		return fmt.Errorf("--timeout must be a positive duration")
	}

	started := time.Now()
	if len(refs) > 0 {
		core.Progress("editing image (img2img) with %d ref(s)", len(refs))
	} else {
		core.Progress("generating image with %s", flagStr(cmd, "model"))
	}

	if provider := flagStr(cmd, "provider"); provider == "cloudflare" {
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		res, err := cloudflare.GenerateImage(ctx, cloudflare.ImageParams{
			Prompt:       prompt,
			Model:        flagStr(cmd, "model"),
			Size:         flagStr(cmd, "size"),
			Quality:      flagStr(cmd, "quality"),
			Background:   flagStr(cmd, "background"),
			OutputFormat: flagStr(cmd, "output-format"),
			Seed:         flagInt(cmd, "seed"),
			Refs:         refs,
			Out:          out,
		})
		if err != nil {
			return err
		}
		elapsed := roundSecs(started)
		core.Progress("done in %.3fs", elapsed)
		var output any = "image"
		if res.URL != "" {
			output = res.URL
		}
		return core.Emit(&core.Envelope{
			Provider:       "cloudflare",
			Endpoint:       res.Endpoint,
			Model:          res.Model,
			Input:          &core.InputInfo{Kind: inputKind(refs), Source: prompt},
			Output:         output,
			ElapsedSeconds: elapsed,
			Out:            out,
		}, res.Raw, common.pretty, common.raw)
	} else if provider != "openai" {
		return fmt.Errorf("unknown image provider %q (want cloudflare|openai)", provider)
	}

	res, err := openai.Generate(openai.ImageParams{
		Context:      cmd.Context(),
		Timeout:      timeout,
		Prompt:       prompt,
		Model:        flagStr(cmd, "model"),
		Size:         flagStr(cmd, "size"),
		Quality:      flagStr(cmd, "quality"),
		Background:   flagStr(cmd, "background"),
		OutputFormat: flagStr(cmd, "output-format"),
		N:            flagInt(cmd, "n"),
		Refs:         refs,
	})
	if err != nil {
		return err
	}
	if err := core.WriteFile(out, res.Bytes); err != nil {
		return err
	}
	elapsed := roundSecs(started)
	core.Progress("done in %.3fs", elapsed)

	env := &core.Envelope{
		Provider:       res.Provider,
		Endpoint:       res.Endpoint,
		Model:          res.Model,
		Input:          &core.InputInfo{Kind: inputKind(refs), Source: prompt},
		Output:         "image",
		Usage:          res.Usage,
		ElapsedSeconds: elapsed,
		Out:            out,
	}
	return core.Emit(env, res.Raw, common.pretty, common.raw)
}
