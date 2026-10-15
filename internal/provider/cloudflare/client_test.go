package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("CLOUDFLARE_API_BASE_URL", srv.URL)
	return &Client{AccountID: "acc", auth: bearer("tok"), Source: "test"}
}

func TestRunCatalogModelWrapsInput(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acc/ai/run" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "bytedance/seedance-2.5" {
			t.Errorf("model = %v", body["model"])
		}
		if in, _ := body["input"].(map[string]any); in["prompt"] != "hi" {
			t.Errorf("input = %v", body["input"])
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"result":{"video":"https://x/v.mp4"}}`)
	})
	res, err := c.Run(context.Background(), "bytedance/seedance-2.5", map[string]any{"prompt": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Result) != `{"video":"https://x/v.mp4"}` {
		t.Fatalf("result = %s", res.Result)
	}
}

func TestRunWorkersAIModelUsesUnifiedEndpoint(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acc/ai/run" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("cf-aig-gateway-id") != "default" {
			t.Errorf("gateway header = %q", r.Header.Get("cf-aig-gateway-id"))
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"input":{"audio":"AAA"},"model":"@cf/openai/whisper-large-v3-turbo"}` {
			t.Errorf("body = %s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"result":{"text":"hello"}}`)
	})
	res, err := c.Run(context.Background(), "@cf/openai/whisper-large-v3-turbo", map[string]any{"audio": "AAA"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Result) != `{"text":"hello"}` {
		t.Fatalf("result = %s", res.Result)
	}
}

func TestRunBinaryResponse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte{1, 2, 3})
	})
	res, err := c.Run(context.Background(), "@cf/deepgram/aura-2-en", map[string]any{"text": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Bytes) != 3 || res.ContentType != "audio/mpeg" {
		t.Fatalf("bytes=%v ct=%s", res.Bytes, res.ContentType)
	}
}

func TestRunSurfacesCloudflareErrors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"success":false,"errors":[{"code":5006,"message":"bad input"}]}`)
	})
	_, err := c.Run(context.Background(), "openai/gpt-image-2", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "5006 bad input") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverAccount(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"result":[{"id":"abc","name":"Me"}]}`)
	})
	id, err := c.discoverAccount()
	if err != nil || id != "abc" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestWranglerAuthParsesBannerPrefixedJSON(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/wrangler"
	writeExec(t, script, "#!/bin/sh\necho ' ⛅️ wrangler'\necho '{\"type\":\"oauth\",\"token\":\"T\"}'\n")
	t.Setenv("WRANGLER_BIN", script)
	w, err := wranglerAuth()
	if err != nil {
		t.Fatal(err)
	}
	if w.Type != "oauth" || w.Token != "T" {
		t.Fatalf("got %+v", w)
	}
}

func TestRunRejectsIncompleteState(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"state":"Failed","result":{}}`)
	})
	if _, err := c.Run(context.Background(), "x/y", map[string]any{}); err == nil {
		t.Fatal("expected error for failed state")
	}
}

func TestRunMultipartUsesModelPath(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acc/ai/run/@cf/black-forest-labs/flux-2-klein-9b" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("ct = %s", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"result":{"image":"QUJD"}}`)
	})
	res, err := c.RunMultipart(context.Background(), "@cf/black-forest-labs/flux-2-klein-9b", "multipart/form-data; boundary=x", []byte("--x--"))
	if err != nil || string(res.Result) != `{"image":"QUJD"}` {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestWranglerErrorPicksErrorLine(t *testing.T) {
	got := lastLine("\n\x1b[31m✘ \x1b[41;31m[\x1b[41;97mERROR\x1b[41;31m]\x1b[0m Not logged in.\n\n🪵  Logs were written to \"x.log\"\n")
	if !strings.Contains(got, "Not logged in.") || strings.Contains(got, "\x1b") {
		t.Fatalf("got %q", got)
	}
}
