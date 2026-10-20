package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteChatWithRetryRefreshesOnce(t *testing.T) {
	calls := 0
	var secondAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
			return
		}
		secondAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	oldURL, oldRefresh := upstreamChatURL, refreshForRetry
	upstreamChatURL = srv.URL
	refreshForRetry = func(sa *storedAuth) error {
		sa.Auth.AccessToken = "dt-refreshed"
		return nil
	}
	defer func() { upstreamChatURL, refreshForRetry = oldURL, oldRefresh }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	if _, _, err := executeChatWithRetry(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[{"role":"user","content":"hi"}]}`)); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if secondAuth != "Bearer dt-refreshed" {
		t.Fatalf("second auth = %q", secondAuth)
	}
}

// retryTestServer counts attempts and answers with status while it is 401/403,
// then with a valid SSE completion once the credential changed.
func retryTestServer(t *testing.T, calls *int, status int, alwaysFail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if alwaysFail || *calls == 1 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestExecuteChatWithRetrySkipsRefreshOnOtherStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		calls := 0
		srv := retryTestServer(t, &calls, status, true)
		oldURL, oldRefresh := upstreamChatURL, refreshForRetry
		upstreamChatURL = srv.URL
		refreshed := false
		refreshForRetry = func(sa *storedAuth) error {
			refreshed = true
			return nil
		}

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
		_, gotStatus, err := executeChatWithRetry(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
		upstreamChatURL, refreshForRetry = oldURL, oldRefresh
		srv.Close()

		if err == nil || gotStatus != status {
			t.Fatalf("status %d: got status %d err %v", status, gotStatus, err)
		}
		if calls != 1 {
			t.Fatalf("status %d: calls = %d, want 1", status, calls)
		}
		if refreshed {
			t.Fatalf("status %d: refresh must not run", status)
		}
	}
}

func TestExecuteChatWithRetryRetriesOnlyOnce(t *testing.T) {
	calls := 0
	srv := retryTestServer(t, &calls, http.StatusUnauthorized, true)
	defer srv.Close()
	oldURL, oldRefresh := upstreamChatURL, refreshForRetry
	upstreamChatURL = srv.URL
	refreshForRetry = func(sa *storedAuth) error {
		sa.Auth.AccessToken = "dt-refreshed"
		return nil
	}
	defer func() { upstreamChatURL, refreshForRetry = oldURL, oldRefresh }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	_, status, err := executeChatWithRetry(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("persistent 401 must surface: status %d err %v", status, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly 2", calls)
	}
}

func TestExecuteChatWithRetrySurfacesRefreshFailure(t *testing.T) {
	calls := 0
	srv := retryTestServer(t, &calls, http.StatusForbidden, true)
	defer srv.Close()
	oldURL, oldRefresh := upstreamChatURL, refreshForRetry
	upstreamChatURL = srv.URL
	refreshForRetry = func(sa *storedAuth) error { return errStubRefresh }
	defer func() { upstreamChatURL, refreshForRetry = oldURL, oldRefresh }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	_, status, err := executeChatWithRetry(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("status %d err %v", status, err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry without a refreshed credential)", calls)
	}
	for _, want := range []string{"token expired", "refresh failed", errStubRefresh.Error()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

var errStubRefresh = errors.New("refresh rejected: 400")

func TestCollectUpstreamStreamRetriesOnceOn401(t *testing.T) {
	calls := 0
	var secondAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"token expired"}}`))
			return
		}
		secondAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":1}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	oldURL, oldRefresh := upstreamChatURL, refreshForRetry
	upstreamChatURL = srv.URL
	refreshForRetry = func(sa *storedAuth) error {
		sa.Auth.AccessToken = "dt-refreshed"
		return nil
	}
	defer func() { upstreamChatURL, refreshForRetry = oldURL, oldRefresh }()

	body, rid, sid, err := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), upstreamUltimate)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	chunks, status, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, false, &sseUsageCollector{})
	if err != nil {
		t.Fatalf("collect retry failed: %v (status %d)", err, status)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if secondAuth != "Bearer dt-refreshed" {
		t.Fatalf("second auth = %q", secondAuth)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
}
