// executor.go implements Qoder Global's pass-through executor: the client's
// OpenAI chat-completions payload is forwarded nearly verbatim to
// api2-v2.qoder.sh/model/v1/chat/completions (plain Bearer, standard OpenAI
// SSE), so tools/tool_calls/tool results survive untouched.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// upstreamChatURL is a var so tests can point it at httptest.
var upstreamChatURL = "https://api2-v2.qoder.sh/model/v1/chat/completions"

// chatUserAgent mirrors the Qoder CLI client. Change only with live evidence.
const chatUserAgent = "qoder/1.1.64"

func buildPassthroughBody(payload []byte, upstreamModel string) ([]byte, string, string, error) {
	if len(payload) == 0 {
		return nil, "", "", fmt.Errorf("payload empty")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, "", "", fmt.Errorf("payload parse: %w", err)
	}
	body["model"] = json.RawMessage(strconv.Quote(upstreamModel))
	body["stream"] = json.RawMessage("true")
	body["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	requestID := uuid.NewString()
	sessionID := uuid.NewString()
	meta, err := json.Marshal(map[string]any{"context": map[string]any{
		"request_id": requestID, "request_set_id": requestID,
		"session_id": sessionID, "task_id": "common", "client_type": "qodercli",
	}})
	if err != nil {
		return nil, "", "", err
	}
	body["metadata"] = meta
	out, err := json.Marshal(body)
	if err != nil {
		return nil, "", "", err
	}
	return out, requestID, sessionID, nil
}

func buildChatRequestWithContext(ctx context.Context, sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamChatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", chatUserAgent)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Session-ID", sessionID)
	return req, nil
}

func buildChatRequest(sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error) {
	return buildChatRequestWithContext(context.Background(), sa, body, requestID, sessionID)
}

// executeChatOnce performs one upstream attempt. Non-stream client requests
// also use stream=true upstream and fold the SSE, matching the Qoder CLI.
func executeChatOnce(sa *storedAuth, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error) {
	body, requestID, sessionID, err := buildPassthroughBody(payload, upstreamModel)
	if err != nil {
		return nil, 0, err
	}
	req, err := buildChatRequest(sa, body, requestID, sessionID)
	if err != nil {
		return nil, 0, err
	}
	stream, status, _, err := hostHTTPDoStream(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http_error: %w", err)
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if status >= 400 {
		raw, _ := io.ReadAll(reader)
		return nil, status, fmt.Errorf("upstream %d: %s", status, truncateRedacted(string(raw), 300))
	}
	completion, err := aggregateCompletion(reader, requestedModel)
	if err != nil {
		return nil, 0, err
	}
	if completionLooksEmpty(completion) {
		return nil, 0, fmt.Errorf("upstream returned an empty completion")
	}
	return completion, status, nil
}

func completionLooksEmpty(completion []byte) bool {
	var c struct {
		Choices []struct {
			Message map[string]any `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(completion, &c) != nil {
		return true
	}
	if v, ok := c.Usage["total_tokens"].(float64); ok && v > 0 {
		return false
	}
	for _, ch := range c.Choices {
		if s, _ := ch.Message["content"].(string); strings.TrimSpace(s) != "" {
			return false
		}
		if tc, ok := ch.Message["tool_calls"].([]any); ok && len(tc) > 0 {
			return false
		}
	}
	return true
}
