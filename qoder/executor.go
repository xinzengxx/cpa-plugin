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
	"sync"
	"time"

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
		return nil, status, fmt.Errorf("upstream %d: %s", status, truncateRedacted(string(raw), upstreamErrorLimit))
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

// refreshForRetry is injectable so tests do not hit the network.
var refreshForRetry = refreshStoredTokens

// Hooks around host auth-store access, overridable in tests (the cgo host
// bridge is unavailable there).
var (
	refreshTargetResolver = resolveRefreshAuthIndex
	loadAuthForRefresh    = hostAuthGet
	persistRefreshedAuth  = persistAuthTokens
)

// refreshLocks serializes credential refreshes per auth. Keyed by UID when
// known (AuthID may be an opaque runtime index), then AuthID / refresh token.
var refreshLocks sync.Map // key -> *sync.Mutex

func refreshLockFor(keys ...string) *sync.Mutex {
	key := "qoder"
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			key = k
			break
		}
	}
	actual, _ := refreshLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// resolveRefreshAuthIndex maps the executor's AuthID (runtime index, record
// ID, or account UID) to a host auth index. The UID is tried as a fallback so
// a renamed/opaque AuthID still resolves.
func resolveRefreshAuthIndex(authID string, sa *storedAuth) string {
	candidates := []string{strings.TrimSpace(authID)}
	if sa != nil {
		candidates = append(candidates, strings.TrimSpace(sa.Account.UID))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if idx, _ := resolveAuthIndexAndID(c); idx != "" {
			return idx
		}
	}
	return ""
}

// refreshStoredTokens refreshes the credential for one auth and persists the
// new pair through the keepalive path before exposing it to the retry.
//
// drt- rotates on every refresh, so an in-memory-only update would leave the
// auth file holding a dead refresh token after the next 401. The whole
// operation is serialized per auth, and any credential a concurrent refresh
// persisted while we waited is adopted first so an already-rotated token is
// never replayed. When no host auth index can be resolved, the refresh is
// refused rather than silently rotating an unpersisted token.
func refreshStoredTokens(authID string, sa *storedAuth) error {
	if sa == nil {
		return fmt.Errorf("no auth to refresh")
	}
	mu := refreshLockFor(sa.Account.UID, authID, sa.Auth.RefreshToken, sa.Auth.PersonalToken)
	mu.Lock()
	defer mu.Unlock()

	authIndex := refreshTargetResolver(authID, sa)
	if authIndex == "" {
		return fmt.Errorf("cannot persist refreshed tokens: no host auth index for auth %q (uid %q); refusing to rotate tokens in memory only", authID, sa.Account.UID)
	}
	// Adopt a pair persisted by a concurrent refresh (host auto-refresh,
	// another 401 retry, or keepalive) while this request waited on the lock.
	if disk, err := loadAuthForRefresh(authIndex); err == nil && disk != nil {
		if drt := strings.TrimSpace(disk.Auth.RefreshToken); drt != "" && drt != strings.TrimSpace(sa.Auth.RefreshToken) {
			sa.Auth.AccessToken = disk.Auth.AccessToken
			sa.Auth.RefreshToken = disk.Auth.RefreshToken
			sa.Auth.ExpiresAt = disk.Auth.ExpiresAt
			if strings.TrimSpace(disk.Auth.PersonalToken) != "" {
				sa.Auth.PersonalToken = disk.Auth.PersonalToken
			}
		}
	}

	next := sa.Auth
	rt := strings.TrimSpace(next.RefreshToken)
	switch {
	case strings.HasPrefix(rt, "drt-"):
		tok, err := refreshDeviceToken(rt)
		if err != nil {
			return err
		}
		next.AccessToken = tok.accessToken()
		next.RefreshToken = firstNonEmpty(tok.RefreshToken, rt)
		next.ExpiresAt = preserveExpiry(deviceExpiryUnix(tok), next.ExpiresAt)
	case strings.HasPrefix(rt, "jrt-"):
		tok, err := refreshJobToken(rt)
		if err != nil {
			return err
		}
		next.AccessToken = tok.Token
		next.RefreshToken = firstNonEmpty(tok.RefreshToken, rt)
		next.ExpiresAt = preserveExpiry(time.Now().Add(time.Duration(tok.ExpiresIn)*time.Millisecond).Unix(), next.ExpiresAt)
	case strings.HasPrefix(strings.TrimSpace(next.PersonalToken), "pt-"):
		tok, err := exchangePATForJobToken(next.PersonalToken)
		if err != nil {
			return err
		}
		next.AccessToken = tok.Token
		next.RefreshToken = tok.RefreshToken
		next.ExpiresAt = preserveExpiry(time.Now().Add(time.Duration(tok.ExpiresIn)*time.Millisecond).Unix(), next.ExpiresAt)
	default:
		return fmt.Errorf("no refresh credential")
	}

	updated := *sa
	updated.Auth = next
	if err := persistRefreshedAuth(authIndex, &updated); err != nil {
		return fmt.Errorf("refresh succeeded but persist failed: %w", err)
	}
	sa.Auth = next
	return nil
}

// executeChatWithRetry retries exactly once on 401/403 after refreshing the
// credential. Any other status (400/404/429/5xx) is returned as-is without a
// refresh. A failed refresh surfaces the original upstream error plus the
// refresh failure.
func executeChatWithRetry(sa *storedAuth, authID, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error) {
	completion, status, err := executeChatOnce(sa, requestedModel, upstreamModel, payload)
	if err == nil || (status != http.StatusUnauthorized && status != http.StatusForbidden) {
		return completion, status, err
	}
	if rerr := refreshForRetry(authID, sa); rerr != nil {
		return nil, status, fmt.Errorf("%w (refresh failed: %v)", err, rerr)
	}
	return executeChatOnce(sa, requestedModel, upstreamModel, payload)
}

// completionLooksEmpty reports whether a folded completion carries no model
// output. Usage alone does NOT count: a content-less completion with token
// counts is still an empty 200 and must fail the guard.
func completionLooksEmpty(completion []byte) bool {
	var c struct {
		Choices []struct {
			Message map[string]any `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(completion, &c) != nil {
		return true
	}
	for _, ch := range c.Choices {
		if s, _ := ch.Message["content"].(string); strings.TrimSpace(s) != "" {
			return false
		}
		if tc, ok := ch.Message["tool_calls"].([]any); ok && len(tc) > 0 {
			return false
		}
		if s, _ := ch.Message["reasoning_content"].(string); strings.TrimSpace(s) != "" {
			return false
		}
	}
	return true
}
