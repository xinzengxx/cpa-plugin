// stream.go owns the stream plumbing shared by the executor: emitting chunks
// and errors back to the host stream (streamEmit/streamEmitError/streamClose),
// the SSE-frame helpers that clean and re-frame chunk payloads, and the
// completion aggregator used when an upstream SSE body must be folded.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// streamEmit pushes one chunk payload to the host stream. Returns an error if
// the host rejected it (e.g. the client already disconnected and the stream
// was closed), which the pump uses to stop reading a dead upstream.
func streamEmit(streamID string, payload []byte) error {
	if streamID == "" {
		return fmt.Errorf("no stream id")
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "payload": payload})
	_, err := hostCall(pluginabi.MethodHostStreamEmit, body)
	return err
}

// streamErrorWire carries an error through the host's dedicated stream error
// channel. A payload-shaped {"error":...} chunk is ignored by the host's
// cross-format translators (notably Claude), which used to turn upstream
// failures into a silent empty end_turn.
func streamErrorWire(streamID, message string) []byte {
	raw, _ := json.Marshal(map[string]any{"stream_id": streamID, "error": redactSecrets(message)})
	return raw
}

func streamEmitError(streamID, message string) {
	if streamID == "" {
		return
	}
	_, _ = hostCall(pluginabi.MethodHostStreamEmit, streamErrorWire(streamID, message))
}

var streamCloseOnce sync.Map // streamID -> *sync.Once

func streamClose(streamID string) {
	if streamID == "" {
		return
	}
	actual, _ := streamCloseOnce.LoadOrStore(streamID, &sync.Once{})
	once := actual.(*sync.Once)
	once.Do(func() {
		body, _ := json.Marshal(map[string]any{"stream_id": streamID})
		_, _ = hostCall(pluginabi.MethodHostStreamClose, body)
		// Bound the map: a closed stream is terminal and never re-registered.
		// The host close is idempotent, so a racing duplicate is harmless.
		streamCloseOnce.Delete(streamID)
	})
}

func streamHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	return h
}

// clientNeedsSSEFrame reports whether chunk payloads must carry their own
// "data: " SSE framing. CPA's chat-completions passthrough adds the prefix
// itself, but every cross-format response translator (claude/gemini/codex/...)
// only consumes payloads already framed as "data: " lines. The host hands the
// plugin the inbound request path in Metadata, so we frame chunks ourselves for
// any entry path other than the native OpenAI chat-completions one.
func clientNeedsSSEFrame(metadata map[string]any) bool {
	path, _ := metadata["request_path"].(string)
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "/v1/chat/completions", "/v1/completions":
		return false
	default:
		return true
	}
}

// cleanChunkJSON strips only the known-problematic empty tool-call shells
// from choice deltas: a null/empty function_call and an empty tool_calls array
// (QoderWork emits these on the terminal chunk, and strict clients interpret
// them as a truncated tool call). Other empty-but-legal values are preserved:
// content:"" is a valid delta (pure tool-call chunk) and the role-only first
// chunk must survive so clients can establish the message role.
func cleanChunkJSON(s string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) != nil {
		return s
	}
	changed := false
	if choices, ok := obj["choices"].([]any); ok {
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}
			if v, present := delta["function_call"]; present && isEmptyValue(v) {
				delete(delta, "function_call")
				changed = true
			}
			if v, present := delta["tool_calls"]; present {
				if arr, isArr := v.([]any); isArr && len(arr) == 0 {
					delete(delta, "tool_calls")
					changed = true
				}
			}
			// Upstream often pads terminal/noop deltas with empty noise fields
			// that clients ignore but pollute wire size / some parsers.
			for _, noise := range []string{"extra_fields", "refusal", "reasoning_content"} {
				if v, present := delta[noise]; present && isEmptyValue(v) {
					delete(delta, noise)
					changed = true
				}
			}
			// Drop a fully-empty delta ONLY when the choice carries no other
			// signal (no finish_reason): e.g. {"delta":{"function_call":null}}
			// reduced to {}. A delta with role/content:"" is meaningful and
			// never reaches this branch (those fields are preserved above).
			if len(delta) == 0 {
				if fr, _ := choice["finish_reason"].(string); fr == "" {
					return ""
				}
			}
		}
	}
	if !changed {
		return s
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return s
	}
	return string(out)
}

func aggregateCompletion(r io.Reader, model string) ([]byte, error) {
	var content, reasoning, role, respModel, respID, finish string
	var created int64
	var usage map[string]any
	// tool_calls arrive as streaming deltas: each chunk carries an index plus a
	// partial call (id/type/function.name on the first delta, argument text
	// fragments afterwards). Merge by index instead of appending raw fragments
	// so the folded completion holds whole calls.
	toolCalls := map[int]map[string]any{}
	var toolOrder []int
	var scanErr error

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		data := stripDataPrefix(scanner.Text())
		if data == "" || data == "[DONE]" {
			continue
		}
		// Upstream can report a failure as an SSE error object and then close.
		// Surface it before chunk parsing, otherwise the fold falls through to
		// an "empty completion" that hides the provider's code and details.
		if msg := streamErrorMessage(data); msg != "" {
			return nil, fmt.Errorf("upstream error: %s", msg)
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if v, ok := chunk["id"].(string); ok && v != "" {
			respID = v
		}
		if v, ok := chunk["model"].(string); ok && v != "" {
			respModel = v
		}
		if v, ok := chunk["created"].(float64); ok {
			created = int64(v)
		}
		if v, ok := chunk["usage"].(map[string]any); ok {
			usage = v
		}
		choices, _ := chunk["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			if delta, ok := choice["delta"].(map[string]any); ok {
				if v, ok := delta["role"].(string); ok && v != "" {
					role = v
				}
				if v, ok := delta["content"].(string); ok {
					content += v
				}
				if v, ok := delta["reasoning_content"].(string); ok {
					reasoning += v
				}
				if tcs, ok := delta["tool_calls"].([]any); ok {
					for _, tc := range tcs {
						call, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						idx := 0
						if v, ok := call["index"].(float64); ok {
							idx = int(v)
						}
						merged, seen := toolCalls[idx]
						if !seen {
							merged = map[string]any{"index": idx}
							toolCalls[idx] = merged
							toolOrder = append(toolOrder, idx)
						}
						mergeToolCallDelta(merged, call)
					}
				}
			}
			if v, ok := choice["finish_reason"].(string); ok && v != "" {
				finish = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		scanErr = err
	}
	// A mid-stream read failure means the folded completion is truncated. The
	// host discards the payload entirely when the plugin returns an error
	// (sdk/api/handlers executeWithPluginExecutor), so fail fast here instead
	// of assembling a partial completion nobody can safely consume.
	if scanErr != nil {
		return nil, fmt.Errorf("upstream stream read error: %w", scanErr)
	}

	message := map[string]any{"role": firstNonEmpty(role, "assistant"), "content": content}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(toolOrder) > 0 {
		sort.Ints(toolOrder)
		calls := make([]map[string]any, 0, len(toolOrder))
		for _, idx := range toolOrder {
			calls = append(calls, toolCalls[idx])
		}
		message["tool_calls"] = calls
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{
		"id":      firstNonEmpty(respID, "chatcmpl-qoder"),
		"object":  "chat.completion",
		"created": created,
		"model":   firstNonEmpty(respModel, model),
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": firstNonEmpty(finish, "stop"),
		}},
	}
	if usage != nil {
		result["usage"] = usage
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mergeToolCallDelta folds one streaming tool_call fragment into the merged
// call: scalar fields (id/type) are taken when first seen, function.name is
// concatenated (upstream may split it), and function.arguments text fragments
// are appended in arrival order.
func mergeToolCallDelta(merged, delta map[string]any) {
	for _, k := range []string{"id", "type"} {
		if _, present := merged[k]; !present {
			if v, ok := delta[k].(string); ok && v != "" {
				merged[k] = v
			}
		}
	}
	dfn, _ := delta["function"].(map[string]any)
	if dfn == nil {
		return
	}
	mfn, _ := merged["function"].(map[string]any)
	if mfn == nil {
		mfn = map[string]any{}
		merged["function"] = mfn
	}
	if v, ok := dfn["name"].(string); ok && v != "" {
		cur, _ := mfn["name"].(string)
		mfn["name"] = cur + v
	}
	if v, ok := dfn["arguments"].(string); ok && v != "" {
		cur, _ := mfn["arguments"].(string)
		mfn["arguments"] = cur + v
	}
}

func stripDataPrefix(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "data:") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// isEmptyValue reports whether v is a "zero" SSE field that should be
// stripped from outgoing chunks. Used by cleanChunkJSON to drop the legacy
// function_call shell ({"name":"","arguments":""}) some upstreams emit on
// the terminal chunk — clients that strictly validate the delta will reject
// the shell as an invalid tool call.
func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		if len(x) == 0 {
			return true
		}
		for _, val := range x {
			if !isEmptyValue(val) {
				return false
			}
		}
		return true
	}
	return false
}

// upstreamErrorLimit caps the rendered upstream error text. Provider errors
// can embed long request/egress diagnostics; 500 chars is enough to identify
// the failure without bloating published usage/recent records.
const upstreamErrorLimit = 500

// streamErrorMessage extracts an upstream error carried inside a data line and
// renders it as "code: message (details)" so provider failures keep their
// diagnostic detail. Normal chunks always have "choices" or "usage"; only
// error objects are treated as failures. The rendered text is redacted and
// capped before it is returned.
func streamErrorMessage(line string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return ""
	}
	if _, ok := obj["choices"]; ok {
		return ""
	}
	if _, ok := obj["usage"]; ok {
		return ""
	}
	if e, ok := obj["error"].(map[string]any); ok {
		code := firstNonEmpty(stringValue(e, "code"), stringValue(e, "type"))
		return renderUpstreamError(code, stringValue(e, "message"), detailText(e["details"]))
	}
	return renderUpstreamError(stringValue(obj, "code"), stringValue(obj, "message"), detailText(obj["details"]))
}

// renderUpstreamError composes "code: message (details)", skipping empty
// parts, then redacts and truncates the result for safe surfacing.
func renderUpstreamError(code, message, details string) string {
	text := strings.TrimSpace(message)
	if code = strings.TrimSpace(code); code != "" {
		if text != "" {
			text = code + ": " + text
		} else {
			text = code
		}
	}
	if details = strings.TrimSpace(details); details != "" {
		if text != "" {
			text += " (" + details + ")"
		} else {
			text = details
		}
	}
	if text == "" {
		return ""
	}
	return truncateRedacted(text, upstreamErrorLimit)
}

// stringValue returns a trimmed string field, or "" when absent / not a string.
func stringValue(obj map[string]any, key string) string {
	s, _ := obj[key].(string)
	return strings.TrimSpace(s)
}

// detailText renders an error "details" value. Upstream sends it as an opaque
// string (often embedded JSON), but objects/arrays are flattened to JSON so
// nothing is silently dropped.
func detailText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
}

// upstreamFailureText renders the client-facing and recorded detail for a
// non-2xx upstream response. When a 401/403 retry could not refresh the
// credential, the refresh failure is appended to the upstream error so neither
// part is swallowed. The combined text stays within upstreamErrorLimit while
// keeping the refresh suffix visible even when the upstream body is long.
func upstreamFailureText(status int, body []byte, bodyLimit int, refreshErr error) string {
	text := fmt.Sprintf("upstream %d: %s", status, truncateRedacted(string(body), bodyLimit))
	if refreshErr == nil {
		return text
	}
	suffix := fmt.Sprintf(" (refresh failed: %v)", refreshErr)
	if keep := upstreamErrorLimit - len(suffix); len(text) > keep {
		if keep < 0 {
			keep = 0
		}
		text = truncate(text, keep)
	}
	return text + suffix
}

func pumpUpstreamStream(ctx context.Context, cancel context.CancelFunc, sa *storedAuth, body []byte, requestID, sessionID, streamID string, sseFramed bool, requestedModel, upstreamModel, authUID, nickname string, started time.Time, authID string) {
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			streamClose(streamID)
		}
	}
	defer closeOnce()
	if cancel != nil {
		defer cancel()
	}
	var stream *hostHTTPStream
	var status int
	var err error
	// rejectedBody keeps the final 401/403 body: the stream is closed before
	// the retry decision, so the original upstream error would be lost. It is
	// reset on every attempt so a body captured from an earlier rejected
	// attempt can never be attributed to a later attempt's status.
	var rejectedBody []byte
	// refreshErr records a failed refresh so the final 401/403 failure reports
	// both the upstream status/body and the refresh failure.
	var refreshErr error
	for attempt := 0; attempt < 2; attempt++ {
		rejectedBody = nil
		httpReq, rerr := buildChatRequestWithContext(ctx, sa, body, requestID, sessionID)
		if rerr != nil {
			streamEmitError(streamID, rerr.Error())
			return
		}
		stream, status, _, err = hostHTTPDoStream(httpReq)
		if err != nil {
			publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, 0, err.Error())
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, err.Error())
			streamEmitError(streamID, "http_error: "+err.Error())
			return
		}
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			break
		}
		rejectedBody, _ = io.ReadAll(newHostStreamReader(stream))
		stream.Close()
		if attempt == 0 {
			if refreshErr = refreshForRetry(authID, sa); refreshErr == nil {
				continue
			}
		}
		break
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if status >= 400 {
		raw := rejectedBody
		if len(raw) == 0 {
			raw, _ = io.ReadAll(reader)
		}
		detail := upstreamFailureText(status, raw, upstreamErrorLimit, refreshErr)
		publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, status, detail)
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, detail)
		if authUID != "" {
			go reconcileByUID(authUID, status, string(raw))
		}
		streamEmitError(streamID, upstreamFailureText(status, raw, upstreamErrorLimit, refreshErr))
		return
	}
	collector := &sseUsageCollector{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		if msg := streamErrorMessage(payload); msg != "" {
			detail := truncateRedacted(msg, upstreamErrorLimit)
			publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, 0, detail)
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, "upstream error: "+detail)
			streamEmitError(streamID, "upstream error: "+detail)
			return
		}
		collector.feed(payload)
		cleaned := cleanChunkJSON(payload)
		if cleaned == "" {
			continue
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		if err := streamEmit(streamID, []byte(cleaned)); err != nil {
			publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, "stream_emit: "+err.Error())
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, "stream_emit: "+err.Error())
			return
		}
	}
	if err := scanner.Err(); err != nil {
		publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, err.Error())
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, err.Error())
		streamEmitError(streamID, "upstream stream read error: "+err.Error())
		return
	}
	if !collector.hasOutput {
		const msg = "upstream returned an empty completion"
		publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, msg)
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, msg)
		streamEmitError(streamID, msg)
		return
	}
	publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), false, 0, "")
	recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), false, "")
	invalidateAccountCredits(authID, authUID)
}

// collectUpstreamStream retries the synchronous stream collection exactly once
// on 401/403 after refreshing the credential. A failed refresh surfaces the
// original upstream error plus the refresh failure.
func collectUpstreamStream(sa *storedAuth, authID, requestedModel string, body []byte, requestID, sessionID string, sseFramed bool, collector *sseUsageCollector) ([]pluginapi.ExecutorStreamChunk, int, error) {
	chunks, status, err := collectUpstreamStreamOnce(sa, requestedModel, body, requestID, sessionID, sseFramed, collector)
	if err != nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		if rerr := refreshForRetry(authID, sa); rerr != nil {
			return nil, status, fmt.Errorf("%w (refresh failed: %v)", err, rerr)
		}
		return collectUpstreamStreamOnce(sa, requestedModel, body, requestID, sessionID, sseFramed, collector)
	}
	return chunks, status, err
}

func collectUpstreamStreamOnce(sa *storedAuth, requestedModel string, body []byte, requestID, sessionID string, sseFramed bool, collector *sseUsageCollector) ([]pluginapi.ExecutorStreamChunk, int, error) {
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
		return nil, status, fmt.Errorf("upstream %d: %s", status, truncateRedacted(string(raw), upstreamErrorLimit))
	}
	// Always drive one collector: it is the single definition of "output"
	// (content, reasoning_content, or tool_calls). Usage alone is not output.
	if collector == nil {
		collector = &sseUsageCollector{}
	}
	chunks := make([]pluginapi.ExecutorStreamChunk, 0, 64)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if msg := streamErrorMessage(payload); msg != "" {
			return chunks, 0, fmt.Errorf("upstream error: %s", truncateRedacted(msg, upstreamErrorLimit))
		}
		collector.feed(payload)
		cleaned := cleanChunkJSON(payload)
		if cleaned == "" {
			continue
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: json.RawMessage(cleaned)})
	}
	if err := scanner.Err(); err != nil {
		return chunks, 0, fmt.Errorf("upstream stream read error: %w", err)
	}
	if !collector.hasOutput {
		return chunks, 0, fmt.Errorf("upstream returned an empty completion")
	}
	return chunks, 0, nil
}
