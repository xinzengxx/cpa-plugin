// campaign.go implements Qoder's reward campaign flow for Global accounts.
// The app no longer uses /sash/api/v1/me/daily-check-in/* for the
// daily +100 credits; it reads /sash/api/v1/me/campaigns and claims
// CLAIM_BENEFIT campaigns via POST /sash/api/v1/me/campaigns/{campaignId}/claim.
package main

import (
	"context"
	"crypto/md5"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
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

	// noCampaignMessage is the single "nothing to claim" hint surfaced by the
	// check-in status/claim paths and as the activity label when no campaign
	// is visible. Fresh Global accounts need one official-client claim first.
	noCampaignMessage = "当前账号无可用签到活动（国际版新号请先在官方 Qoder 客户端领取一次）"
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

// campaignHeaders mirrors the Qoder desktop client's campaign request: plain
// Bearer auth plus client type/version and machine headers. A configured
// fingerprint (checkin_fingerprint) wins; otherwise a stable per-account
// identity is derived from the UID. MachineCode falls back to MachineId and
// the hostname comes from the configured fingerprint or os.Hostname().
// Global accounts may see no CLAIMABLE campaign until the official client
// has claimed once.
func campaignHeaders(req *http.Request, sa *storedAuth) error {
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", campaignUserAgent)
	req.Header.Set("Cosy-ClientType", campaignClientType)
	req.Header.Set("Cosy-Version", campaignVersion)
	req.Header.Set("Cosy-MachineOS", campaignMachineOS())

	machineID := deriveMachineID(sa.Account.UID)
	machineToken := deriveMachineToken(sa.Account.UID)
	machineType := deriveMachineType(sa.Account.UID)
	machineCode := ""
	hostname := ""
	if fp := configuredFingerprint(); fp != nil {
		machineID = fp.MachineID
		machineToken = fp.MachineToken
		machineType = fp.MachineType
		machineCode = fp.MachineCode
		hostname = fp.Hostname
		if fp.MachineOS != "" {
			req.Header.Set("Cosy-MachineOS", fp.MachineOS)
		}
	}
	if machineCode == "" {
		machineCode = machineID
	}
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	req.Header.Set("Cosy-MachineId", machineID)
	req.Header.Set("Cosy-MachineToken", machineToken)
	req.Header.Set("Cosy-MachineType", machineType)
	req.Header.Set("Cosy-MachineCode", machineCode)
	if hostname != "" {
		req.Header.Set("Cosy-MachineHostname", hostname)
	}
	return nil
}

func deriveMachineID(uid string) string {
	sum := md5.Sum([]byte("machine:" + uid))
	return hex.EncodeToString(sum[:])
}

func deriveMachineToken(uid string) string {
	sum := sha512.Sum512([]byte("machinetoken:" + uid))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:43]
}

func deriveMachineType(uid string) string {
	sum := md5.Sum([]byte("machinetype:" + uid))
	return hex.EncodeToString(sum[:])[:18]
}

func fetchCampaigns(sa *storedAuth) (*campaignList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamBase+"/sash/api/v1/me/campaigns", nil)
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
	endpoint := upstreamBase + "/sash/api/v1/me/campaigns/" + url.PathEscape(campaignID) + "/claim"
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
