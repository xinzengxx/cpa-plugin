// campaign.go implements Qoder CN's current reward campaign flow. The app no
// longer uses /sash/api/v1/me/daily-check-in/* for the daily +100 credits;
// it reads /sash/api/v1/me/campaigns and claims CLAIM_BENEFIT campaigns via
// POST /sash/api/v1/me/campaigns/{campaignId}/claim.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	campaignClientType = "10"
	campaignVersion    = "0.4.8"
	campaignUserAgent  = "Qoder"

	// noCampaignMessage is the "nothing to claim" hint surfaced by the check-in
	// status/claim paths and as the activity label when no campaign is
	// visible. The upstream hides campaigns from requests carrying an unknown
	// device token, so the hint points at the fingerprint config.
	noCampaignMessage = "当前账号无可用签到活动（可配置 checkin_fingerprint 使用真实设备指纹）"
)

type campaignContent struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	DetailURL   string `json:"detailUrl"`
	ButtonText  string `json:"buttonText"`
}

type campaignPlacement struct {
	Type        string                     `json:"type"`
	CampaignURL string                     `json:"campaignUrl"`
	Content     map[string]campaignContent `json:"content"`
}

type campaignBenefit struct {
	Kind   string `json:"kind"`
	Amount int64  `json:"amount"`
}

type campaign struct {
	CampaignID  string              `json:"campaignId"`
	CampaignKey string              `json:"campaignKey"`
	ActionType  string              `json:"actionType"`
	StartAt     int64               `json:"startAt"`
	EndAt       int64               `json:"endAt"`
	ClaimStatus string              `json:"claimStatus"`
	Benefit     campaignBenefit     `json:"benefit"`
	Placements  []campaignPlacement `json:"placements"`
}

type campaignList struct {
	UID          string     `json:"uid"`
	ShowCampaign bool       `json:"showCampaign"`
	Claimable    bool       `json:"claimable"`
	CampaignURL  string     `json:"campaignUrl"`
	Campaigns    []campaign `json:"campaigns"`
}

func campaignMachineOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "windows"
	default:
		return runtime.GOOS
	}
}

// campaignHeaders mirrors the Qoder CN desktop client's campaign request:
// plain Bearer auth plus the client type/version and machine headers.
//
// The machine headers only carry a real device identity. Upstream answers an
// unknown Cosy-MachineToken with HTTP 200 and an empty campaign list rather
// than an error, so sending a made-up token silently hides every campaign.
// The COSY signing session is NOT reused here: its machine token is randomly
// generated per process and is valid only for the gateway, not for /sash.
//
// Without a configured fingerprint no machine token is sent at all — upstream
// then returns the account's campaigns normally.
func campaignHeaders(req *http.Request, sa *storedAuth) error {
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", campaignUserAgent)
	req.Header.Set("Cosy-ClientType", campaignClientType)
	req.Header.Set("Cosy-Version", campaignVersion)
	req.Header.Set("Cosy-MachineOS", campaignMachineOS())

	if fp := configuredFingerprint(); fp != nil {
		req.Header.Set("Cosy-MachineId", fp.MachineID)
		req.Header.Set("Cosy-MachineToken", fp.MachineToken)
		req.Header.Set("Cosy-MachineType", fp.MachineType)
		machineCode := fp.MachineCode
		if machineCode == "" {
			machineCode = fp.MachineID
		}
		req.Header.Set("Cosy-MachineCode", machineCode)
		hostname := fp.Hostname
		if hostname == "" {
			hostname, _ = os.Hostname()
		}
		if hostname != "" {
			req.Header.Set("Cosy-MachineHostname", hostname)
		}
		if fp.MachineOS != "" {
			req.Header.Set("Cosy-MachineOS", fp.MachineOS)
		}
	}
	return nil
}

func fetchCampaigns(sa *storedAuth) (*campaignList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamBaseCN+"/sash/api/v1/me/campaigns", nil)
	if err != nil {
		return nil, err
	}
	if err := campaignHeaders(req, sa); err != nil {
		return nil, err
	}
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("campaigns http %d body=%s", resp.StatusCode, truncateRedacted(string(resp.Body), 200))
	}
	var out campaignList
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("campaigns parse: %w", err)
	}
	return &out, nil
}

func claimCampaign(sa *storedAuth, campaignID string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := upstreamBaseCN + "/sash/api/v1/me/campaigns/" + url.PathEscape(campaignID) + "/claim"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	if err := campaignHeaders(req, sa); err != nil {
		return nil, err
	}
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return map[string]any{
			"success":     false,
			"message":     fmt.Sprintf("http %d: %s", resp.StatusCode, truncateRedacted(string(resp.Body), 200)),
			"status_code": resp.StatusCode,
		}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(resp.Body, &m); err != nil {
		return nil, fmt.Errorf("campaign claim parse: %w", err)
	}
	if e, ok := m["error"]; ok && e != nil {
		m["success"] = false
	} else if _, ok := m["success"]; !ok {
		m["success"] = true
	}
	return m, nil
}

// findCreditCampaign returns the credit campaign active in the current window
// and its state: "claimable", "claimed", or "" (none).
func findCreditCampaign(list *campaignList, now time.Time) (campaign, string) {
	if list == nil {
		return campaign{}, ""
	}
	nowSec := now.Unix()
	var claimed *campaign
	for i := range list.Campaigns {
		c := &list.Campaigns[i]
		if !strings.EqualFold(c.ActionType, "CLAIM_BENEFIT") {
			continue
		}
		if !strings.EqualFold(c.Benefit.Kind, "CREDITS") {
			continue
		}
		if c.StartAt > 0 && nowSec < c.StartAt {
			continue
		}
		if c.EndAt > 0 && nowSec >= c.EndAt {
			continue
		}
		switch strings.ToUpper(c.ClaimStatus) {
		case "CLAIMABLE":
			return *c, "claimable"
		case "CLAIMED":
			if claimed == nil {
				claimed = c
			}
		}
	}
	if claimed != nil {
		return *claimed, "claimed"
	}
	return campaign{}, ""
}

func campaignTitle(c campaign) string {
	for _, p := range c.Placements {
		for _, lang := range []string{"zh", "zh-CN", "en", "en-US"} {
			if content, ok := p.Content[lang]; ok && strings.TrimSpace(content.Title) != "" {
				return strings.TrimSpace(content.Title)
			}
		}
	}
	return "每日签到"
}

func campaignSummaryFromList(list *campaignList, now time.Time) *checkinSummary {
	c, state := findCreditCampaign(list, now)
	if state == "" {
		return &checkinSummary{
			Active:       false,
			ActivityName: noCampaignMessage,
		}
	}
	sum := &checkinSummary{
		Active:         true,
		TodayCheckedIn: state == "claimed",
		DailyCredit:    c.Benefit.Amount,
		ActivityName:   campaignTitle(c),
		CampaignID:     c.CampaignID,
		CampaignKey:    c.CampaignKey,
		CampaignTitle:  campaignTitle(c),
	}
	if state == "claimed" {
		sum.TodayCredit = c.Benefit.Amount
	}
	return sum
}

func fetchCampaignCheckinStatus(sa *storedAuth) (*checkinSummary, error) {
	list, err := fetchCampaigns(sa)
	if err != nil {
		return nil, err
	}
	return campaignSummaryFromList(list, time.Now()), nil
}
