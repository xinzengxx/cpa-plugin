package main

import (
	"os"
	"testing"
	"time"
)

// Live verification of the CN check-in flow against the real upstream.
//
// Skipped unless QODER_CN_ACCESS_TOKEN is set — it exercises a real account
// and must never run in CI.
//
//	QODER_CN_ACCESS_TOKEN=<jt- token> go test -run TestLiveCNCheckin -v
func TestLiveCNCheckin(t *testing.T) {
	token := os.Getenv("QODER_CN_ACCESS_TOKEN")
	if token == "" {
		t.Skip("QODER_CN_ACCESS_TOKEN not set")
	}
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: token},
		Account: storedAccount{UID: "live-test", Nickname: "live"},
	}

	sum, err := fetchCheckinStatus(sa)
	if err != nil {
		t.Fatalf("fetchCheckinStatus: %v", err)
	}
	t.Logf("Active=%v TodayCheckedIn=%v Activity=%q CampaignKey=%q CampaignID=%q",
		sum.Active, sum.TodayCheckedIn, sum.ActivityName, sum.CampaignKey, sum.CampaignID)
	if !sum.Active {
		t.Fatalf("expected an active campaign, got %+v", sum)
	}

	res, err := performCheckinCall(sa)
	if err != nil {
		t.Fatalf("performCheckinCall: %v", err)
	}
	t.Logf("claim result: %+v", res)

	// The upstream answers a successful claim with success=true; an already
	// claimed campaign must be normalised to skipped/already.
	success, _ := res["success"].(bool)
	if !success {
		t.Fatalf("claim did not succeed: %+v", res)
	}
	_ = time.Now
}
