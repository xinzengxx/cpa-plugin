package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func resetRecentForTest() {
	recentMu.Lock()
	recentCalls = nil
	recentMu.Unlock()
}

func TestRecentRingKeepsNewestTwenty(t *testing.T) {
	resetRecentForTest()
	for i := 0; i < 25; i++ {
		recordRecentCall("idx", "uid", "nick", "qmodel", "qmodel", false, time.Now(),
			usage.Detail{InputTokens: int64(i), OutputTokens: int64(i * 2), TotalTokens: int64(i * 3)}, qoderCredits{}, false, "")
	}
	calls := recentCallsNewestFirst()
	if len(calls) != recentCap {
		t.Fatalf("want %d calls, got %d", recentCap, len(calls))
	}
	if calls[0].PromptTokens != 24 {
		t.Fatalf("newest first: want 24, got %d", calls[0].PromptTokens)
	}
	if calls[len(calls)-1].PromptTokens != 5 {
		t.Fatalf("oldest retained: want 5, got %d", calls[len(calls)-1].PromptTokens)
	}
}

func TestRecentRecordsTokensAndStatus(t *testing.T) {
	resetRecentForTest()
	recordRecentCall("idx", "uid", "nick", "deepseek-v4.1-flash", "dfmodel", true, time.Now(),
		usage.Detail{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
		qoderCredits{Credits: 0.0123, OriginalCredits: 0.0456, Billable: true, Known: true}, false, "")
	call := recentCallsNewestFirst()[0]
	if call.Model != "deepseek-v4.1-flash" || call.UpstreamModel != "dfmodel" {
		t.Fatalf("unexpected model pair: %+v", call)
	}
	if call.PromptTokens != 11 || call.CompletionTokens != 7 || call.TotalTokens != 18 {
		t.Fatalf("unexpected tokens: %+v", call)
	}
	if call.Credits != 0.0123 || call.OriginalCredits != 0.0456 || !call.Billable || !call.CreditsKnown {
		t.Fatalf("unexpected credits: %+v", call)
	}
	if !call.OK || !call.Stream || call.Error != "" {
		t.Fatalf("unexpected status: %+v", call)
	}
}

func TestRecentFailureRedactsSecrets(t *testing.T) {
	resetRecentForTest()
	recordRecentCall("idx", "uid", "nick", "dmodel", "dmodel", false, time.Now(),
		usage.Detail{}, qoderCredits{}, true, "upstream 401: Bearer test-token-not-a-real-secret")
	call := recentCallsNewestFirst()[0]
	if call.OK {
		t.Fatal("failure must be recorded as not OK")
	}
	if strings.Contains(call.Error, "test-token-not-a-real-secret") {
		t.Fatalf("secret leaked into recent error: %s", call.Error)
	}
	if !strings.Contains(call.Error, "Bearer ***") {
		t.Fatalf("bearer token was not redacted: %s", call.Error)
	}
}

func TestRecentJSONShape(t *testing.T) {
	resetRecentForTest()
	recordRecentCall("idx", "uid", "nick", "qmodel", "qmodel", false, time.Now(),
		usage.Detail{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}, qoderCredits{}, false, "")
	v := recentJSON()
	if v["capacity"] != recentCap {
		t.Fatalf("capacity: %v", v["capacity"])
	}
	calls, ok := v["calls"].([]recentCall)
	if !ok || len(calls) != 1 {
		t.Fatalf("calls shape: %#v", v["calls"])
	}
}

func TestManagementRecentRoute(t *testing.T) {
	resetRecentForTest()
	recordRecentCall("idx", "uid", "nick", "deepseek-v4-pro", "dmodel", true, time.Now(),
		usage.Detail{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		qoderCredits{Credits: 0.0612, OriginalCredits: 0.0612, Billable: true, Known: true}, false, "")
	raw, _ := json.Marshal(pluginapi.ManagementRequest{
		Method: "GET",
		Path:   "/v0/management/plugins/qoder/recent",
	})
	out, err := handleManagement(raw)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCode int    `json:"StatusCode"`
			Body       string `json:"Body"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Result.StatusCode != 200 {
		t.Fatalf("envelope: %s", string(out))
	}
	body, err := base64.StdEncoding.DecodeString(env.Result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "deepseek-v4-pro") || !strings.Contains(string(body), "\"prompt_tokens\":10") {
		t.Fatalf("recent body missing data: %s", body)
	}
	if !strings.Contains(string(body), "\"credits\":0.0612") || !strings.Contains(string(body), "\"credits_known\":true") {
		t.Fatalf("recent body missing credits: %s", body)
	}
}

func TestCreditsFromUsageMap(t *testing.T) {
	c := creditsFromUsageMap(map[string]any{
		"credits": 0.025, "original_credits": 0.063, "billable": true,
	})
	if !c.Known || c.Credits != 0.025 || c.OriginalCredits != 0.063 || !c.Billable {
		t.Fatalf("credits: %+v", c)
	}
	c = creditsFromUsageMap(map[string]any{"credit": 0})
	if !c.Known || c.Credits != 0 {
		t.Fatalf("singular credit: %+v", c)
	}
	if c = creditsFromUsageMap(map[string]any{}); c.Known {
		t.Fatalf("empty usage must be unknown: %+v", c)
	}
}

func TestCreditsFromCompletion(t *testing.T) {
	c := creditsFromCompletion([]byte(`{"usage":{"credits":0.0123,"original_credits":0.05,"billable":false}}`))
	if !c.Known || c.Credits != 0.0123 || c.OriginalCredits != 0.05 || c.Billable {
		t.Fatalf("completion credits: %+v", c)
	}
	if c := creditsFromCompletion([]byte(`not json`)); c.Known {
		t.Fatalf("invalid completion must be unknown: %+v", c)
	}
}
