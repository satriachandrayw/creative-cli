package cmd

import (
	"fmt"
	"time"

	"github.com/satriachandrayw/creative-cli/internal/core"
	"github.com/satriachandrayw/creative-cli/internal/provider/cloudflare"
	"github.com/satriachandrayw/creative-cli/internal/provider/elevenlabs"
	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		c := &cobra.Command{
			Use:   "music",
			Short: "Compose a music track via ElevenLabs Music on Cloudflare AI (default) or the ElevenLabs API directly.",
			Args:  cobra.NoArgs,
			RunE:  runMusic,
		}
		c.Flags().String("prompt", "", "music description (required)")
		c.Flags().Float64("duration-seconds", 0, "target length in seconds (optional; maps to music_length_ms)")
		c.Flags().String("output-format", "", "audio output format")
		c.Flags().String("model", "elevenlabs/music-v2", "Cloudflare catalog music model (cloudflare provider)")
		c.Flags().Bool("instrumental", false, "force an instrumental track (cloudflare)")
		addOutFlag(c)
		addProviderFlag(c, "cloudflare")
		root.AddCommand(c)
	})
}

func runMusic(cmd *cobra.Command, _ []string) error {
	common := readCommon(cmd)
	prompt := flagStr(cmd, "prompt")
	if prompt == "" {
		return fmt.Errorf("--prompt is required")
	}
	out := flagStr(cmd, "out")
	if out == "" {
		return fmt.Errorf("--out is required")
	}

	var lengthMs int
	if cmd.Flags().Changed("duration-seconds") {
		v, _ := cmd.Flags().GetFloat64("duration-seconds")
		lengthMs = int(v * 1000)
	}

	if provider := flagStr(cmd, "provider"); provider == "cloudflare" {
		started := time.Now()
		core.Progress("composing music with %s via cloudflare", flagStr(cmd, "model"))
		res, err := cloudflare.Music(cmd.Context(), cloudflare.MusicParams{
			Prompt:            prompt,
			Model:             flagStr(cmd, "model"),
			MusicLengthMs:     lengthMs,
			OutputFormat:      flagStr(cmd, "output-format"),
			ForceInstrumental: flagBool(cmd, "instrumental"),
			Out:               out,
		})
		if err != nil {
			return err
		}
		return emitMedia(common, started, res, "prompt", prompt, "audio", out)
	} else if provider != "elevenlabs" {
		return fmt.Errorf("unknown music provider %q (want cloudflare|elevenlabs)", provider)
	}

	client, err := elevenlabs.New()
	if err != nil {
		return err
	}
	started := time.Now()
	core.Progress("composing music")
	res, err := client.Music(elevenlabs.MusicParams{
		Prompt:        prompt,
		MusicLengthMs: lengthMs,
		OutputFormat:  flagStr(cmd, "output-format"),
	})
	if err != nil {
		return err
	}

	output := "audio"
	switch {
	case len(res.Bytes) > 0:
		if err := core.WriteFile(out, res.Bytes); err != nil {
			return err
		}
	case res.URL != "":
		if _, err := core.Download(res.URL, out, nil); err != nil {
			return err
		}
		output = res.URL
	default:
		return fmt.Errorf("music response contained neither audio nor a download url")
	}
	elapsed := roundSecs(started)
	core.Progress("done in %.3fs", elapsed)

	env := &core.Envelope{
		Provider:       "elevenlabs",
		Endpoint:       res.Endpoint,
		Input:          &core.InputInfo{Kind: "prompt", Source: prompt},
		Output:         output,
		ElapsedSeconds: elapsed,
		Out:            out,
	}
	return core.Emit(env, nil, common.pretty, common.raw)
}
