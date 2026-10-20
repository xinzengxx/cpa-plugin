package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"testing"
)

var (
	// nonceRe pins the Global device-flow nonce shape: 32 lowercase hex, no dashes.
	nonceRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// machineIDRe pins machine_id to a dashed UUID.
	machineIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func TestStartLoginGlobalURL(t *testing.T) {
	// Literal, deliberately NOT qoderClientID: a typo in the constant under test
	// must fail here instead of silently agreeing with itself.
	const wantClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"

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
		URL   string `json:"url"`
		State string `json:"state"`
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
	if got := q.Get("client_id"); got != wantClientID {
		t.Fatalf("client_id = %q, want %q", got, wantClientID)
	}
	if q.Has("redirect_uri") {
		t.Fatalf("redirect_uri must be absent, got %q", q.Get("redirect_uri"))
	}
	nonce := q.Get("nonce")
	if !nonceRe.MatchString(nonce) {
		t.Fatalf("nonce = %q, want 32 hex", nonce)
	}
	if got := q.Get("machine_id"); !machineIDRe.MatchString(got) {
		t.Fatalf("machine_id = %q, want UUID", got)
	}
	if q.Get("challenge_method") != "S256" || q.Get("challenge") == "" {
		t.Fatalf("pkce params missing: %v", q)
	}

	// The poll side resolves verifier/nonce by state, so the state returned to
	// the host must be the key stored in loginStates, with a verifier to use.
	if resp.State == "" {
		t.Fatalf("state is empty")
	}
	t.Cleanup(func() { loginStates.Delete(resp.State) })
	v, ok := loginStates.Load(resp.State)
	if !ok {
		t.Fatalf("state %q not stored in loginStates", resp.State)
	}
	lc, ok := v.(*loginCtx)
	if !ok {
		t.Fatalf("loginStates[%q] has type %T, want *loginCtx", resp.State, v)
	}
	if lc.verifier == "" {
		t.Fatalf("stored verifier for state %q is empty", resp.State)
	}
	if lc.nonce != nonce {
		t.Fatalf("stored nonce = %q, want URL nonce %q", lc.nonce, nonce)
	}
}
