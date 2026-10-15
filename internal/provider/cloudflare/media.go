package cloudflare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/satriachandrayw/creative-cli/internal/core"
)

// loadRef returns the bytes and MIME type of a local path, http(s) URL, or data
// URI.
func loadRef(ctx context.Context, ref string) ([]byte, string, error) {
	switch {
	case strings.HasPrefix(ref, "data:"):
		return decodeDataURI(ref)
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return fetch(ctx, ref)
	default:
		data, err := os.ReadFile(ref)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", ref, err)
		}
		return data, core.MIMEForPath(ref), nil
	}
}

func fetch(ctx context.Context, url string) ([]byte, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", core.UserAgent())
	resp, err := core.SharedClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetch %s: http %d", url, resp.StatusCode)
	}
	ct := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if ct == "" || ct == "application/octet-stream" {
		ct = core.MIMEForPath(strings.Split(url, "?")[0])
	}
	return data, ct, nil
}

func decodeDataURI(uri string) ([]byte, string, error) {
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, "", fmt.Errorf("malformed data uri")
	}
	meta := uri[len("data:"):comma]
	payload := uri[comma+1:]
	mime := strings.Split(meta, ";")[0]
	if strings.HasSuffix(meta, ";base64") {
		data, err := base64.StdEncoding.DecodeString(payload)
		return data, mime, err
	}
	return []byte(payload), mime, nil
}

func dataURI(data []byte, mime string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// refURI passes remote http(s)/data refs through untouched and inlines local
// files as base64 data URIs, the encoding catalog media models accept.
func refURI(ref string) (string, error) {
	if core.IsRemoteRef(ref) {
		return ref, nil
	}
	data, mime, err := loadRef(nil, ref)
	if err != nil {
		return "", err
	}
	return dataURI(data, mime), nil
}

func refURIs(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		u, err := refURI(r)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// field extracts a string member from a JSON result object.
func field(result json.RawMessage, name string) string {
	var m map[string]any
	if json.Unmarshal(result, &m) != nil {
		return ""
	}
	s, _ := m[name].(string)
	return s
}

// saveMedia writes a model's media output to dest. The value may be an http(s)
// URL (downloaded), a data URI, or bare base64. It returns the URL when one was
// provided so the envelope can surface it.
func saveMedia(ctx context.Context, value, dest string) (string, error) {
	switch {
	case value == "":
		return "", fmt.Errorf("model returned no media")
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"):
		if dest == "" {
			return value, nil
		}
		data, _, err := fetch(ctx, value)
		if err != nil {
			return value, err
		}
		return value, core.WriteFile(dest, data)
	case strings.HasPrefix(value, "data:"):
		data, _, err := decodeDataURI(value)
		if err != nil {
			return "", err
		}
		if dest == "" {
			return "", fmt.Errorf("--out is required for inline media output")
		}
		return "", core.WriteFile(dest, data)
	default:
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return "", fmt.Errorf("decode base64 media: %w", err)
		}
		if dest == "" {
			return "", fmt.Errorf("--out is required for inline media output")
		}
		return "", core.WriteFile(dest, data)
	}
}

// writeOrDownload stores a Response's media, whether returned as raw bytes or as
// a URL/base64 string under the given result field.
func writeOrDownload(ctx context.Context, res *Response, fieldName, dest string) (string, error) {
	if len(res.Bytes) > 0 {
		if dest == "" {
			return "", fmt.Errorf("--out is required for binary media output")
		}
		return "", core.WriteFile(dest, res.Bytes)
	}
	return saveMedia(ctx, field(res.Result, fieldName), dest)
}

func putStr(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}
