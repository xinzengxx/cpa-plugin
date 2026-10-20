// refresh_test.go covers credential-refresh routing, dead-session
// classification, and the executor-side persist/serialize contract.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	refreshDeviceOK = `{"token":"dt-new","refresh_token":"drt-new","expires_in":2592000000}`
	refreshJobOK    = `{"token":"jt-new","refresh_token":"jrt-new","expires_in":86400000}`
	refreshPATOK    = `{"token":"jt-pat","refresh_token":"jrt-pat"}`
)

// overrideRefreshEndpoints points the three refresh endpoints at base for the
// duration of the test.
func overrideRefreshEndpoints(t *testing.T, base string) {
	t.Helper()
	oldDevice, oldJobRefresh, oldJobExchange := endpointDeviceTokenRefresh, endpointJobTokenRefresh, endpointJobTokenExchange
	endpointDeviceTokenRefresh = base + "/deviceToken/refresh"
	endpointJobTokenRefresh = base + "/jobToken/refresh"
	endpointJobTokenExchange = base + "/jobToken/exchange"
	t.Cleanup(func() {
		endpointDeviceTokenRefresh = oldDevice
		endpointJobTokenRefresh = oldJobRefresh
		endpointJobTokenExchange = oldJobExchange
	})
}

func TestRefreshCallRoutesByTokenFamily(t *testing.T) {
	t.Run("drt uses deviceToken refresh only", func(t *testing.T) {
		var device, jobRefresh, jobExchange int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/deviceToken/refresh":
				atomic.AddInt32(&device, 1)
				_, _ = w.Write([]byte(refreshDeviceOK))
			case "/jobToken/refresh":
				atomic.AddInt32(&jobRefresh, 1)
				_, _ = w.Write([]byte(refreshJobOK))
			case "/jobToken/exchange":
				atomic.AddInt32(&jobExchange, 1)
				_, _ = w.Write([]byte(refreshPATOK))
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		}))
		defer srv.Close()
		overrideRefreshEndpoints(t, srv.URL)

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old", PersonalToken: "pt-fallback"}}
		data, _, status, err := refreshCall(sa)
		if err != nil {
			t.Fatalf("refreshCall: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		var tok deviceTokenResponse
		if err := json.Unmarshal(data, &tok); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if tok.accessToken() != "dt-new" || tok.RefreshToken != "drt-new" {
			t.Fatalf("device token = %+v", tok)
		}
		if device != 1 || jobRefresh != 0 || jobExchange != 0 {
			t.Fatalf("hits device=%d jobRefresh=%d jobExchange=%d, want 1/0/0", device, jobRefresh, jobExchange)
		}
	})

	t.Run("jrt uses jobToken refresh", func(t *testing.T) {
		var device, jobRefresh, jobExchange int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/deviceToken/refresh":
				atomic.AddInt32(&device, 1)
			case "/jobToken/refresh":
				atomic.AddInt32(&jobRefresh, 1)
				_, _ = w.Write([]byte(refreshJobOK))
			case "/jobToken/exchange":
				atomic.AddInt32(&jobExchange, 1)
			}
		}))
		defer srv.Close()
		overrideRefreshEndpoints(t, srv.URL)

		sa := &storedAuth{Auth: storedTokens{AccessToken: "jt-old", RefreshToken: "jrt-old"}}
		data, _, _, err := refreshCall(sa)
		if err != nil {
			t.Fatalf("refreshCall: %v", err)
		}
		var tok jobTokenResponse
		if err := json.Unmarshal(data, &tok); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if tok.Token != "jt-new" || tok.RefreshToken != "jrt-new" {
			t.Fatalf("job token = %+v", tok)
		}
		if jobRefresh != 1 || device != 0 || jobExchange != 0 {
			t.Fatalf("hits jobRefresh=%d device=%d jobExchange=%d, want 1/0/0", jobRefresh, device, jobExchange)
		}
	})

	t.Run("PAT is only the jrt fallback", func(t *testing.T) {
		var device, jobRefresh, jobExchange int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/deviceToken/refresh":
				atomic.AddInt32(&device, 1)
			case "/jobToken/refresh":
				atomic.AddInt32(&jobRefresh, 1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"TOKEN_EXPIRE","message":"refresh token expired"}`))
			case "/jobToken/exchange":
				atomic.AddInt32(&jobExchange, 1)
				_, _ = w.Write([]byte(refreshPATOK))
			}
		}))
		defer srv.Close()
		overrideRefreshEndpoints(t, srv.URL)

		sa := &storedAuth{Auth: storedTokens{AccessToken: "jt-old", RefreshToken: "jrt-old", PersonalToken: "pt-x"}}
		data, _, status, err := refreshCall(sa)
		if err != nil {
			t.Fatalf("PAT fallback should rescue the refresh: %v", err)
		}
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200", status)
		}
		var tok jobTokenResponse
		if err := json.Unmarshal(data, &tok); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if tok.Token != "jt-pat" {
			t.Fatalf("token = %q, want jt-pat", tok.Token)
		}
		if jobRefresh != 1 || jobExchange != 1 || device != 0 {
			t.Fatalf("hits jobRefresh=%d jobExchange=%d device=%d, want 1/1/0", jobRefresh, jobExchange, device)
		}
	})

	t.Run("drt never falls back to the PAT", func(t *testing.T) {
		var jobExchange int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/deviceToken/refresh":
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"TOKEN_EXPIRE","message":"dead session"}`))
			case "/jobToken/exchange":
				atomic.AddInt32(&jobExchange, 1)
			}
		}))
		defer srv.Close()
		overrideRefreshEndpoints(t, srv.URL)

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old", PersonalToken: "pt-x"}}
		_, _, status, err := refreshCall(sa)
		if err == nil || status != http.StatusUnauthorized {
			t.Fatalf("status = %d err = %v, want 401", status, err)
		}
		if jobExchange != 0 {
			t.Fatalf("PAT exchange hit %d times; drt- must never fall back", jobExchange)
		}
	})
}

// TestRefreshCallPreservesSessionDeadBody is the regression test for the
// unreachable dead-session detection: the raw 401 body must reach the
// classifier.
func TestRefreshCallPreservesSessionDeadBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"TOKEN_EXPIRE","message":"Offline user session not found"}`))
	}))
	defer srv.Close()
	overrideRefreshEndpoints(t, srv.URL)

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	_, raw, status, err := refreshCall(sa)
	if err == nil || status != http.StatusUnauthorized {
		t.Fatalf("status = %d err = %v, want 401", status, err)
	}
	if !strings.Contains(string(raw), "TOKEN_EXPIRE") {
		t.Fatalf("raw body lost on error: %q", raw)
	}
	if !isSessionDeadError(refreshFailureText(raw, err)) {
		t.Fatalf("raw body not classified as session-dead: %q", raw)
	}
	// doRawJSON embeds the body in its error string too, so classification
	// still works when only the error is available.
	if !isSessionDeadError(err.Error()) {
		t.Fatalf("error text not classified as session-dead: %v", err)
	}
}

func TestIsSessionDeadError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"token expired", false},
		{"refresh rejected (HTTP 400)", false},
		{"TOKEN_EXPIRE", true},
		{`http_error: upstream 401 body={"code":"TOKEN_EXPIRE","message":"x"}`, true},
		{"code 12153", true},
		{"Offline user session not found", true},
	}
	for _, tc := range tests {
		if got := isSessionDeadError(tc.msg); got != tc.want {
			t.Errorf("isSessionDeadError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestRefreshStoredTokensPersistsRotatedPair(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(refreshDeviceOK))
	}))
	defer srv.Close()
	overrideRefreshEndpoints(t, srv.URL)

	oldResolver, oldLoad, oldPersist := refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth
	defer func() {
		refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth = oldResolver, oldLoad, oldPersist
	}()
	refreshTargetResolver = func(authID string, sa *storedAuth) string {
		if authID != "auth-1" {
			t.Errorf("resolver authID = %q", authID)
		}
		return "idx-1"
	}
	loadAuthForRefresh = func(string) (*storedAuth, error) { return nil, errors.New("no host") }
	var persistedIndex string
	var persistedTokens storedTokens
	persistRefreshedAuth = func(authIndex string, sa *storedAuth) error {
		persistedIndex = authIndex
		persistedTokens = sa.Auth
		return nil
	}

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old", ExpiresAt: 1},
		Account: storedAccount{UID: "uid-1"},
	}
	if err := refreshStoredTokens("auth-1", sa); err != nil {
		t.Fatalf("refreshStoredTokens: %v", err)
	}
	if sa.Auth.AccessToken != "dt-new" || sa.Auth.RefreshToken != "drt-new" {
		t.Fatalf("in-memory tokens not updated: %+v", sa.Auth)
	}
	if persistedIndex != "idx-1" {
		t.Fatalf("persisted index = %q", persistedIndex)
	}
	if persistedTokens.AccessToken != "dt-new" || persistedTokens.RefreshToken != "drt-new" {
		t.Fatalf("persisted tokens = %+v", persistedTokens)
	}
	if persistedTokens.ExpiresAt <= 1 {
		t.Fatalf("expiry not refreshed: %d", persistedTokens.ExpiresAt)
	}
}

// TestRefreshStoredTokensAdoptsConcurrentRotation proves a waiter refreshes
// with the pair persisted by the winner, not the already-rotated one it
// parsed from its original request.
func TestRefreshStoredTokensAdoptsConcurrentRotation(t *testing.T) {
	var gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotRefresh = body.RefreshToken
		_, _ = w.Write([]byte(`{"token":"dt-new2","refresh_token":"drt-new2","expires_in":1000}`))
	}))
	defer srv.Close()
	overrideRefreshEndpoints(t, srv.URL)

	oldResolver, oldLoad, oldPersist := refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth
	defer func() {
		refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth = oldResolver, oldLoad, oldPersist
	}()
	refreshTargetResolver = func(string, *storedAuth) string { return "idx-1" }
	loadAuthForRefresh = func(string) (*storedAuth, error) {
		return &storedAuth{Auth: storedTokens{AccessToken: "dt-new", RefreshToken: "drt-new"}}, nil
	}
	persistRefreshedAuth = func(string, *storedAuth) error { return nil }

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	if err := refreshStoredTokens("auth-1", sa); err != nil {
		t.Fatalf("refreshStoredTokens: %v", err)
	}
	if gotRefresh != "drt-new" {
		t.Fatalf("refreshed with %q, want the concurrently persisted drt-new", gotRefresh)
	}
	if sa.Auth.RefreshToken != "drt-new2" {
		t.Fatalf("final refresh token = %q", sa.Auth.RefreshToken)
	}
}

func TestRefreshStoredTokensRefusesWithoutHostIndex(t *testing.T) {
	oldResolver := refreshTargetResolver
	defer func() { refreshTargetResolver = oldResolver }()
	refreshTargetResolver = func(string, *storedAuth) string { return "" }

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"}}
	err := refreshStoredTokens("auth-1", sa)
	if err == nil || !strings.Contains(err.Error(), "cannot persist") {
		t.Fatalf("err = %v, want a clear persistence error", err)
	}
	if sa.Auth.AccessToken != "dt-old" || sa.Auth.RefreshToken != "drt-old" {
		t.Fatalf("tokens mutated without persistence: %+v", sa.Auth)
	}
}

func TestRefreshStoredTokensSerializesPerAuth(t *testing.T) {
	var inFlight, maxInFlight int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			prev := atomic.LoadInt32(&maxInFlight)
			if cur <= prev || atomic.CompareAndSwapInt32(&maxInFlight, prev, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		_, _ = w.Write([]byte(refreshDeviceOK))
	}))
	defer srv.Close()
	overrideRefreshEndpoints(t, srv.URL)

	oldResolver, oldLoad, oldPersist := refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth
	defer func() {
		refreshTargetResolver, loadAuthForRefresh, persistRefreshedAuth = oldResolver, oldLoad, oldPersist
	}()
	refreshTargetResolver = func(string, *storedAuth) string { return "idx-1" }
	loadAuthForRefresh = func(string) (*storedAuth, error) { return nil, errors.New("no host") }
	persistRefreshedAuth = func(string, *storedAuth) error { return nil }

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sa := &storedAuth{
				Auth:    storedTokens{AccessToken: "dt-old", RefreshToken: "drt-old"},
				Account: storedAccount{UID: "uid-1"},
			}
			if err := refreshStoredTokens("auth-1", sa); err != nil {
				t.Errorf("refreshStoredTokens: %v", err)
			}
		}()
	}
	wg.Wait()
	if maxInFlight != 1 {
		t.Fatalf("max concurrent refreshes = %d, want 1", maxInFlight)
	}
}
