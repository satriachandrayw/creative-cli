// Package cloudflare routes creative verbs through Cloudflare's AI platform:
// Workers AI models (@cf/...) and the third-party catalog (openai/..., google/...,
// elevenlabs/..., bytedance/..., topaz/...) served by the same /ai/run endpoint.
//
// Credentials resolve in this order, so an agent with `wrangler login` needs no
// extra setup:
//
//  1. CLOUDFLARE_API_TOKEN (env or /run/secrets) — the wrangler convention.
//  2. `wrangler auth token --json` — reuses wrangler's OAuth session or its own
//     env-based credentials (CLOUDFLARE_API_KEY + CLOUDFLARE_EMAIL).
//
// The account id comes from CLOUDFLARE_ACCOUNT_ID (env or /run/secrets), else it
// is discovered via GET /accounts when the token can see exactly one account.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/satriachandrayw/creative-cli/internal/core"
)

const defaultAPIBase = "https://api.cloudflare.com/client/v4"

// apiBase honours CLOUDFLARE_API_BASE_URL (the wrangler env var) for tests/proxies.
func apiBase() string {
	if v := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultAPIBase
}

// Client is an authenticated handle on one Cloudflare account.
type Client struct {
	AccountID string
	auth      func(*http.Request)
	// Source names where the credential came from ("env" | "wrangler:<type>").
	Source string
}

var (
	cached    *Client
	cachedErr error
	once      sync.Once
)

// New resolves credentials once per process.
func New() (*Client, error) {
	once.Do(func() { cached, cachedErr = resolve() })
	return cached, cachedErr
}

func resolve() (*Client, error) {
	c := &Client{}
	if tok, err := core.ReadSecret("CLOUDFLARE_API_TOKEN"); err == nil {
		c.auth = bearer(tok)
		c.Source = "env"
	} else {
		w, werr := wranglerAuth()
		if werr != nil {
			return nil, fmt.Errorf("cloudflare credentials missing: set CLOUDFLARE_API_TOKEN (Workers AI + AI Gateway permissions) or run `wrangler login` (%v)", werr)
		}
		switch w.Type {
		case "api_key":
			key, email := w.Key, w.Email
			c.auth = func(r *http.Request) {
				r.Header.Set("X-Auth-Key", key)
				r.Header.Set("X-Auth-Email", email)
			}
		default:
			c.auth = bearer(w.Token)
		}
		c.Source = "wrangler:" + w.Type
	}
	if id, err := core.ReadSecret("CLOUDFLARE_ACCOUNT_ID"); err == nil {
		c.AccountID = id
		return c, nil
	}
	id, err := c.discoverAccount()
	if err != nil {
		return nil, err
	}
	c.AccountID = id
	return c, nil
}

func bearer(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

type wranglerToken struct {
	Type  string `json:"type"`
	Token string `json:"token"`
	Key   string `json:"key"`
	Email string `json:"email"`
}

// wranglerCommand returns the argv prefix used to invoke wrangler. WRANGLER_BIN
// overrides it (e.g. "npx -y wrangler@4"); otherwise a PATH wrangler is used,
// falling back to `npx -y wrangler`.
func wranglerCommand() []string {
	if v := strings.TrimSpace(os.Getenv("WRANGLER_BIN")); v != "" {
		return strings.Fields(v)
	}
	if p, err := exec.LookPath("wrangler"); err == nil {
		return []string{p}
	}
	return []string{"npx", "-y", "wrangler"}
}

func wranglerAuth() (*wranglerToken, error) {
	argv := append(wranglerCommand(), "auth", "token", "--json")
	cmd := exec.Command(argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		return nil, fmt.Errorf("`%s` failed: %v: %s", strings.Join(argv, " "), err, lastLine(msg))
	}
	// wrangler may print banners/warnings before the JSON object.
	start := bytes.IndexByte(out, '{')
	if start < 0 {
		return nil, fmt.Errorf("wrangler auth token returned no JSON")
	}
	var w wranglerToken
	if err := json.Unmarshal(out[start:], &w); err != nil {
		return nil, fmt.Errorf("parse wrangler auth token: %w", err)
	}
	if w.Token == "" && w.Key == "" {
		return nil, fmt.Errorf("wrangler auth token returned an empty credential")
	}
	return &w, nil
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// lastLine picks wrangler's most useful stderr line: the first [ERROR] line,
// else the last line that is not a log-file notice.
func lastLine(s string) string {
	lines := strings.Split(ansi.ReplaceAllString(strings.TrimSpace(s), ""), "\n")
	for _, l := range lines {
		if strings.Contains(l, "ERROR") {
			return strings.TrimSpace(l)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.Contains(l, "Logs were written") {
			return l
		}
	}
	return s
}

func (c *Client) discoverAccount() (string, error) {
	req, err := http.NewRequest(http.MethodGet, apiBase()+"/accounts?per_page=50", nil)
	if err != nil {
		return "", err
	}
	parsed, _, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("discover cloudflare account: %w", err)
	}
	var env struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(parsed, &env); err != nil {
		return "", err
	}
	switch len(env.Result) {
	case 0:
		return "", fmt.Errorf("no cloudflare accounts visible to this token; set CLOUDFLARE_ACCOUNT_ID")
	case 1:
		return env.Result[0].ID, nil
	default:
		names := make([]string, 0, len(env.Result))
		for _, a := range env.Result {
			names = append(names, a.Name+"="+a.ID)
		}
		return "", fmt.Errorf("multiple cloudflare accounts (%s); set CLOUDFLARE_ACCOUNT_ID", strings.Join(names, ", "))
	}
}

// do sends req with auth and returns the body. Non-2xx statuses become errors
// carrying Cloudflare's error list.
func (c *Client) do(req *http.Request) ([]byte, string, error) {
	c.auth(req)
	req.Header.Set("User-Agent", core.UserAgent())
	resp, err := core.SharedClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, ct, fmt.Errorf("cloudflare http %d: %s", resp.StatusCode, errorText(body))
	}
	return body, ct, nil
}

func errorText(body []byte) string {
	var env struct {
		Errors []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &env) == nil && len(env.Errors) > 0 {
		parts := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			parts = append(parts, fmt.Sprintf("%d %s", e.Code, e.Message))
		}
		return strings.Join(parts, "; ")
	}
	if len(body) > 4000 {
		return string(body[:4000]) + "…(truncated)"
	}
	return string(body)
}

// Response is a decoded /ai/run reply. JSON replies populate Result (the
// unwrapped `result` member) and Raw; binary replies (some audio models stream
// bytes back) populate Bytes and ContentType instead.
type Response struct {
	Model       string
	Endpoint    string
	Result      json.RawMessage
	Raw         json.RawMessage
	Bytes       []byte
	ContentType string
}

// Run executes a model synchronously via the unified POST /ai/run endpoint,
// which serves both Workers AI (@cf/...) and third-party catalog models.
func (c *Client) Run(ctx context.Context, model string, input any) (*Response, error) {
	endpoint := "/ai/run"
	url := fmt.Sprintf("%s/accounts/%s%s", apiBase(), c.AccountID, endpoint)
	body, err := json.Marshal(map[string]any{"model": model, "input": input})
	if err != nil {
		return nil, err
	}
	return c.post(ctx, model, endpoint, url, "application/json", body)
}

// RunMultipart posts a multipart form to the model-in-path endpoint, the only
// form multipart-only Workers AI models (FLUX.2) accept.
func (c *Client) RunMultipart(ctx context.Context, model, contentType string, form []byte) (*Response, error) {
	endpoint := "/ai/run/" + model
	url := fmt.Sprintf("%s/accounts/%s%s", apiBase(), c.AccountID, endpoint)
	return c.post(ctx, model, endpoint, url, contentType, form)
}

// gatewayID is the AI Gateway every request is routed through. Third-party
// models require one; "default" is auto-created on first use. Set
// CLOUDFLARE_AI_GATEWAY_ID=none to omit the header.
func gatewayID() string {
	v := strings.TrimSpace(os.Getenv("CLOUDFLARE_AI_GATEWAY_ID"))
	switch v {
	case "":
		return "default"
	case "none":
		return ""
	}
	return v
}

func (c *Client) post(ctx context.Context, model, endpoint, url, contentType string, body []byte) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if gw := gatewayID(); gw != "" {
		req.Header.Set("cf-aig-gateway-id", gw)
	}
	data, ct, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", model, err)
	}
	res := &Response{Model: model, Endpoint: endpoint, ContentType: ct}
	if !strings.Contains(ct, "json") {
		res.Bytes = data
		return res, nil
	}
	res.Raw = data
	var env struct {
		Success *bool           `json:"success"`
		State   string          `json:"state"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", model, err)
	}
	if env.Success != nil && !*env.Success {
		return nil, fmt.Errorf("%s: %s", model, errorText(data))
	}
	if env.State != "" && !strings.EqualFold(env.State, "completed") {
		return nil, fmt.Errorf("%s: run state %q: %s", model, env.State, errorText(data))
	}
	if len(env.Result) > 0 {
		res.Result = env.Result
	} else {
		res.Result = data
	}
	return res, nil
}
