package cloudflare

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

// ImageParams configures an image generation/edit run.
type ImageParams struct {
	Prompt       string
	Model        string
	Size         string
	Quality      string
	Background   string
	OutputFormat string
	Seed         int
	Refs         []string
	Out          string
}

// ImageResult describes a completed image run.
type ImageResult struct {
	Model    string
	Endpoint string
	URL      string
	Raw      []byte
}

// ImageModel maps the CLI's model names onto Cloudflare catalog ids. Bare
// OpenAI names (gpt-image-2) gain the openai/ prefix; ids that already carry a
// provider or @cf/ prefix are used verbatim.
func ImageModel(model string) string {
	switch {
	case model == "" || model == "auto":
		return "openai/gpt-image-2"
	case strings.HasPrefix(model, "@cf/"), strings.Contains(model, "/"):
		return model
	case strings.HasPrefix(model, "gpt-image"):
		return "openai/" + model
	case strings.HasPrefix(model, "flux-2"):
		return "@cf/black-forest-labs/" + model
	}
	return model
}

func isMultipartImageModel(model string) bool {
	return strings.HasPrefix(model, "@cf/black-forest-labs/flux-2")
}

// GenerateImage runs a text-to-image or reference-image edit and writes the
// first image to p.Out.
func GenerateImage(ctx context.Context, p ImageParams) (*ImageResult, error) {
	c, err := New()
	if err != nil {
		return nil, err
	}
	model := ImageModel(p.Model)
	var res *Response
	switch {
	case isMultipartImageModel(model):
		body, ct, ferr := fluxForm(ctx, p)
		if ferr != nil {
			return nil, ferr
		}
		res, err = c.RunMultipart(ctx, model, ct, body)
	case strings.HasPrefix(model, "@cf/"):
		in := map[string]any{"prompt": p.Prompt}
		if w, h, ok := parseSize(p.Size); ok {
			in["width"], in["height"] = w, h
		}
		if p.Seed >= 0 {
			in["seed"] = p.Seed
		}
		if len(p.Refs) > 0 {
			return nil, fmt.Errorf("%s does not accept --image references; use a flux-2 or gpt-image model", model)
		}
		res, err = c.Run(ctx, model, in)
	default:
		in := map[string]any{"prompt": p.Prompt}
		putStr(in, "size", p.Size)
		putStr(in, "quality", p.Quality)
		putStr(in, "background", p.Background)
		putStr(in, "output_format", p.OutputFormat)
		if len(p.Refs) > 0 {
			imgs := make([]string, 0, len(p.Refs))
			for _, r := range p.Refs {
				data, mime, lerr := loadRef(ctx, r)
				if lerr != nil {
					return nil, lerr
				}
				imgs = append(imgs, dataURI(data, mime))
			}
			in["images"] = imgs
		}
		res, err = c.Run(ctx, model, in)
	}
	if err != nil {
		return nil, err
	}
	url, err := writeOrDownload(ctx, res, "image", p.Out)
	if err != nil {
		return nil, err
	}
	return &ImageResult{Model: model, Endpoint: res.Endpoint, URL: url, Raw: res.Raw}, nil
}

// fluxForm builds the multipart body FLUX.2 requires, even for prompt-only runs.
func fluxForm(ctx context.Context, p ImageParams) ([]byte, string, error) {
	if len(p.Refs) > 4 {
		return nil, "", fmt.Errorf("flux-2 accepts at most 4 --image references")
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("prompt", p.Prompt)
	if wd, ht, ok := parseSize(p.Size); ok {
		_ = w.WriteField("width", strconv.Itoa(wd))
		_ = w.WriteField("height", strconv.Itoa(ht))
	}
	if p.Seed >= 0 {
		_ = w.WriteField("seed", strconv.Itoa(p.Seed))
	}
	for i, r := range p.Refs {
		data, mime, err := loadRef(ctx, r)
		if err != nil {
			return nil, "", err
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="input_image_%d"; filename="ref%d"`, i, i))
		h.Set("Content-Type", mime)
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func parseSize(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
