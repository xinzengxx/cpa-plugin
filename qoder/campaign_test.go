package main

import (
	"net/http"
	"testing"
	"time"
)

func sampleCampaignList(now time.Time, creditStatus string) *campaignList {
	start := now.Add(-time.Hour).Unix()
	end := now.Add(time.Hour).Unix()
	return &campaignList{
		ShowCampaign: true,
		Claimable:    creditStatus == "CLAIMABLE",
		Campaigns: []campaign{
			{
				CampaignID:  "cid-credit",
				CampaignKey: "act-20261020-894",
				ActionType:  "CLAIM_BENEFIT",
				StartAt:     start,
				EndAt:       end,
				ClaimStatus: creditStatus,
				Benefit:     campaignBenefit{Kind: "CREDITS", Amount: 100},
				Placements: []campaignPlacement{
					{Type: "POPUP", Content: map[string]campaignContent{
						"zh": {Title: "每天领 100 Credits", Description: "每日 10:00 刷新"},
						"en": {Title: "Claim 100 Credits Daily"},
					}},
				},
			},
			{
				CampaignID:  "cid-details",
				CampaignKey: "act-20260901-922",
				ActionType:  "VIEW_DETAILS",
				StartAt:     start,
				EndAt:       end,
				ClaimStatus: "CLAIMED",
			},
		},
	}
}

func TestFindCreditCampaignClaimable(t *testing.T) {
	now := time.Unix(1792465000, 0)
	c, state := findCreditCampaign(sampleCampaignList(now, "CLAIMABLE"), now)
	if state != "claimable" || c.CampaignID != "cid-credit" {
		t.Fatalf("state=%q campaign=%+v", state, c)
	}
}

func TestFindCreditCampaignClaimed(t *testing.T) {
	now := time.Unix(1792465000, 0)
	c, state := findCreditCampaign(sampleCampaignList(now, "CLAIMED"), now)
	if state != "claimed" || c.CampaignID != "cid-credit" {
		t.Fatalf("state=%q campaign=%+v", state, c)
	}
}

func TestFindCreditCampaignSkipsExpiredAndViewDetails(t *testing.T) {
	now := time.Unix(1792465000, 0)
	list := sampleCampaignList(now, "CLAIMABLE")
	list.Campaigns[0].EndAt = now.Add(-time.Second).Unix()
	if _, state := findCreditCampaign(list, now); state != "" {
		t.Fatalf("expired campaign must be ignored, got %q", state)
	}
}

func TestCampaignSummaryFromList(t *testing.T) {
	now := time.Unix(1792465000, 0)
	sum := campaignSummaryFromList(sampleCampaignList(now, "CLAIMABLE"), now)
	if !sum.Active || sum.TodayCheckedIn || sum.DailyCredit != 100 {
		t.Fatalf("claimable summary: %+v", sum)
	}
	if sum.ActivityName != "每天领 100 Credits" || sum.CampaignID != "cid-credit" {
		t.Fatalf("claimable title/campaign: %+v", sum)
	}
	sum = campaignSummaryFromList(sampleCampaignList(now, "CLAIMED"), now)
	if !sum.Active || !sum.TodayCheckedIn || sum.TodayCredit != 100 {
		t.Fatalf("claimed summary: %+v", sum)
	}
	sum = campaignSummaryFromList(&campaignList{ShowCampaign: false}, now)
	if sum.Active || sum.TodayCheckedIn {
		t.Fatalf("empty summary: %+v", sum)
	}
}

func TestCampaignHeadersMatchDesktopClient(t *testing.T) {
	sa := &storedAuth{
		Auth: storedTokens{
			AccessToken:  "dt-test-token",
			RefreshToken: "drt-test-token",
		},
		Account: storedAccount{UID: "uid-test", Nickname: "tester"},
	}
	req, err := http.NewRequest(http.MethodGet, "https://openapi.qoder.com/sash/api/v1/me/campaigns", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := campaignHeaders(req, sa); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Cosy-ClientType": "10",
		"Cosy-Version":    "0.4.8",
		"User-Agent":      "Qoder",
		"Accept":          "application/json",
	}
	for k, v := range want {
		if got := req.Header.Get(k); got != v {
			t.Fatalf("%s: want %q got %q", k, v, got)
		}
	}
	for _, k := range []string{"Cosy-MachineId", "Cosy-MachineToken", "Cosy-MachineType", "Cosy-MachineCode"} {
		if req.Header.Get(k) == "" {
			t.Fatalf("missing %s", k)
		}
	}
}
