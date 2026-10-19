// recent.go keeps the last 20 completed executor calls for the dashboard.
// The ring lives in the plugin process: CPA restarts clear it, same as the
// workbuddy-cpa panel's recent list.
package main

import (
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const recentCap = 20

type recentCall struct {
	TS               int64   `json:"ts"`
	AuthID           string  `json:"auth_id,omitempty"`
	AuthUID          string  `json:"auth_uid,omitempty"`
	Nickname         string  `json:"nickname,omitempty"`
	Model            string  `json:"model"`
	UpstreamModel    string  `json:"upstream_model,omitempty"`
	Stream           bool    `json:"stream"`
	OK               bool    `json:"ok"`
	Error            string  `json:"error,omitempty"`
	DurationMS       int64   `json:"duration_ms"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Credits          float64 `json:"credits"`
	OriginalCredits  float64 `json:"original_credits"`
	Billable         bool    `json:"billable"`
	CreditsKnown     bool    `json:"credits_known"`
}

var (
	recentMu    sync.Mutex
	recentCalls []recentCall
)

func recordRecentCall(authID, authUID, nickname, model, upstreamModel string, stream bool, started time.Time, detail usage.Detail, credits qoderCredits, failed bool, errMsg string) {
	d := normalizeUsageDetail(detail)
	errText := ""
	if failed {
		if errMsg == "" {
			errMsg = "request failed"
		}
		errText = truncate(redactSecrets(errMsg), 300)
	}
	duration := int64(0)
	if !started.IsZero() {
		duration = time.Since(started).Milliseconds()
		if duration < 0 {
			duration = 0
		}
	}
	call := recentCall{
		TS:               time.Now().Unix(),
		AuthID:           authID,
		AuthUID:          authUID,
		Nickname:         nickname,
		Model:            model,
		UpstreamModel:    upstreamModel,
		Stream:           stream,
		OK:               !failed,
		Error:            errText,
		DurationMS:       duration,
		PromptTokens:     d.InputTokens,
		CompletionTokens: d.OutputTokens,
		TotalTokens:      d.TotalTokens,
		Credits:          credits.Credits,
		OriginalCredits:  credits.OriginalCredits,
		Billable:         credits.Billable,
		CreditsKnown:     credits.Known,
	}

	recentMu.Lock()
	recentCalls = append(recentCalls, call)
	if len(recentCalls) > recentCap {
		recentCalls = recentCalls[len(recentCalls)-recentCap:]
	}
	recentMu.Unlock()
}

func recentCallsNewestFirst() []recentCall {
	recentMu.Lock()
	defer recentMu.Unlock()
	out := make([]recentCall, 0, len(recentCalls))
	for i := len(recentCalls) - 1; i >= 0; i-- {
		out = append(out, recentCalls[i])
	}
	return out
}

func recentJSON() map[string]any {
	return map[string]any{
		"calls":    recentCallsNewestFirst(),
		"capacity": recentCap,
	}
}
