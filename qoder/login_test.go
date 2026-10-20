// login_test.go covers the spec §5.1 post-login identity/pro-upgrade steps
// without live OAuth: the decision helpers are exercised directly.
package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRequireUserInfo(t *testing.T) {
	if _, err := requireUserInfo(nil, nil); err == nil {
		t.Fatal("nil userinfo must fail")
	}
	if _, err := requireUserInfo(&userInfoResponse{}, nil); err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("empty user_id error = %v", err)
	}
	if _, err := requireUserInfo(nil, errors.New("boom")); err == nil {
		t.Fatal("upstream error must fail")
	}
	ui, err := requireUserInfo(&userInfoResponse{ID: "uid-1", Name: "Alice"}, nil)
	if err != nil || ui.ID != "uid-1" {
		t.Fatalf("valid userinfo rejected: %+v / %v", ui, err)
	}
}

func TestFinalizeDeviceLoginUsesUserInfoIdentity(t *testing.T) {
	tok := &deviceTokenResponse{Token: "dt-x", RefreshToken: "drt-x", UserID: "poll-uid"}
	sa, err := finalizeDeviceLogin(tok, func(jt string) (*userInfoResponse, error) {
		if jt != "dt-x" {
			t.Errorf("userinfo called with %q, want dt-x", jt)
		}
		return &userInfoResponse{ID: "ui-uid", Name: "Alice"}, nil
	})
	if err != nil {
		t.Fatalf("finalizeDeviceLogin: %v", err)
	}
	if sa.Account.UID != "ui-uid" || sa.Account.Nickname != "Alice" {
		t.Fatalf("account = %+v, want userinfo identity", sa.Account)
	}
	if sa.Auth.AccessToken != "dt-x" || sa.Auth.RefreshToken != "drt-x" {
		t.Fatalf("tokens = %+v", sa.Auth)
	}
}

func TestFinalizeDeviceLoginRejectsMissingUID(t *testing.T) {
	tok := &deviceTokenResponse{Token: "dt-x", RefreshToken: "drt-x", UserID: "poll-uid"}
	tests := []struct {
		name  string
		fetch userInfoFetcher
	}{
		{
			name:  "nil userinfo",
			fetch: func(string) (*userInfoResponse, error) { return nil, nil },
		},
		{
			name:  "empty user_id",
			fetch: func(string) (*userInfoResponse, error) { return &userInfoResponse{Name: "x"}, nil },
		},
		{
			name:  "userinfo error",
			fetch: func(string) (*userInfoResponse, error) { return nil, errors.New("boom") },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := finalizeDeviceLogin(tok, tc.fetch); err == nil {
				t.Fatal("missing identity must fail the login")
			}
		})
	}
	if _, err := finalizeDeviceLogin(&deviceTokenResponse{}, func(string) (*userInfoResponse, error) {
		return &userInfoResponse{ID: "uid"}, nil
	}); err == nil {
		t.Fatal("empty access token must fail")
	}
}

func TestAutoClaimProUpgrade(t *testing.T) {
	newServer := func(t *testing.T, eligible bool, claimBody string, eligibilityStatus int) (*httptest.Server, *int32) {
		t.Helper()
		var claimHits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/eligibility":
				if eligibilityStatus != 0 {
					w.WriteHeader(eligibilityStatus)
					return
				}
				if eligible {
					_, _ = w.Write([]byte(`{"eligible":true}`))
				} else {
					_, _ = w.Write([]byte(`{"eligible":false}`))
				}
			case "/claim":
				atomic.AddInt32(&claimHits, 1)
				_, _ = w.Write([]byte(claimBody))
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		}))
		return srv, &claimHits
	}

	t.Run("eligible claims once", func(t *testing.T) {
		srv, claimHits := newServer(t, true, `{"success":true}`, 0)
		defer srv.Close()
		oldElig, oldClaim := endpointProUpgradeEligibility, endpointProUpgrade
		defer func() { endpointProUpgradeEligibility, endpointProUpgrade = oldElig, oldClaim }()
		endpointProUpgradeEligibility = srv.URL + "/eligibility"
		endpointProUpgrade = srv.URL + "/claim"

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-x"}}
		if err := autoClaimProUpgrade(sa); err != nil {
			t.Fatalf("autoClaimProUpgrade: %v", err)
		}
		if got := atomic.LoadInt32(claimHits); got != 1 {
			t.Fatalf("claim hits = %d, want 1", got)
		}
	})

	t.Run("not eligible does not claim", func(t *testing.T) {
		srv, claimHits := newServer(t, false, `{"success":true}`, 0)
		defer srv.Close()
		oldElig, oldClaim := endpointProUpgradeEligibility, endpointProUpgrade
		defer func() { endpointProUpgradeEligibility, endpointProUpgrade = oldElig, oldClaim }()
		endpointProUpgradeEligibility = srv.URL + "/eligibility"
		endpointProUpgrade = srv.URL + "/claim"

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-x"}}
		if err := autoClaimProUpgrade(sa); err != nil {
			t.Fatalf("autoClaimProUpgrade: %v", err)
		}
		if got := atomic.LoadInt32(claimHits); got != 0 {
			t.Fatalf("claim hits = %d, want 0", got)
		}
	})

	t.Run("eligibility failure is returned for logging only", func(t *testing.T) {
		srv, claimHits := newServer(t, false, `{}`, http.StatusInternalServerError)
		defer srv.Close()
		oldElig, oldClaim := endpointProUpgradeEligibility, endpointProUpgrade
		defer func() { endpointProUpgradeEligibility, endpointProUpgrade = oldElig, oldClaim }()
		endpointProUpgradeEligibility = srv.URL + "/eligibility"
		endpointProUpgrade = srv.URL + "/claim"

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-x"}}
		if err := autoClaimProUpgrade(sa); err == nil {
			t.Fatal("eligibility failure must be surfaced to the caller (logged, not fatal)")
		}
		if got := atomic.LoadInt32(claimHits); got != 0 {
			t.Fatalf("claim hits = %d, want 0", got)
		}
	})

	t.Run("claim rejection is reported", func(t *testing.T) {
		srv, claimHits := newServer(t, true, `{"success":false,"message":"already claimed"}`, 0)
		defer srv.Close()
		oldElig, oldClaim := endpointProUpgradeEligibility, endpointProUpgrade
		defer func() { endpointProUpgradeEligibility, endpointProUpgrade = oldElig, oldClaim }()
		endpointProUpgradeEligibility = srv.URL + "/eligibility"
		endpointProUpgrade = srv.URL + "/claim"

		sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-x"}}
		err := autoClaimProUpgrade(sa)
		if err == nil || !strings.Contains(err.Error(), "already claimed") {
			t.Fatalf("claim rejection error = %v", err)
		}
		if got := atomic.LoadInt32(claimHits); got != 1 {
			t.Fatalf("claim hits = %d, want 1", got)
		}
	})
}
