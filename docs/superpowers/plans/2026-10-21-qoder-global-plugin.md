# Qoder Global（qoder.com）插件 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新建独立 CPA provider 插件 `qoder`，用国际版（qoder.com）账号把客户端可见的 `claude-opus-5-5` 透传到上游 `ultimate`（极致）模型，并保留 OAuth/PAT、签到、积分面板与用量记账。

**Architecture:** 从 `qoderwork/` fork 出 `qoder/`，删除 COSY/agent 请求重建链路；执行器改为把 OpenAI chat-completions payload 原样透传到 `api2-v2.qoder.sh/model/v1/chat/completions`（纯 Bearer、标准 SSE）。认证/签到/面板复用 qoderwork 的成熟实现，仅换国际版端点与 OAuth 参数。

**Tech Stack:** Go 1.26、CGO `-buildmode=c-shared`、CPA SDK v7（`github.com/router-for-me/CLIProxyAPI/v7 v7.2.130`）、`net/http` + `encoding/json`、httptest。

**Spec:** `docs/superpowers/specs/2026-10-21-qoder-global-plugin-design.md`

## Global Constraints

- providerName 必须是 `qoder`；auth 文件 `qoder-<uid>.json`；兼容名 `qoder.json`；顶层 `"type": "qoder"`。
- 客户端模型 ID 必须是 `claude-opus-5-5`；上游 key 必须是 `ultimate`；v1 不接受/不暴露任何其他模型。
- 端点（逐字）：`https://openapi.qoder.sh`、`https://api2-v2.qoder.sh/model/v1/chat/completions`、OAuth 站 `https://qoder.com`、client_id `e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb`（授权 URL 不带 redirect_uri，nonce 为 32 位 hex）。
- v1 禁止引入 COSY 签名依赖；`sign.go` / `cosy_session.go` / `body.go` / `encoding.go` / `image.go` 不得出现在 `qoder/`。
- 透传执行器不得改写 `messages`、`tools`、`tool_calls`、`tool_call_id`；只允许覆盖 `model`、`stream`、`stream_options`、`metadata`。
- 所有 Go 文件通过 `gofmt`、`go vet`、`go test -race ./...`；提交信息用 `feat(qoder):` / `fix(qoder):` / `docs(qoder):`。
- 未提交的 `qoderwork/` 改动不纳入本计划的任何 commit。

---

### Task 1: Fork 插件骨架、单模型映射、去 COSY

**Files:**
- Create: `qoder/`（从 `qoderwork/` 复制 26 个文件，见 Step 1）
- Create: `qoder/models_test.go`
- Modify: `qoder/go.mod`、`qoder/Makefile`、`qoder/main.go`、`qoder/models.go`（整文件替换）、`qoder/stream.go`（裁剪）、`qoder/campaign.go`、`qoder/keepalive.go`、`qoder/oauth.go`
- Delete: `qoder/body.go`、`qoder/body_test.go`、`qoder/encoding.go`、`qoder/image.go`、`qoder/image_test.go`、`qoder/sign.go`、`qoder/cosy_session.go`

**Interfaces:**
- Produces: `const clientModelID = "claude-opus-5-5"`；`const upstreamUltimate = "ultimate"`；`func cpaToUpstreamKey(string) string`；`func isAllowedUpstreamModel(string) bool`；`func globalModels() []pluginapi.ModelInfo`；`func handleModelStatic/handleModelForAuth([]byte) ([]byte, error)`；`func campaignHeaders(*http.Request, *storedAuth) error`。

- [ ] **Step 1: 复制 fork 源文件**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
mkdir -p qoder
for f in active_auth.go authfile.go billing.go cache.go campaign.go campaign_test.go checkin.go credits_handler.go host_auth.go host_bridge.go keepalive.go lifecycle.go main.go management.go models.go oauth.go panel.go panel.html policy.go recent.go recent_test.go scheduler.go stream.go usage.go usage_config.go go.mod go.sum Makefile VERSION LICENSE; do
  cp "qoderwork/$f" "qoder/$f"
done
```

- [ ] **Step 2: 删除 COSY / agent 专属文件**

```bash
cd qoder
rm body.go body_test.go encoding.go image.go image_test.go sign.go cosy_session.go models_test.go
```

- [ ] **Step 3: 批量改名与端点常量**

```bash
cd qoder
sed -i '' 's/qoderwork/qoder/g' *.go panel.html Makefile
sed -i '' 's/upstreamBaseCN/upstreamBase/g; s/gatewayBaseCN/gatewayBase/g' *.go
sed -i '' 's/qoder\.com\.cn/qoder.com/g' *.go
```

`go.mod` 第一行改为：

```go
module github.com/Sliverkiss/cpa-plugin/qoder
```

`Makefile` 的 release 目标里把 `cp README.md README_CN.md LICENSE $$out/` 改成 `cp README.md LICENSE $$out/`。

- [ ] **Step 4: 替换 main.go 常量块**

把 `const (...)` 中从 `providerName` 到 `loginTTL` 的整块替换为：

```go
const (
	providerName  = "qoder"
	authFileName  = "qoder.json"
	pluginLogoURL = "https://raw.githubusercontent.com/DGZSbot/ai-icon/refs/heads/main/QoderWork.png"

	// Qoder Global: OpenAPI for auth/billing/check-in; api2-v2 for the
	// OpenAI-compatible chat endpoint. No COSY signing on this path.
	upstreamBase = "https://openapi.qoder.sh"
	clientUA     = "Go-http-client/2.0"

	endpointJobTokenExchange = upstreamBase + "/api/v1/jobToken/exchange"
	endpointJobTokenRefresh  = upstreamBase + "/api/v1/jobToken/refresh"

	endpointUserInfo      = upstreamBase + "/api/v1/userinfo"
	endpointQuotaUsage    = upstreamBase + "/api/v2/quota/usage"
	endpointUserPlan      = upstreamBase + "/api/v2/user/plan"
	endpointCheckinStatus = upstreamBase + "/sash/api/v1/me/daily-check-in/status"
	endpointCheckinClaim  = upstreamBase + "/sash/api/v1/me/daily-check-in/claim"
	endpointProUpgrade    = upstreamBase + "/sash/api/v1/me/pro-upgrade/claim"

	endpointChat = "https://api2-v2.qoder.sh/model/v1/chat/completions"

	loginTTL = 10 * time.Minute
)
```

- [ ] **Step 5: 写失败的 models 测试**

新建 `qoder/models_test.go`：

```go
package main

import "testing"

func TestCPAToUpstreamKey(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":        "ultimate",
		"qoder/claude-opus-5-5":  "ultimate",
		"Claude-Opus-5-5":        "ultimate",
		"ultimate":               "ultimate",
	}
	for in, want := range cases {
		if got := cpaToUpstreamKey(in); got != want {
			t.Errorf("cpaToUpstreamKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownModelRejectedByGuard(t *testing.T) {
	for _, in := range []string{"sonus", "cantus", "auto", "qwen3.8-max", ""} {
		if isAllowedUpstreamModel(cpaToUpstreamKey(in)) {
			t.Errorf("%q must not pass the executor guard", in)
		}
	}
}

func TestGlobalModelsAdvertisesSingleModel(t *testing.T) {
	models := globalModels()
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	if models[0].ID != "claude-opus-5-5" {
		t.Fatalf("model id = %q, want claude-opus-5-5", models[0].ID)
	}
	if models[0].OwnedBy != "qoder" {
		t.Fatalf("owned_by = %q, want qoder", models[0].OwnedBy)
	}
}
```

- [ ] **Step 6: 运行测试确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run 'TestCPAToUpstreamKey|TestUnknownModelRejectedByGuard|TestGlobalModelsAdvertisesSingleModel'
```

Expected: 编译失败，`undefined: cpaToUpstreamKey` / `undefined: globalModels`。

- [ ] **Step 7: 整文件替换 models.go**

```go
// models.go implements the ModelProvider capability for Qoder Global.
// v1 advertises exactly one client-facing model, claude-opus-5-5, mapped to
// Qoder's "ultimate" tier. There is no dynamic model discovery: entitlement
// problems surface as upstream errors from the executor.
package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	clientModelID    = "claude-opus-5-5"
	upstreamUltimate = "ultimate"
)

func globalModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{{
		ID:                         clientModelID,
		Name:                       "Qoder Ultimate",
		ContextLength:              200000,
		MaxCompletionTokens:        8192,
		OwnedBy:                    providerName,
		SupportedGenerationMethods: []string{"chat"},
	}}
}

// cpaToUpstreamKey maps a client-facing model id to the upstream gateway key.
func cpaToUpstreamKey(cpaModel string) string {
	m := strings.ToLower(strings.TrimSpace(stripProviderPrefix(cpaModel)))
	if m == clientModelID || m == upstreamUltimate {
		return upstreamUltimate
	}
	return m
}

// isAllowedUpstreamModel is the executor-side guard. Unknown models are
// rejected instead of silently falling back to Auto upstream.
func isAllowedUpstreamModel(model string) bool {
	return model == upstreamUltimate
}

func handleModelStatic(raw []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cacheModelAliases(req.Host)
	models := filterExcludedModels(globalModels(), req.Host)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

func handleModelForAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	cacheModelAliases(req.Host)
	models := filterExcludedModels(globalModels(), req.Host)
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models})
}

// The following four helpers are copied verbatim from
// qoderwork/models.go:264-399 (cacheModelAliases, resolveUpstreamModel,
// parseModelAliasAttribute, filterExcludedModels). Keep them unchanged so
// host oauth-model-alias / oauth-excluded-models keep working.
```

然后把 `qoderwork/models.go` 中以下四个函数**原样**复制到本文件末尾：

- `cacheModelAliases(host pluginapi.HostConfigSummary)`
- `resolveUpstreamModel(model string, attributes map[string]string) string`
- `parseModelAliasAttribute(attributes map[string]string) map[string]string`
- `filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo`

（行号范围 `qoderwork/models.go:264-399`，包含 `filterExcludedModels` 里的 fresh-slice 注释与实现。）

- [ ] **Step 8: 裁剪 stream.go，移除 COSY 依赖**

在 `qoder/stream.go` 中**保留**：`streamEmit`、`streamErrorWire`、`streamEmitError`、`streamClose`、`streamHeaders`、`cleanChunkJSON`、`aggregateCompletion`、`mergeToolCallDelta`、`stripDataPrefix`、`firstNonEmpty`、`isEmptyValue`、`clientNeedsSSEFrame`。

**删除**：`pumpUpstreamStream`、`collectUpstreamStreamQoder`、`aggregateQoderSSE`、`outerStatus`、`providerErrorMessage`（Task 3 会用标准 SSE 版本重新加回前两个）。

- [ ] **Step 9: 删除 COSY 调用点**

1. 删除 `main.go` 中的 `applyCosyHeaders` 整个函数。
2. 删除 `keepalive.go:159`、`oauth.go:547`、`oauth.go:568` 的 `invalidateCosySession(...)` 调用行。

- [ ] **Step 10: 用 stub 替换两个执行器（Task 2/3 再实现）**

把 `qoder/main.go` 的 `handleExecExecute` 与 `handleExecStream` 函数体整体替换为：

```go
func handleExecExecute(raw []byte) ([]byte, error) {
	return nil, fmt.Errorf("executor_not_implemented: qoder global executor lands in Task 2")
}

func handleExecStream(raw []byte) ([]byte, error) {
	return nil, fmt.Errorf("executor_not_implemented: qoder global executor lands in Task 3")
}
```

保留 `stripProviderPrefix`、`executorStreamRequest`、`streamResponse` 不变。按 `go build` 报错删除不再使用的 import（预期涉及 `io`、`usage`）。

- [ ] **Step 11: 重写 campaignHeaders，去掉 cosySessionFor**

把 `qoder/campaign.go` 的 `campaignHeaders` 替换为下面代码，并追加三个派生函数：

```go
// campaignHeaders mirrors the Qoder desktop client's campaign request: plain
// Bearer auth plus client type/version and machine headers. A configured
// fingerprint (checkin_fingerprint) wins; otherwise a stable per-account
// identity is derived from the UID. Global accounts may see no CLAIMABLE
// campaign until the official client has claimed once.
func campaignHeaders(req *http.Request, sa *storedAuth) error {
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", campaignUserAgent)
	req.Header.Set("Cosy-ClientType", campaignClientType)
	req.Header.Set("Cosy-Version", campaignVersion)
	req.Header.Set("Cosy-MachineOS", campaignMachineOS())

	id := deriveMachineID(sa.Account.UID)
	token := deriveMachineToken(sa.Account.UID)
	machineType := deriveMachineType(sa.Account.UID)
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		req.Header.Set("Cosy-MachineHostname", host)
	}
	req.Header.Set("Cosy-MachineId", id)
	req.Header.Set("Cosy-MachineToken", token)
	req.Header.Set("Cosy-MachineType", machineType)
	req.Header.Set("Cosy-MachineCode", id)
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
```

import 增加 `crypto/md5`、`crypto/sha512`、`encoding/base64`、`encoding/hex`；`campaignMachineOS` 等常量保持不变。

- [ ] **Step 12: 编译、vet、跑测试**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go build ./...
go vet ./...
go test ./... -run 'TestCPAToUpstreamKey|TestUnknownModelRejectedByGuard|TestGlobalModelsAdvertisesSingleModel|TestCampaign|TestRecent' -count=1
```

Expected: 全部 PASS；`go build` 无输出。

- [ ] **Step 13: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/
git commit -m "feat(qoder): fork QoderWork plugin for Qoder Global and map claude-opus-5-5 to ultimate"
```

---

### Task 2: 透传 body 与同步（非流式）执行器

**Files:**
- Create: `qoder/executor.go`、`qoder/executor_test.go`
- Modify: `qoder/main.go`（`handleExecExecute`）

**Interfaces:**
- Consumes: `cpaToUpstreamKey`、`isAllowedUpstreamModel`、`clientModelID`、`upstreamUltimate`、`hostHTTPDoStream`、`newHostStreamReader`、`aggregateCompletion`、`usageDetailFromCompletion`、`creditsFromCompletion`、`publishUsage`、`recordRecentCall`、`reconcileAfterExecutorError`。
- Produces: `var upstreamChatURL string`；`func buildPassthroughBody(payload []byte, upstreamModel string) (body []byte, requestID, sessionID string, err error)`；`func buildChatRequest(sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error)`；`func buildChatRequestWithContext(ctx context.Context, sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error)`；`func executeChatOnce(sa *storedAuth, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error)`；`func completionLooksEmpty([]byte) bool`。

- [ ] **Step 1: 写失败测试**

新建 `qoder/executor_test.go`：

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildPassthroughBodyPreservesClientRequest(t *testing.T) {
	payload := []byte(`{
		"model":"claude-opus-5-5",
		"messages":[
			{"role":"system","content":"client system prompt"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"temperature":0.2,
		"top_p":0.9,
		"max_tokens":512,
		"stop":["END"]
	}`)
	body, requestID, sessionID, err := buildPassthroughBody(payload, upstreamUltimate)
	if err != nil {
		t.Fatalf("buildPassthroughBody: %v", err)
	}
	if requestID == "" || sessionID == "" {
		t.Fatal("request/session id must be generated")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if got["model"] != "ultimate" {
		t.Fatalf("model = %v, want ultimate", got["model"])
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
	if got["temperature"] != 0.2 || got["top_p"] != 0.9 {
		t.Fatalf("sampling params lost: %v / %v", got["temperature"], got["top_p"])
	}
	if got["tool_choice"] != "auto" {
		t.Fatalf("tool_choice lost: %v", got["tool_choice"])
	}
	if _, ok := got["tools"].([]any); !ok {
		t.Fatal("tools lost")
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if _, ok := msgs[1].(map[string]any)["tool_calls"].([]any); !ok {
		t.Fatal("assistant tool_calls lost")
	}
	if msgs[2].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatal("tool_call_id lost")
	}
	meta := got["metadata"].(map[string]any)["context"].(map[string]any)
	if meta["client_type"] != "qodercli" || meta["task_id"] != "common" {
		t.Fatalf("metadata = %v", meta)
	}
}

func TestExecuteChatOnceAggregatesSSE(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("X-Request-ID") == "" || r.Header.Get("X-Session-ID") == "" {
			t.Error("missing request/session header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"ultimate\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5,\"credits\":0.5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	completion, status, err := executeChatOnce(sa, clientModelID, upstreamUltimate, []byte(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("executeChatOnce: %v (status %d)", err, status)
	}
	if gotAuth != "Bearer dt-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	var out map[string]any
	if err := json.Unmarshal(completion, &out); err != nil {
		t.Fatalf("parse completion: %v", err)
	}
	msg := out["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hi" {
		t.Fatalf("content = %v", msg["content"])
	}
	if creditsFromCompletion(completion).Credits != 0.5 {
		t.Fatal("credits not carried through")
	}
}

func TestExecuteChatOnceSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"no ultimate entitlement"}}`))
	}))
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	_, status, err := executeChatOnce(sa, clientModelID, upstreamUltimate, []byte(`{"messages":[]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if !strings.Contains(err.Error(), "no ultimate entitlement") {
		t.Fatalf("error loses upstream body: %v", err)
	}
}

func TestCompletionLooksEmpty(t *testing.T) {
	empty := []byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`)
	if !completionLooksEmpty(empty) {
		t.Fatal("empty completion not detected")
	}
	toolOnly := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"x"}]}}]}`)
	if completionLooksEmpty(toolOnly) {
		t.Fatal("tool-only completion must not be treated as empty")
	}
	withUsage := []byte(`{"choices":[{"message":{"role":"assistant","content":""}}],"usage":{"total_tokens":7}}`)
	if completionLooksEmpty(withUsage) {
		t.Fatal("usage-bearing completion must not be treated as empty")
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run 'TestBuildPassthroughBody|TestExecuteChatOnce|TestCompletionLooksEmpty'
```

Expected: 编译失败，`undefined: buildPassthroughBody`。

- [ ] **Step 3: 实现 executor.go**

```go
// executor.go implements Qoder Global's pass-through executor: the client's
// OpenAI chat-completions payload is forwarded nearly verbatim to
// api2-v2.qoder.sh/model/v1/chat/completions (plain Bearer, standard OpenAI
// SSE), so tools/tool_calls/tool results survive untouched.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// upstreamChatURL is a var so tests can point it at httptest.
var upstreamChatURL = "https://api2-v2.qoder.sh/model/v1/chat/completions"

// chatUserAgent mirrors the Qoder CLI client. Change only with live evidence.
const chatUserAgent = "qoder/1.1.64"

func buildPassthroughBody(payload []byte, upstreamModel string) ([]byte, string, string, error) {
	if len(payload) == 0 {
		return nil, "", "", fmt.Errorf("payload empty")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, "", "", fmt.Errorf("payload parse: %w", err)
	}
	body["model"] = json.RawMessage(strconv.Quote(upstreamModel))
	body["stream"] = json.RawMessage("true")
	body["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	requestID := uuid.NewString()
	sessionID := uuid.NewString()
	meta, err := json.Marshal(map[string]any{"context": map[string]any{
		"request_id": requestID, "request_set_id": requestID,
		"session_id": sessionID, "task_id": "common", "client_type": "qodercli",
	}})
	if err != nil {
		return nil, "", "", err
	}
	body["metadata"] = meta
	out, err := json.Marshal(body)
	if err != nil {
		return nil, "", "", err
	}
	return out, requestID, sessionID, nil
}

func buildChatRequestWithContext(ctx context.Context, sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamChatURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sa.Auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", chatUserAgent)
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Session-ID", sessionID)
	return req, nil
}

func buildChatRequest(sa *storedAuth, body []byte, requestID, sessionID string) (*http.Request, error) {
	return buildChatRequestWithContext(context.Background(), sa, body, requestID, sessionID)
}

// executeChatOnce performs one upstream attempt. Non-stream client requests
// also use stream=true upstream and fold the SSE, matching the Qoder CLI.
func executeChatOnce(sa *storedAuth, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error) {
	body, requestID, sessionID, err := buildPassthroughBody(payload, upstreamModel)
	if err != nil {
		return nil, 0, err
	}
	req, err := buildChatRequest(sa, body, requestID, sessionID)
	if err != nil {
		return nil, 0, err
	}
	stream, status, _, err := hostHTTPDoStream(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http_error: %w", err)
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if status >= 400 {
		raw, _ := io.ReadAll(reader)
		return nil, status, fmt.Errorf("upstream %d: %s", status, truncateRedacted(string(raw), 300))
	}
	completion, err := aggregateCompletion(reader, requestedModel)
	if err != nil {
		return nil, 0, err
	}
	if completionLooksEmpty(completion) {
		return nil, 0, fmt.Errorf("upstream returned an empty completion")
	}
	return completion, status, nil
}

func completionLooksEmpty(completion []byte) bool {
	var c struct {
		Choices []struct {
			Message map[string]any `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(completion, &c) != nil {
		return true
	}
	if v, ok := c.Usage["total_tokens"].(float64); ok && v > 0 {
		return false
	}
	for _, ch := range c.Choices {
		if s, _ := ch.Message["content"].(string); strings.TrimSpace(s) != "" {
			return false
		}
		if tc, ok := ch.Message["tool_calls"].([]any); ok && len(tc) > 0 {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 替换 handleExecExecute**

```go
func handleExecExecute(raw []byte) ([]byte, error) {
	var req pluginapi.ExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	upstreamModel := cpaToUpstreamKey(req.Model)
	started := time.Now()
	authUID := sa.Account.UID
	if !isAllowedUpstreamModel(upstreamModel) {
		msg := fmt.Sprintf("model_not_supported: qoder serves %s only (got %q)", clientModelID, req.Model)
		publishUsage(req.Model, upstreamModel, authUID, started, usage.Detail{}, true, 0, msg)
		recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, false, started, usage.Detail{}, qoderCredits{}, true, msg)
		return nil, fmt.Errorf("%s", msg)
	}
	completion, status, err := executeChatOnce(sa, req.Model, upstreamModel, req.Payload)
	if err != nil {
		publishUsage(req.Model, upstreamModel, authUID, started, usage.Detail{}, true, status, err.Error())
		recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, false, started, usage.Detail{}, qoderCredits{}, true, err.Error())
		reconcileAfterExecutorError(req.AuthID, status, err.Error())
		return nil, err
	}
	detail := usageDetailFromCompletion(completion)
	publishUsage(req.Model, upstreamModel, authUID, started, detail, false, 0, "")
	recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, false, started, detail, creditsFromCompletion(completion), false, "")
	invalidateAccountCredits(req.AuthID, authUID)
	return okEnvelope(pluginapi.ExecutorResponse{Payload: completion})
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go test ./... -run 'TestBuildPassthroughBody|TestExecuteChatOnce|TestCompletionLooksEmpty' -count=1
go vet ./...
```

Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/executor.go qoder/executor_test.go qoder/main.go
git commit -m "feat(qoder): add pass-through non-stream executor for claude-opus-5-5"
```

---

### Task 3: 流式执行器（标准 OpenAI SSE 透传）

**Files:**
- Modify: `qoder/stream.go`（加回 pump/collect，删除旧 envelope 逻辑）、`qoder/main.go`（`handleExecStream`）
- Create: `qoder/stream_test.go`

**Interfaces:**
- Consumes: Task 2 的 `buildPassthroughBody`、`buildChatRequest`、`buildChatRequestWithContext`。
- Produces: `func collectUpstreamStream(sa *storedAuth, requestedModel string, body []byte, requestID, sessionID string, sseFramed bool, collector *sseUsageCollector) ([]pluginapi.ExecutorStreamChunk, int, error)`；`func pumpUpstreamStream(httpReq *http.Request, cancel context.CancelFunc, streamID string, sseFramed bool, requestedModel, upstreamModel, authUID, nickname string, started time.Time, authID string)`；`func streamErrorMessage(string) string`。

- [ ] **Step 1: 写失败测试**

新建 `qoder/stream_test.go`：

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"SF\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15,\"credits\":1.5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestCollectUpstreamStreamPreservesToolCallsAndUsage(t *testing.T) {
	srv := sseServer(t)
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, err := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"weather?"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`), upstreamUltimate)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	collector := &sseUsageCollector{}
	chunks, status, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, false, collector)
	if err != nil {
		t.Fatalf("collect: %v (status %d)", err, status)
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want 4", len(chunks))
	}
	joined := ""
	for _, c := range chunks {
		joined += string(c.Payload)
	}
	if strings.Contains(joined, "[DONE]") {
		t.Fatal("[DONE] must not be forwarded")
	}
	if !strings.Contains(joined, `"get_weather"`) {
		t.Fatalf("tool call fragments lost: %s", joined)
	}
	if !strings.Contains(joined, `"city`) {
		t.Fatalf("tool call arguments lost: %s", joined)
	}
	if !collector.hasOutput {
		t.Fatal("collector saw no output")
	}
	if collector.detail().TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", collector.detail().TotalTokens)
	}
	if collector.credits().Credits != 1.5 {
		t.Fatalf("credits = %v, want 1.5", collector.credits().Credits)
	}
}

func TestCollectUpstreamStreamSSEFraming(t *testing.T) {
	srv := sseServer(t)
	defer srv.Close()
	old := upstreamChatURL
	upstreamChatURL = srv.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, _ := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), upstreamUltimate)
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	chunks, _, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, true, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, c := range chunks {
		if !strings.HasPrefix(string(c.Payload), "data: ") {
			t.Fatalf("unframed chunk: %s", c.Payload)
		}
	}
}

func TestStreamErrorMessage(t *testing.T) {
	if got := streamErrorMessage(`{"choices":[{"delta":{"content":"ok"}}]}`); got != "" {
		t.Fatalf("normal chunk treated as error: %q", got)
	}
	if got := streamErrorMessage(`{"error":{"message":"no entitlement"}}`); got != "no entitlement" {
		t.Fatalf("error message = %q", got)
	}
}

func TestCollectUpstreamStreamEmptyGuard(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer empty.Close()
	old := upstreamChatURL
	upstreamChatURL = empty.URL
	defer func() { upstreamChatURL = old }()

	body, rid, sid, _ := buildPassthroughBody([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), upstreamUltimate)
	sa := &storedAuth{Auth: storedTokens{AccessToken: "dt-test"}}
	if _, _, err := collectUpstreamStream(sa, clientModelID, body, rid, sid, false, &sseUsageCollector{}); err == nil {
		t.Fatal("empty upstream stream must return an error, not an empty 200")
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run 'TestCollectUpstreamStream|TestStreamErrorMessage'
```

Expected: 编译失败，`undefined: collectUpstreamStream`。

- [ ] **Step 3: 在 stream.go 加回标准 SSE 实现**

追加到 `qoder/stream.go`（import 需要 `context`、`time`、`bufio`、`io`、`net/http`）：

```go
// streamErrorMessage extracts an upstream error carried inside a data line.
// Normal chunks always have "choices" or "usage"; only error objects are
// treated as failures.
func streamErrorMessage(line string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return ""
	}
	if _, ok := obj["choices"]; ok {
		return ""
	}
	if _, ok := obj["usage"]; ok {
		return ""
	}
	if e, ok := obj["error"].(map[string]any); ok {
		if msg, _ := e["message"].(string); strings.TrimSpace(msg) != "" {
			return strings.TrimSpace(msg)
		}
	}
	if msg, ok := obj["message"].(string); ok && strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	return ""
}

func pumpUpstreamStream(ctx context.Context, cancel context.CancelFunc, sa *storedAuth, body []byte, requestID, sessionID, streamID string, sseFramed bool, requestedModel, upstreamModel, authUID, nickname string, started time.Time, authID string) {
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			streamClose(streamID)
		}
	}
	defer closeOnce()
	if cancel != nil {
		defer cancel()
	}
	httpReq, err := buildChatRequestWithContext(ctx, sa, body, requestID, sessionID)
	if err != nil {
		streamEmitError(streamID, err.Error())
		return
	}
	stream, status, _, err := hostHTTPDoStream(httpReq)
	if err != nil {
		publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, 0, err.Error())
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, err.Error())
		streamEmitError(streamID, "http_error: "+err.Error())
		return
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if status >= 400 {
		raw, _ := io.ReadAll(reader)
		detail := truncateRedacted(string(raw), 300)
		publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, status, detail)
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, detail)
		if authUID != "" {
			go reconcileByUID(authUID, status, string(raw))
		}
		streamEmitError(streamID, fmt.Sprintf("upstream %d: %s", status, truncateRedacted(string(raw), 200)))
		return
	}
	collector := &sseUsageCollector{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		if msg := streamErrorMessage(payload); msg != "" {
			detail := truncateRedacted(msg, 300)
			publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, 0, detail)
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, "upstream error: "+detail)
			streamEmitError(streamID, "upstream error: "+detail)
			return
		}
		collector.feed(payload)
		cleaned := cleanChunkJSON(payload)
		if cleaned == "" {
			continue
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		if err := streamEmit(streamID, []byte(cleaned)); err != nil {
			publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, "stream_emit: "+err.Error())
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, "stream_emit: "+err.Error())
			return
		}
	}
	if err := scanner.Err(); err != nil {
		publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, err.Error())
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, err.Error())
		streamEmitError(streamID, "upstream stream read error: "+err.Error())
		return
	}
	if !collector.hasOutput && collector.detail().TotalTokens == 0 {
		const msg = "upstream returned an empty completion"
		publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), true, 0, msg)
		recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), true, msg)
		streamEmitError(streamID, msg)
		return
	}
	publishUsage(requestedModel, upstreamModel, authUID, started, collector.detail(), false, 0, "")
	recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, collector.detail(), collector.credits(), false, "")
	invalidateAccountCredits(authID, authUID)
}

func collectUpstreamStream(sa *storedAuth, requestedModel string, body []byte, requestID, sessionID string, sseFramed bool, collector *sseUsageCollector) ([]pluginapi.ExecutorStreamChunk, int, error) {
	req, err := buildChatRequest(sa, body, requestID, sessionID)
	if err != nil {
		return nil, 0, err
	}
	stream, status, _, err := hostHTTPDoStream(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http_error: %w", err)
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
	if status >= 400 {
		raw, _ := io.ReadAll(reader)
		return nil, status, fmt.Errorf("upstream %d: %s", status, truncateRedacted(string(raw), 200))
	}
	chunks := make([]pluginapi.ExecutorStreamChunk, 0, 64)
	hasOutput := false
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if msg := streamErrorMessage(payload); msg != "" {
			return chunks, 0, fmt.Errorf("upstream error: %s", truncateRedacted(msg, 200))
		}
		if collector != nil {
			collector.feed(payload)
			if collector.hasOutput {
				hasOutput = true
			}
		} else if chunkHasOutput(payload) {
			hasOutput = true
		}
		cleaned := cleanChunkJSON(payload)
		if cleaned == "" {
			continue
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: json.RawMessage(cleaned)})
	}
	if err := scanner.Err(); err != nil {
		return chunks, 0, fmt.Errorf("upstream stream read error: %w", err)
	}
	if !hasOutput {
		return chunks, 0, fmt.Errorf("upstream returned an empty completion")
	}
	return chunks, 0, nil
}

func chunkHasOutput(payload string) bool {
	var chunk map[string]any
	if json.Unmarshal([]byte(payload), &chunk) != nil {
		return false
	}
	choices, _ := chunk["choices"].([]any)
	for _, raw := range choices {
		choice, _ := raw.(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if s, _ := delta["content"].(string); s != "" {
			return true
		}
		if tc, _ := delta["tool_calls"].([]any); len(tc) > 0 {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 替换 handleExecStream**

```go
func handleExecStream(raw []byte) ([]byte, error) {
	var req executorStreamRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	sa, err := parseStored(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	upstreamModel := cpaToUpstreamKey(req.Model)
	started := time.Now()
	authUID := sa.Account.UID
	sseFramed := clientNeedsSSEFrame(req.Metadata)
	if !isAllowedUpstreamModel(upstreamModel) {
		msg := fmt.Sprintf("model_not_supported: qoder serves %s only (got %q)", clientModelID, req.Model)
		publishUsage(req.Model, upstreamModel, authUID, started, usage.Detail{}, true, 0, msg)
		recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, msg)
		if req.StreamID != "" {
			streamEmitError(req.StreamID, msg)
			streamClose(req.StreamID)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	payload := req.Payload
	if len(payload) == 0 {
		payload = req.OriginalRequest
	}
	body, requestID, sessionID, err := buildPassthroughBody(payload, upstreamModel)
	if err != nil {
		publishUsage(req.Model, upstreamModel, authUID, started, usage.Detail{}, true, 0, err.Error())
		return nil, err
	}
	headers := streamHeaders()
	if req.StreamID == "" {
		collector := &sseUsageCollector{}
		chunks, status, errCollect := collectUpstreamStream(sa, req.Model, body, requestID, sessionID, sseFramed, collector)
		if errCollect != nil {
			publishUsage(req.Model, upstreamModel, authUID, started, usage.Detail{}, true, status, errCollect.Error())
			recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, true, started, collector.detail(), collector.credits(), true, errCollect.Error())
			return nil, errCollect
		}
		publishUsage(req.Model, upstreamModel, authUID, started, collector.detail(), false, 0, "")
		recordRecentCall(req.AuthID, authUID, sa.Account.Nickname, req.Model, upstreamModel, true, started, collector.detail(), collector.credits(), false, "")
		invalidateAccountCredits(req.AuthID, authUID)
		return okEnvelope(streamResponse{Headers: headers, Chunks: chunks})
	}
	ctx, cancel := context.WithCancel(context.Background())
	go pumpUpstreamStream(ctx, cancel, sa, body, requestID, sessionID, req.StreamID, sseFramed, req.Model, upstreamModel, authUID, sa.Account.Nickname, started, req.AuthID)
	return okEnvelope(streamResponse{Headers: headers})
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go test ./... -run 'TestCollectUpstreamStream|TestStreamErrorMessage' -count=1
go test ./... -count=1
go vet ./...
```

Expected: 全部 PASS。

- [ ] **Step 6: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/stream.go qoder/stream_test.go qoder/main.go
git commit -m "feat(qoder): stream standard OpenAI SSE chunks with tool_calls preserved"
```

---

### Task 4: 401/403 刷新重试

**Files:**
- Modify: `qoder/executor.go`、`qoder/main.go`（`handleExecExecute` 调用重试版）、`qoder/stream.go`（同步路径用重试版）
- Create: `qoder/retry_test.go`

**Interfaces:**
- Consumes: `refreshDeviceToken`、`refreshJobToken`、`exchangePATForJobToken`、`firstNonEmpty`。
- Produces: `var refreshForRetry func(*storedAuth) error`；`func executeChatWithRetry(sa *storedAuth, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error)`。

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"net/http"
	"net/http/httptest"
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
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run TestExecuteChatWithRetryRefreshesOnce
```

Expected: `undefined: executeChatWithRetry`。

- [ ] **Step 3: 实现刷新与重试**

追加到 `qoder/executor.go`：

```go
// refreshForRetry is injectable so tests do not hit the network.
var refreshForRetry = refreshStoredTokens

func refreshStoredTokens(sa *storedAuth) error {
	rt := strings.TrimSpace(sa.Auth.RefreshToken)
	switch {
	case strings.HasPrefix(rt, "drt-"):
		tok, err := refreshDeviceToken(rt)
		if err != nil {
			return err
		}
		sa.Auth.AccessToken = tok.accessToken()
		sa.Auth.RefreshToken = firstNonEmpty(tok.RefreshToken, rt)
		return nil
	case strings.HasPrefix(rt, "jrt-"):
		tok, err := refreshJobToken(rt)
		if err != nil {
			return err
		}
		sa.Auth.AccessToken = tok.Token
		sa.Auth.RefreshToken = firstNonEmpty(tok.RefreshToken, rt)
		return nil
	case strings.HasPrefix(strings.TrimSpace(sa.Auth.PersonalToken), "pt-"):
		tok, err := exchangePATForJobToken(sa.Auth.PersonalToken)
		if err != nil {
			return err
		}
		sa.Auth.AccessToken = tok.Token
		sa.Auth.RefreshToken = tok.RefreshToken
		return nil
	}
	return fmt.Errorf("no refresh credential")
}

func executeChatWithRetry(sa *storedAuth, requestedModel, upstreamModel string, payload []byte) ([]byte, int, error) {
	completion, status, err := executeChatOnce(sa, requestedModel, upstreamModel, payload)
	if err == nil || (status != http.StatusUnauthorized && status != http.StatusForbidden) {
		return completion, status, err
	}
	if rerr := refreshForRetry(sa); rerr != nil {
		return nil, status, fmt.Errorf("%w (refresh failed: %v)", err, rerr)
	}
	return executeChatOnce(sa, requestedModel, upstreamModel, payload)
}
```

- [ ] **Step 4: 接入同步与流式路径**

1. `handleExecExecute` 中把 `executeChatOnce(...)` 改为 `executeChatWithRetry(...)`。
2. `collectUpstreamStream` 改为：先 `executeChatRequestOnce`（把 Task 3 中 collect 的函数体拆成一次尝试）；收到 401/403 时调用 `refreshForRetry(sa)` 后重试一次。为保证可测试，新增：

```go
func collectUpstreamStream(sa *storedAuth, requestedModel string, body []byte, requestID, sessionID string, sseFramed bool, collector *sseUsageCollector) ([]pluginapi.ExecutorStreamChunk, int, error) {
	chunks, status, err := collectUpstreamStreamOnce(sa, requestedModel, body, requestID, sessionID, sseFramed, collector)
	if err != nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		if rerr := refreshForRetry(sa); rerr == nil {
			return collectUpstreamStreamOnce(sa, requestedModel, body, requestID, sessionID, sseFramed, collector)
		}
	}
	return chunks, status, err
}
```

把 Task 3 的 `collectUpstreamStream` 函数体原样改名为 `collectUpstreamStreamOnce`。

3. 流式路径：把 `pumpUpstreamStream` 开头到 `defer stream.Close()` 的代码替换为下面带一次重试的循环：

```go
	var stream *hostHTTPStream
	var status int
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		httpReq, rerr := buildChatRequestWithContext(ctx, sa, body, requestID, sessionID)
		if rerr != nil {
			streamEmitError(streamID, rerr.Error())
			return
		}
		stream, status, _, err = hostHTTPDoStream(httpReq)
		if err != nil {
			publishUsage(requestedModel, upstreamModel, authUID, started, usage.Detail{}, true, 0, err.Error())
			recordRecentCall(authID, authUID, nickname, requestedModel, upstreamModel, true, started, usage.Detail{}, qoderCredits{}, true, err.Error())
			streamEmitError(streamID, "http_error: "+err.Error())
			return
		}
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			break
		}
		stream.Close()
		if attempt == 0 && refreshForRetry(sa) == nil {
			continue
		}
		break
	}
	defer stream.Close()
	reader := newHostStreamReader(stream)
```

同时删除 Task 3 里原有的 `httpReq, err := ...` 与 `stream, status, _, err := hostHTTPDoStream(httpReq)` 两段，避免变量重复声明。

- [ ] **Step 5: 运行测试**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go test ./... -run TestExecuteChatWithRetryRefreshesOnce -count=1
go test ./... -count=1
go vet ./...
```

Expected: PASS。

- [ ] **Step 6: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/executor.go qoder/executor_test.go qoder/retry_test.go qoder/main.go qoder/stream.go
git commit -m "feat(qoder): refresh credentials once on 401/403 and retry"
```

---

### Task 5: 国际版 OAuth 参数

**Files:**
- Modify: `qoder/oauth.go`
- Create: `qoder/oauth_test.go`

**Interfaces:**
- Produces: `const qoderWebsite = "https://qoder.com"`；`const qoderClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"`；`handleStartLogin` 生成的 URL 不含 `redirect_uri`、nonce 为 32 hex。

- [ ] **Step 1: 写失败测试**

```go
package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"testing"
)

func TestStartLoginGlobalURL(t *testing.T) {
	raw, err := handleStartLogin([]byte(`{}`))
	if err != nil {
		t.Fatalf("handleStartLogin: %v", err)
	}
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("envelope: %v / %s", err, raw)
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("result: %v", err)
	}
	u, err := url.Parse(resp.URL)
	if err != nil {
		t.Fatalf("url parse: %v", err)
	}
	if u.Host != "qoder.com" {
		t.Fatalf("host = %q", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != qoderClientID {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "" {
		t.Fatalf("redirect_uri must be absent, got %q", q.Get("redirect_uri"))
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(q.Get("nonce")) {
		t.Fatalf("nonce = %q, want 32 hex", q.Get("nonce"))
	}
	if q.Get("challenge_method") != "S256" || q.Get("challenge") == "" {
		t.Fatalf("pkce params missing: %v", q)
	}
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run TestStartLoginGlobalURL
```

Expected: FAIL（host 是 qoder.com.cn 或 URL 带 redirect_uri、nonce 带横线）。

- [ ] **Step 3: 修改 oauth.go**

把常量块替换为：

```go
const (
	qoderWebsite  = "https://qoder.com"
	qoderClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"
)
```

`handleStartLogin` 内改为：

```go
	verifier, challenge := makePKCE()
	nonce := strings.ReplaceAll(uuid.NewString(), "-", "")
	machineID := uuid.NewString()

	q := url.Values{}
	q.Set("challenge", challenge)
	q.Set("challenge_method", "S256")
	q.Set("nonce", nonce)
	q.Set("machine_id", machineID)
	q.Set("client_id", qoderClientID)
	authURL := qoderWebsite + "/device/selectAccounts?" + q.Encode()
```

`Metadata.prompt` 改为：

```go
"prompt": "在打开的页面中登录并授权 Qoder（国际版）。完成后此窗口会自动关闭。",
```

并确认 `buildStoredAuthFromDeviceToken` 写入的 `Domain` 是 `qoder.com`（Task 1 的 sed 已覆盖）。

- [ ] **Step 4: 运行测试**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go test ./... -run TestStartLoginGlobalURL -count=1
go test ./... -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/oauth.go qoder/oauth_test.go
git commit -m "feat(qoder): use Qoder Global device OAuth client parameters"
```

---

### Task 6: 签到仅走 campaign + 可选指纹配置

**Files:**
- Modify: `qoder/billing.go`、`qoder/campaign.go`、`qoder/usage_config.go`、`qoder/main.go`（ConfigFields + 删除 legacy 端点常量）
- Create: `qoder/fingerprint_test.go`

**Interfaces:**
- Produces: `type checkinFingerprint struct`；`func configuredFingerprint() *checkinFingerprint`；`checkin_fingerprint` 配置字段；`campaignHeaders` 优先使用配置指纹。
- Consumes: Task 1 的 `deriveMachineID/Token/Type`。

- [ ] **Step 1: 写失败测试**

`qoder/fingerprint_test.go`：

```go
package main

import (
	"net/http"
	"testing"
)

func TestCampaignHeadersUsesConfiguredFingerprint(t *testing.T) {
	setFingerprintForTest(`{"machine_id":"mid","machine_token":"mtok","machine_type":"mtype","machine_os":"mac"}`)
	defer setFingerprintForTest("")

	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := campaignHeaders(req, &storedAuth{Account: storedAccount{UID: "u1"}}); err != nil {
		t.Fatalf("campaignHeaders: %v", err)
	}
	if req.Header.Get("Cosy-MachineToken") != "mtok" || req.Header.Get("Cosy-MachineType") != "mtype" {
		t.Fatalf("fingerprint not applied: %v", req.Header)
	}
	if req.Header.Get("Cosy-MachineOS") != "mac" {
		t.Fatalf("machine os = %q", req.Header.Get("Cosy-MachineOS"))
	}
}

func TestCampaignHeadersDerivesStableIdentity(t *testing.T) {
	setFingerprintForTest("")
	sa := &storedAuth{Account: storedAccount{UID: "u1"}}
	first, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	second, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	_ = campaignHeaders(first, sa)
	_ = campaignHeaders(second, sa)
	if first.Header.Get("Cosy-MachineToken") == "" || first.Header.Get("Cosy-MachineType") == "" {
		t.Fatal("derived identity missing")
	}
	if first.Header.Get("Cosy-MachineToken") != second.Header.Get("Cosy-MachineToken") {
		t.Fatal("derived identity must be stable per UID")
	}
}

func setFingerprintForTest(raw string) {
	checkinFingerprintMu.Lock()
	checkinFingerprintJSON = raw
	checkinFingerprintMu.Unlock()
}
```

- [ ] **Step 2: 运行确认失败**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
go test ./... -run TestCampaignHeaders
```

Expected: 编译失败，`undefined: checkinFingerprintMu`。

- [ ] **Step 3: 增加指纹配置解析**

在 `qoder/usage_config.go` 的 var 块加：

```go
	// checkin_fingerprint: optional JSON with real device identity
	// (machine_id/machine_token/machine_type). Global campaigns may withhold
	// credits until the official client has claimed once unless this is set.
	checkinFingerprintJSON = ""
	checkinFingerprintMu   sync.RWMutex
```

在 `configure()` 的 YAML 行解析循环中加：

```go
				if strings.HasPrefix(line, "checkin_fingerprint:") {
					v := strings.TrimSpace(strings.TrimPrefix(line, "checkin_fingerprint:"))
					nextFingerprint = strings.Trim(v, "\"'")
				}
```

在函数末尾把局部变量写回（与现有 `nextCheckinAuto` 等同一个赋值区）：

```go
	checkinFingerprintMu.Lock()
	checkinFingerprintJSON = nextFingerprint
	checkinFingerprintMu.Unlock()
```

在 `configure()` 开头与 `nextMgmtKey` 并列声明：

```go
	nextFingerprint := ""
```

在文件末尾追加：

```go
type checkinFingerprint struct {
	MachineID    string `json:"machine_id"`
	MachineToken string `json:"machine_token"`
	MachineType  string `json:"machine_type"`
	MachineCode  string `json:"machine_code"`
	Hostname     string `json:"hostname"`
	MachineOS    string `json:"machine_os"`
}

func configuredFingerprint() *checkinFingerprint {
	checkinFingerprintMu.RLock()
	raw := checkinFingerprintJSON
	checkinFingerprintMu.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var fp checkinFingerprint
	if json.Unmarshal([]byte(raw), &fp) != nil {
		return nil
	}
	if fp.MachineToken == "" || fp.MachineType == "" {
		return nil
	}
	return &fp
}
```

- [ ] **Step 4: campaignHeaders 优先使用配置指纹**

在 Task 1 的 `campaignHeaders` 中，把派生头赋值替换为：

```go
	if fp := configuredFingerprint(); fp != nil {
		req.Header.Set("Cosy-MachineId", fp.MachineID)
		req.Header.Set("Cosy-MachineToken", fp.MachineToken)
		req.Header.Set("Cosy-MachineType", fp.MachineType)
		if fp.MachineCode != "" {
			req.Header.Set("Cosy-MachineCode", fp.MachineCode)
		}
		if fp.Hostname != "" {
			req.Header.Set("Cosy-MachineHostname", fp.Hostname)
		}
		if fp.MachineOS != "" {
			req.Header.Set("Cosy-MachineOS", fp.MachineOS)
		}
	} else {
		id := deriveMachineID(sa.Account.UID)
		req.Header.Set("Cosy-MachineId", id)
		req.Header.Set("Cosy-MachineToken", deriveMachineToken(sa.Account.UID))
		req.Header.Set("Cosy-MachineType", deriveMachineType(sa.Account.UID))
		req.Header.Set("Cosy-MachineCode", id)
	}
```

- [ ] **Step 5: 签到只走 campaign**

1. `billing.go`：删除 `fetchLegacyCheckinStatus`，把 `fetchCheckinStatus` 改成：

```go
func fetchCheckinStatus(sa *storedAuth) (*checkinSummary, error) {
	return fetchCampaignCheckinStatus(sa)
}
```

2. 删除 `main.go` 中的 `endpointCheckinStatus`、`endpointCheckinClaim` 常量。
3. `billing.go` 的 `performCheckinCall` 中，无活动返回的 `message` 改为：

```go
"message": "当前账号无可用签到活动（国际版新号请先在官方 Qoder 客户端领取一次）",
```

- [ ] **Step 6: 注册配置字段**

在 `main.go` 的 `ConfigFields` 中，`checkin_auto` 之后加：

```go
{Name: "checkin_fingerprint", Type: pluginapi.ConfigFieldTypeString, Description: "Optional JSON fingerprint for Global check-in (machine_id/machine_token/machine_type). Fresh Global accounts need the official client to claim once unless this is set."},
```

- [ ] **Step 7: 运行测试**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
gofmt -w .
go test ./... -run 'TestCampaignHeaders|TestCampaign' -count=1
go test ./... -count=1
go vet ./...
```

Expected: PASS。

- [ ] **Step 8: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/billing.go qoder/campaign.go qoder/usage_config.go qoder/main.go qoder/fingerprint_test.go
git commit -m "feat(qoder): campaign-only check-in with optional device fingerprint"
```

---

### Task 7: 面板、README、registry

**Files:**
- Modify: `qoder/panel.html`（品牌文案）、`qoder/main.go`（Author/描述）、`registry.json`
- Create: `qoder/README.md`

- [ ] **Step 1: 面板品牌化**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
sed -i '' 's/QoderWork/Qoder Global/g' panel.html
grep -n "Qoder Global" panel.html | head
```

如果页面标题仍是 `Qoder`，把 `<title>` 内容改为 `Qoder Global`。

- [ ] **Step 2: 更新注册元数据**

`main.go` 的 `Metadata.Author` 改为 `Sliverkiss`；`GitHubRepository` 保持不变；`ConfigFields` 中 `checkin_auto` 描述里的 "CN accounts" 改为 "Global accounts"。

- [ ] **Step 3: 写 README**

新建 `qoder/README.md`：

```markdown
# Qoder Global CPA 插件

CLIProxyAPI 的 Qoder 国际版（qoder.com）Provider 插件：OAuth/PAT 双登录、OpenAI 兼容透传、单模型 `claude-opus-5-5`、每日签到、积分面板、token 保活。

## 模型

| 客户端调用名 | 上游 key | 说明 |
|---|---|---|
| `claude-opus-5-5` | `ultimate` | Qoder 国际版“极致”模型；上游真实来源以官方为准 |

v1 只暴露这一个模型；其他模型请求会返回 `model_not_supported`，不会静默回退到 Auto。

## 安装

```bash
cd qoder
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-X main.version=0.1.0" -o qoder.dylib .
cp qoder.dylib ~/.cli-proxy-api/plugins/
```

```yaml
plugins:
  configs:
    qoder:
      enabled: true
      # checkin_auto: true
      # token_keepalive: true
      # checkin_fingerprint: '{"machine_id":"...","machine_token":"...","machine_type":"..."}'
```

## 登录

- OAuth：CPA 管理面板 → Auth → Qoder 登录卡片 → 浏览器授权
- PAT：插件面板右上角「导入凭证」→ 粘贴 `pt-` 开头的 Personal Access Token

## 签到

面板支持单账号/全部签到，自动签到 10:00 / 21:00。国际版新号可能因设备指纹限制看不到活动：请先在官方 Qoder 客户端领取一次，或在配置中提供 `checkin_fingerprint`。

## 透传

执行器把 OpenAI chat-completions 请求原样转发到 `https://api2-v2.qoder.sh/model/v1/chat/completions`，保留 `tools`、`tool_calls`、`tool_call_id`，可直接用于 ZCode / Claude Code 的工具循环。
```

- [ ] **Step 4: 注册到 registry.json**

在 `plugins` 数组末尾追加：

```json
    {
      "id": "qoder",
      "name": "Qoder Global",
      "description": "Qoder Global (qoder.com) provider for CLIProxyAPI: OAuth/PAT login, OpenAI-compatible pass-through executor exposing claude-opus-5-5 (upstream ultimate), campaign check-in with optional fingerprint, credits panel, token keepalive.",
      "author": "Sliverkiss",
      "version": "0.1.0",
      "repository": "https://github.com/Sliverkiss/cpa-plugin",
      "logo": "https://raw.githubusercontent.com/DGZSbot/ai-icon/refs/heads/main/QoderWork.png",
      "homepage": "https://github.com/Sliverkiss/cpa-plugin/tree/main/qoder",
      "license": "MIT",
      "tags": ["Provider", "OAuth", "Qoder", "Global", "Management"]
    }
```

- [ ] **Step 5: Commit**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add qoder/README.md qoder/panel.html qoder/main.go registry.json
git commit -m "docs(qoder): add README, panel branding and registry entry"
```

---

### Task 8: 构建、部署与线上验收

**Files:**
- Modify: `/opt/homebrew/etc/cliproxyapi.conf`（新增 plugins.configs.qoder，保留其他配置）
- Deploy: `~/.cli-proxy-api/plugins/qoder.dylib`

**前提：** 用户已提供可用的国际版账号，并可在浏览器完成 OAuth（或提供 `pt-` PAT）。

- [ ] **Step 1: 构建 dylib**

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin/qoder
CGO_ENABLED=1 go build -buildmode=c-shared -ldflags "-X main.version=0.1.0" -o qoder.dylib .
ls -lh qoder.dylib
```

Expected: 文件存在且 >1MB。

- [ ] **Step 2: 部署并启用**

先确认现有配置结构（缩进/键名以实际为准）：

```bash
grep -n -A8 '^plugins:' /opt/homebrew/etc/cliproxyapi.conf | head -20
```

在 `plugins.configs` 下新增（保留已有条目）：

```yaml
    qoder:
      enabled: true
```

然后：

```bash
cp qoder.dylib ~/.cli-proxy-api/plugins/
launchctl kickstart -k gui/$(id -u)/sh.brew.cliproxyapi
sleep 2
grep -i 'plugin.*qoder\|qoder.*plugin' ~/.cli-proxy-api/logs/main.log | tail -5
```

Expected: 进程重启；日志无插件加载错误（若配置结构不是 `plugins.configs`，按 `grep` 结果手工合并，不要覆盖其他配置）。

- [ ] **Step 3: 登录并确认模型**

1. 打开 CPA 管理面板 → Auth → Qoder → OAuth 登录，浏览器完成授权（或导入 PAT）。
2. 读取 CPA key 并验证模型列表：

```bash
CPA_KEY=$(python3 - <<'PY'
import re
s = open('/opt/homebrew/etc/cliproxyapi.conf').read()
m = re.search(r'api-keys:\s*\n\s*-\s*"?([^"\n]+)"?', s)
print(m.group(1).strip() if m else '')
PY
)
curl -s http://127.0.0.1:8317/v1/models -H "Authorization: Bearer $CPA_KEY" | grep -o 'claude-opus-5-5' | head -1
```

Expected: 输出 `claude-opus-5-5`。

- [ ] **Step 4: 非流式 / 流式冒烟**

```bash
curl -s http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer $CPA_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"claude-opus-5-5","messages":[{"role":"user","content":"只回复 OK"}],"max_tokens":32}'

curl -N http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer $CPA_KEY" -H 'Content-Type: application/json' \
  -d '{"model":"claude-opus-5-5","stream":true,"messages":[{"role":"user","content":"数到 3"}],"max_tokens":64}'
```

Expected: 第一条返回含 `choices[0].message.content`；第二条出现多个 `data:` chunk 并以 `[DONE]` 结束。

- [ ] **Step 5: 工具往返验收（关键）**

```bash
python3 - <<'PY'
import json, os, urllib.request
key = os.environ["CPA_KEY"]
url = "http://127.0.0.1:8317/v1/chat/completions"
tools = [{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]
def call(messages):
    req = urllib.request.Request(url, data=json.dumps({"model":"claude-opus-5-5","messages":messages,"tools":tools,"tool_choice":"auto"}).encode(), headers={"Authorization":"Bearer "+key,"Content-Type":"application/json"})
    return json.load(urllib.request.urlopen(req, timeout=120))
first = call([{"role":"user","content":"What is the weather in San Francisco? Use the tool."}])
msg = first["choices"][0]["message"]
assert msg.get("tool_calls"), "no tool_calls returned"
tc = msg["tool_calls"][0]
print("tool_call:", tc["function"]["name"], tc["function"]["arguments"])
second = call([{"role":"user","content":"What is the weather in San Francisco? Use the tool."}, msg, {"role":"tool","tool_call_id":tc["id"],"content":'{"temp_c":18,"sky":"sunny"}'}])
fin = second["choices"][0]["message"]
assert fin.get("content"), "no final answer after tool result"
print("final:", fin["content"][:200])
PY
```

Expected: 第一次返回 `get_weather` 的 `tool_calls`；第二次返回自然语言最终回答，无空 stop。

- [ ] **Step 6: Anthropic 入口验收**

```bash
curl -s http://127.0.0.1:8317/v1/messages \
  -H "x-api-key: $CPA_KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' \
  -d '{"model":"claude-opus-5-5","max_tokens":64,"messages":[{"role":"user","content":"只回复 ANTHROPIC_OK"}]}' | grep -o 'ANTHROPIC_OK' | head -1
```

Expected: 输出 `ANTHROPIC_OK`。

- [ ] **Step 7: 面板与签到验收**

打开 `/v0/resource/plugins/qoder/panel`（带 management key）：账号卡片显示积分/plan；点击签到，成功后积分快照刷新。若显示“无可用签到活动”，按 README 提示先在官方客户端领一次，再重试。

- [ ] **Step 8: ZCode 端到端**

在 ZCode 中把模型设为 `claude-opus-5-5`，执行一个需要工具的任务（例如“读取 xxx 文件并总结”），确认：

- 工具调用出现且能拿到结果；
- 多轮工具后仍能产出最终文本；
- `~/.zcode/cli/log/zcode-*.jsonl` 中 `model.response.diagnostics` 不再出现 `responseLength=0` + `toolCallCount=0`。

- [ ] **Step 9: qoderwork 回归**

```bash
curl -s http://127.0.0.1:8317/v1/models -H "Authorization: Bearer $CPA_KEY" | grep -o 'qwen3.8-max' | head -1
```

Expected: 输出 `qwen3.8-max`（CN 插件未被影响）。

- [ ] **Step 10: 记录证据并提交文档修正（如有）**

把实测结果（模型列表、工具往返输出、签到结果、ZCode 日志行）贴回会话；若实现与 spec 有偏差，更新 spec 并 commit：

```bash
cd /Users/xinyuan/Downloads/GitHub/cpa-plugin
git add docs/superpowers/specs/2026-10-21-qoder-global-plugin-design.md
git commit -m "docs(qoder): record live verification results"
```

---

## Self-Review

- **Spec coverage:** 身份/隔离（Task 1、7）、认证（Task 1/4/5）、单模型映射（Task 1/2）、透传执行器（Task 2/3）、工具保留（Task 2/3 测试 + Task 8 Step 5）、401 重试（Task 4）、签到与指纹（Task 6）、面板/文档（Task 7）、部署与实测（Task 8）。
- **Placeholder scan:** 无 TBD/TODO；所有新逻辑均有代码；fork 复用处以精确文件+行号引用。
- **Type consistency:** `buildPassthroughBody` 四返回值在 Task 2/3/4 一致；`collectUpstreamStream` 签名在 Task 3/4 一致；`refreshForRetry` 为包级 var，测试可替换；`clientModelID`/`upstreamUltimate` 全程复用。
