package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"testing"
)

func TestStartLoginGlobalURL(t *testing.T) {
	raw, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin: %v", err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %v / %s", err, raw)
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("result: %v", err)
	}
	u, err := url.Parse(resp.URL)
	if err != nil {
		t.Fatalf("url parse: %v", err)
	}
	if u.Host != "qoder.com" {
		t.Fatalf("host = %q", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != qoderClientID {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "" {
		t.Fatalf("redirect_uri must be absent, got %q", q.Get("redirect_uri"))
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(q.Get("nonce")) {
		t.Fatalf("nonce = %q, want 32 hex", q.Get("nonce"))
	}
	if q.Get("challenge_method") != "S256" || q.Get("challenge") == "" {
		t.Fatalf("pkce params missing: %v", q)
	}
}
