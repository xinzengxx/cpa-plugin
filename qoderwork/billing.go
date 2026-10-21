// billing.go owns the upstream billing API surface: check-in status, user
// resource (credits / packages), payment type, and the perform-* call wrappers
// for daily check-in. Includes the shared JSON helpers used to tolerate the
// upstream's loosely-typed response shapes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func billingHeaders(req *http.Request, sa *storedAuth) {
	// QoderWork billing endpoints authenticate with the active token as a
	// plain Bearer — jobToken (jt-) or device token (dt-), both accepted
	// upstream (verified live 2026-07-27). No COSY signing (KNOWLEDGE §2).
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
}

// checkinStatusResponse mirrors GET /sash/api/v1/me/daily-check-in/status
// (plain JSON, no envelope).
type checkinStatusResponse struct {
	Status             string `json:"status"` // CLAIMABLE | CLAIMED
	RewardCredits      int64  `json:"rewardCredits"`
	NextClaimAt        int64  `json:"nextClaimAt"` // s epoch
	CurrentStreakDays  int64  `json:"currentStreakDays"`
	TotalClaimDays     int64  `json:"totalClaimDays"`
	TotalRewardCredits int64  `json:"totalRewardCredits"`
	LastClaimedAt      int64  `json:"lastClaimedAt"`   // s epoch
	RewardExpiresAt    int64  `json:"rewardExpiresAt"` // s epoch
}

func fetchLegacyCheckinStatus(sa *storedAuth) (*checkinSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamBaseCN+"/sash/api/v1/me/daily-check-in/status", nil)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("checkin status http %d body=%s", resp.StatusCode, truncateRedacted(string(resp.Body), 200))
	}
	var q checkinStatusResponse
	if err := json.Unmarshal(resp.Body, &q); err != nil {
		return nil, fmt.Errorf("checkin status parse: %w", err)
	}
	today := time.Now().Format("2006-01-02")
	lastClaimed := ""
	if q.LastClaimedAt > 0 {
		lastClaimed = time.Unix(q.LastClaimedAt, 0).Format("2006-01-02")
	}
	sum := &checkinSummary{
		Active:          q.Status == "CLAIMABLE" || q.Status == "CLAIMED",
		TodayCheckedIn:  q.Status == "CLAIMED" && lastClaimed == today,
		StreakDays:      q.CurrentStreakDays,
		DailyCredit:     q.RewardCredits,
		TodayCredit:     0,
		TotalCredits:    q.TotalRewardCredits,
		WeekCheckinDays: q.TotalClaimDays,
		ActivityName:    "每日签到",
	}
	if sum.TodayCheckedIn {
		sum.TodayCredit = q.RewardCredits
	}
	return sum, nil
}

// fetchCheckinStatus prefers the current campaign system (act-YYYYMMDD-*,
// daily 10:00 UTC+8 reset) and falls back to the legacy /daily-check-in
// endpoint when the campaign API is unavailable or reports no campaign.
//
// The fallback covers two distinct failure shapes: a transport/HTTP error, and
// a successful response carrying an empty campaign list. The latter matters
// because upstream answers an unusable device fingerprint with HTTP 200 and an
// empty list, which would otherwise be reported as "no campaign available".
//
// The legacy endpoint is only called when the campaign path yields nothing, so
// the normal path stays at one upstream request.
func fetchCheckinStatus(sa *storedAuth) (*checkinSummary, error) {
	campaignSum, campaignErr := fetchCampaignCheckinStatus(sa)
	if campaignErr == nil && campaignSum != nil && campaignSum.Active {
		return campaignSum, nil
	}

	legacySum, legacyErr := fetchLegacyCheckinStatus(sa)
	if legacyErr == nil && legacySum != nil && legacySum.Active {
		return legacySum, nil
	}

	// Neither path offers an active campaign. Report the campaign result when
	// the campaign API answered (even with an empty list), otherwise surface
	// the combined error.
	if campaignErr == nil && campaignSum != nil {
		return campaignSum, nil
	}
	if legacyErr == nil && legacySum != nil {
		return legacySum, nil
	}
	return nil, fmt.Errorf("campaign: %v; legacy: %v", campaignErr, legacyErr)
}

// quotaUsageResponse mirrors GET /api/v2/quota/usage response (plain JSON,
// no envelope). Both userQuota (base credits) and addOnQuota (one-time pro
// upgrade + checkin packs) are summed for the panel.
type quotaUsageResponse struct {
	UserID               string  `json:"userId"`
	UserType             string  `json:"userType"`
	UsageType            string  `json:"usageType"`
	TotalUsagePercentage float64 `json:"totalUsagePercentage"`
	IsQuotaExceeded      bool    `json:"isQuotaExceeded"`
	ExpiresAt            int64   `json:"expiresAt"` // ms epoch
	UpgradeURL           string  `json:"upgradeUrl"`
	UserQuota            struct {
		Total     float64 `json:"total"`
		Used      float64 `json:"used"`
		Remaining float64 `json:"remaining"`
		Unit      string  `json:"unit"`
	} `json:"userQuota"`
	AddOnQuota struct {
		Total     float64 `json:"total"`
		Used      float64 `json:"used"`
		Remaining float64 `json:"remaining"`
	} `json:"addOnQuota"`
}

// fetchUserResource queries QoderWork's quota endpoint and aggregates base +
// add-on credits into the panel's creditsSummary shape.
func fetchUserResource(sa *storedAuth) (*creditsSummary, error) {
	req, err := http.NewRequest(http.MethodGet, upstreamBaseCN+"/api/v2/quota/usage", nil)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("quota/usage http %d body=%s", resp.StatusCode, truncateRedacted(string(resp.Body), 200))
	}
	var q quotaUsageResponse
	if err := json.Unmarshal(resp.Body, &q); err != nil {
		return nil, fmt.Errorf("quota/usage parse: %w", err)
	}
	sum := &creditsSummary{
		TotalRemain: int64(q.UserQuota.Remaining + q.AddOnQuota.Remaining),
		TotalUsed:   int64(q.UserQuota.Used + q.AddOnQuota.Used),
		TotalSize:   int64(q.UserQuota.Total + q.AddOnQuota.Total),
		PackCount:   2,
		Packages: []packageSummary{
			{Name: "基础额度", Remain: int64(q.UserQuota.Remaining), Used: int64(q.UserQuota.Used), Size: int64(q.UserQuota.Total)},
			{Name: "赠送/签到额度", Remain: int64(q.AddOnQuota.Remaining), Used: int64(q.AddOnQuota.Used), Size: int64(q.AddOnQuota.Total)},
		},
	}
	return sum, nil
}

// planResponse mirrors GET /api/v2/user/plan (plain JSON, no envelope).
type planResponse struct {
	UserType       string          `json:"user_type"`
	PlanTierName   string          `json:"plan_tier_name"`
	IsPersonal     bool            `json:"is_personal_version"`
	IsPaid         bool            `json:"is_paid_plan"`
	IsHighestTier  bool            `json:"is_highest_tier"`
	FeatureAllowed map[string]bool `json:"feature_allowed"`
	StartDate      int64           `json:"start_date"` // ms epoch
	EndDate        int64           `json:"end_date"`   // ms epoch
}

func fetchPaymentType(sa *storedAuth) string {
	req, err := http.NewRequest(http.MethodGet, upstreamBaseCN+"/api/v2/user/plan", nil)
	if err != nil {
		return ""
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil || resp.StatusCode >= 400 {
		return ""
	}
	var p planResponse
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return ""
	}
	// Prefer plan_tier_name (e.g. "Pro Trial") over the raw user_type string.
	if p.PlanTierName != "" {
		return p.PlanTierName
	}
	return p.UserType
}

func performCheckinCall(sa *storedAuth) (map[string]any, error) {
	list, err := fetchCampaigns(sa)
	if err != nil {
		return nil, err
	}
	c, state := findCreditCampaign(list, time.Now())
	if state == "" {
		return map[string]any{
			"success":  false,
			"inactive": true,
			"reason":   "inactive",
			"message":  noCampaignMessage,
		}, nil
	}
	if state == "claimed" {
		return map[string]any{
			"success":       true,
			"skipped":       true,
			"reason":        "already",
			"message":       "今日已领取",
			"rewardCredits": float64(c.Benefit.Amount),
			"campaign_key":  c.CampaignKey,
		}, nil
	}
	res, err := claimCampaign(sa, c.CampaignID)
	if err != nil {
		return nil, err
	}
	if success, _ := res["success"].(bool); success {
		if _, ok := res["rewardCredits"]; !ok {
			res["rewardCredits"] = float64(c.Benefit.Amount)
		}
		if _, ok := res["message"]; !ok {
			res["message"] = "签到成功"
		}
		res["campaign_key"] = c.CampaignKey
	} else {
		msg, _ := res["message"].(string)
		if strings.Contains(strings.ToLower(msg), "already") || strings.Contains(msg, "已领取") {
			res["success"] = true
			res["skipped"] = true
			res["reason"] = "already"
		}
	}
	return res, nil
}

// isCreditsExhausted is the shared "耗尽" definition for panel + scheduler.
// Exhausted = we have usage signal and no remaining credits.
// Missing credits data is NOT exhausted (unknown).
func isCreditsExhausted(cr *creditsSummary) bool {
	if cr == nil {
		return false
	}
	if cr.TotalRemain > 0 {
		return false
	}
	// remain==0: exhausted only when we know there was/is a package total
	// (used>0, size>0, or packages present). Pure zero with no packages = no data.
	if cr.TotalUsed > 0 || cr.TotalSize > 0 {
		return true
	}
	return len(cr.Packages) > 0
}
