package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/satriachandrayw/creative-cli/internal/core"
	"github.com/satriachandrayw/creative-cli/internal/provider/cloudflare"
	"github.com/satriachandrayw/creative-cli/internal/provider/falpipe"
	"github.com/satriachandrayw/creative-cli/internal/provider/zapcap"
	"github.com/spf13/cobra"
)

func init() {
	register(func(root *cobra.Command) {
		c := &cobra.Command{
			Use:   "caption <input>",
			Short: "Burn subtitles onto a video via Workers AI Whisper + ffmpeg (cloudflare), fal VEED, or ZapCap.",
			Args:  cobra.ExactArgs(1),
			RunE:  runCaption,
		}
		addProviderFlag(c, "cloudflare")
		c.Flags().String("preset", "simple", "caption style preset (veed)")
		c.Flags().String("position", "", "veed caption position: top | center | bottom")
		c.Flags().String("shadow", "", "veed text shadow: none | min | mid | max")
		c.Flags().String("font", "", "veed Google Font family, e.g. Inter")
		c.Flags().Int("font-weight", 0, "veed font weight 100-900")
		c.Flags().String("font-color", "", "veed baseline word colour, hex")
		c.Flags().String("highlight-color", "", "veed highlighted word colour, hex")
		c.Flags().String("language", "", "source language override (veed: optional; zapcap: defaults en)")
		c.Flags().Int("font-size", 18, "cloudflare caption font size (libass units)")
		c.Flags().String("outline-color", "", "cloudflare caption outline colour, hex")
		c.Flags().Int("max-words", 5, "cloudflare: max words per caption cue")
		c.Flags().String("srt-out", "", "cloudflare: also write the generated .srt here")
		c.Flags().String("template-id", "", "caption template id (zapcap, required)")
		addOutFlag(c)
		addAsyncFlags(c)
		root.AddCommand(c)
	})
}

func runCaption(cmd *cobra.Command, args []string) error {
	common := readCommon(cmd)
	var (
		env *core.Envelope
		raw json.RawMessage
		err error
	)
	switch provider := flagStr(cmd, "provider"); provider {
	case "cloudflare":
		started := time.Now()
		core.Progress("captioning %s with Workers AI Whisper + ffmpeg", args[0])
		res, cerr := cloudflare.Caption(cmd.Context(), cloudflare.CaptionParams{
			Input:        args[0],
			Out:          flagStr(cmd, "out"),
			Language:     flagStr(cmd, "language"),
			Position:     flagStr(cmd, "position"),
			Font:         flagStr(cmd, "font"),
			FontSize:     flagInt(cmd, "font-size"),
			FontColor:    flagStr(cmd, "font-color"),
			OutlineColor: flagStr(cmd, "outline-color"),
			Shadow:       flagStr(cmd, "shadow"),
			MaxWords:     flagInt(cmd, "max-words"),
			SubtitlesOut: flagStr(cmd, "srt-out"),
		})
		if cerr != nil {
			return cerr
		}
		elapsed := roundSecs(started)
		core.Progress("done in %.3fs", elapsed)
		return core.Emit(&core.Envelope{
			Provider:       "cloudflare",
			Endpoint:       res.Endpoint,
			Model:          res.Model,
			Input:          &core.InputInfo{Kind: "video", Source: args[0]},
			Output:         "video",
			ElapsedSeconds: elapsed,
			Out:            flagStr(cmd, "out"),
		}, nil, common.pretty, common.raw)
	case "veed", "zapcap":
	default:
		return fmt.Errorf("unknown caption provider %q (want cloudflare|veed|zapcap)", provider)
	}
	if flagStr(cmd, "provider") == "zapcap" {
		env, raw, err = zapcap.Render(zapcap.RenderOpts{
			Input:            args[0],
			TemplateID:       flagStr(cmd, "template-id"),
			Language:         flagStr(cmd, "language"),
			Out:              flagStr(cmd, "out"),
			NoWait:           flagBool(cmd, "no-wait"),
			PollIntervalSecs: flagInt(cmd, "poll-interval-secs"),
			MaxWaitSecs:      flagInt(cmd, "max-wait-secs"),
		}, common.pretty, common.raw)
	} else {
		env, raw, err = falpipe.Veed(falpipe.VeedOpts{
			Input:            args[0],
			Preset:           flagStr(cmd, "preset"),
			Position:         flagStr(cmd, "position"),
			Shadow:           flagStr(cmd, "shadow"),
			Font:             flagStr(cmd, "font"),
			FontWeight:       flagInt(cmd, "font-weight"),
			FontColor:        flagStr(cmd, "font-color"),
			HighlightColor:   flagStr(cmd, "highlight-color"),
			Language:         flagStr(cmd, "language"),
			Out:              flagStr(cmd, "out"),
			NoWait:           flagBool(cmd, "no-wait"),
			PollIntervalSecs: flagInt(cmd, "poll-interval-secs"),
			MaxWaitSecs:      flagInt(cmd, "max-wait-secs"),
		}, common.pretty, common.raw)
	}
	if err != nil {
		return err
	}
	return core.Emit(env, raw, common.pretty, common.raw)
}
