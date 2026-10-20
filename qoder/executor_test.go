package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildPassthroughBodyPreservesClientRequest(t *testing.T) {
	payload := []byte(`{
		"model":"claude-opus-5-5",
		"messages":[
			{"role":"system","content":"client system prompt"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"temperature":0.2,
		"top_p":0.9,
		"max_tokens":512,
		"stop":["END"]
	}`)
	body, requestID, sessionID, err := buildPassthroughBody(payload, upstreamUltimate)
	if err != nil {
		t.Fatalf("buildPassthroughBody: %v", err)
	}
	if requestID == "" || sessionID == "" {
		t.Fatal("request/session id must be generated")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if got["model"] != "ultimate" {
		t.Fatalf("model = %v, want ultimate", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
	if got["temperature"] != 0.2 || got["top_p"] != 0.9 {
		t.Fatalf("sampling params lost: %v / %v", got["temperature"], got["top_p"])
	}
	if got["tool_choice"] != "auto" {
		t.Fatalf("tool_choice lost: %v", got["tool_choice"])
	}
	if _, ok := got["tools"].([]any); !ok {
		t.Fatal("tools lost")
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if _, ok := msgs[1].(map[string]any)["tool_calls"].([]any); !ok {
		t.Fatal("assistant tool_calls lost")
	}
	if msgs[2].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatal("tool_call_id lost")
	}
	meta := got["metadata"].(map[string]any)["context"].(map[string]any)
	if meta["client_type"] != "qodercli" || meta["task_id"] != "common" {
		t.Fatalf("metadata = %v", meta)
	}
}

func TestExecuteChatOnceAggregatesSSE(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("X-Request-ID") == "" || r.Header.Get("X-Session-ID") == "" {
			t.Error("missing request/session header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"ultimate\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5,\"credits\":0.5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	completion, status, err := executeChatOnce(sa, clientModelID, upstreamUltimate, []byte(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("executeChatOnce: %v (status %d)", err, status)
	}
	if gotAuth != "Bearer dt-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	var out map[string]any
	if err := json.Unmarshal(completion, &out); err != nil {
		t.Fatalf("parse completion: %v", err)
	}
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hi" {
		t.Fatalf("content = %v", msg["content"])
	}
	if creditsFromCompletion(completion).Credits != 0.5 {
		t.Fatal("credits not carried through")
	}
}

func TestExecuteChatOnceSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"no ultimate entitlement"}}`))
	}))
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	_, status, err := executeChatOnce(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if !strings.Contains(err.Error(), "no ultimate entitlement") {
		t.Fatalf("error loses upstream body: %v", err)
	}
}

func TestExecuteChatOnceSurfacesSSEError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: error\n"))
		_, _ = w.Write([]byte("data: {\"code\":\"provider_error\",\"message\":\"All models failed\",\"request_id\":\"req-1\",\"type\":\"provider_error\",\"details\":\"bedrock: AccessDeniedException: explicit deny\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	_, _, err := executeChatOnce(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err == nil {
		t.Fatal("expected upstream SSE error, got nil")
	}
	for _, want := range []string{"All models failed", "AccessDeniedException"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "empty completion") {
		t.Fatalf("SSE error reported as empty completion: %v", err)
	}
}

func TestCompletionLooksEmpty(t *testing.T) {
	empty := []byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	if !completionLooksEmpty(empty) {
		t.Fatal("empty completion not detected")
	}
	toolOnly := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"x"}]}}]}`)
	if completionLooksEmpty(toolOnly) {
		t.Fatal("tool-only completion must not be treated as empty")
	}
	reasoningOnly := []byte(`{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"thinking..."}}]}`)
	if completionLooksEmpty(reasoningOnly) {
		t.Fatal("reasoning-only completion must not be treated as empty")
	}
	// Token counts are not output: a content-less completion with usage must
	// still fail the guard instead of turning into an empty 200.
	usageOnly := []byte(`{"choices":[{"message":{"role":"assistant","content":""}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	if !completionLooksEmpty(usageOnly) {
		t.Fatal("usage-only completion must be treated as empty")
	}
}
