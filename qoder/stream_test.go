package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"SF\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15,\"credits\":1.5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestCollectUpstreamStreamPreservesToolCallsAndUsage(t *testing.T) {
	srv := sseServer(t)
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, err := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"weather?"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`), upstreamUltimate)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	collector := &sseUsageCollector{}
	chunks, status, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, false, collector)
	if err != nil {
		t.Fatalf("collect: %v (status %d)", err, status)
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	joined := ""
	for _, c := range chunks {
		joined += string(c.Payload)
	}
	if strings.Contains(joined, "[DONE]") {
		t.Fatal("[DONE] must not be forwarded")
	}
	if !strings.Contains(joined, `"get_weather"`) {
		t.Fatalf("tool call fragments lost: %s", joined)
	}
	if !strings.Contains(joined, `"city`) {
		t.Fatalf("tool call arguments lost: %s", joined)
	}
	if !collector.hasOutput {
		t.Fatal("collector saw no output")
	}
	if collector.detail().TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", collector.detail().TotalTokens)
	}
	if collector.credits().Credits != 1.5 {
		t.Fatalf("credits = %v, want 1.5", collector.credits().Credits)
	}
}

func TestCollectUpstreamStreamSSEFraming(t *testing.T) {
	srv := sseServer(t)
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, _ := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), upstreamUltimate)
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	chunks, _, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, true, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, c := range chunks {
		if !strings.HasPrefix(string(c.Payload), "data: ") {
			t.Fatalf("unframed chunk: %s", c.Payload)
		}
	}
}

func TestStreamErrorMessage(t *testing.T) {
	if got := streamErrorMessage(`{"choices":[{"delta":{"content":"ok"}}]}`); got != "" {
		t.Fatalf("normal chunk treated as error: %q", got)
	}
	if got := streamErrorMessage(`{"error":{"message":"no entitlement"}}`); got != "no entitlement" {
		t.Fatalf("error message = %q", got)
	}
}

func TestCollectUpstreamStreamEmptyGuard(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer empty.Close()
	old := upstreamChatURL
	upstreamChatURL = empty.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, _ := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), upstreamUltimate)
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	if _, _, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, false, &sseUsageCollector{}); err == nil {
		t.Fatal("empty upstream stream must return an error, not an empty 200")
	}
}
