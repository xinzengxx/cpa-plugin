package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const testPNGDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl1sAAAAASUVORK5CYII="

func TestNormalizeContentVariants(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"anthropic_text_blocks", []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "text", "text": "world"},
		}, "hello\nworld"},
		{"openai_text_blocks", []any{
			map[string]any{"type": "text", "text": "part1"},
			map[string]any{"type": "text", "text": "part2"},
		}, "part1\npart2"},
		{"tool_result_text", []any{
			map[string]any{"type": "tool_result", "content": "tool output"},
		}, "tool output"},
		{"tool_result_blocks", []any{
			map[string]any{"type": "tool_result", "content": []any{
				map[string]any{"type": "text", "text": "nested"},
			}},
		}, "nested"},
		{"image", []any{map[string]any{"type": "image"}}, "[image]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeContent(tc.in); got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestOpenAIRequestAcceptsAnthropicContentArray(t *testing.T) {
	raw := []byte(`{
		"model":"deepseek-v4.1-flash",
		"max_tokens":512,
		"system":[{"type":"text","text":"be brief"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"只回复"},{"type":"text","text":"OK"}]}
		]
	}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal anthropic request: %v", err)
	}
	if got := extractLatestUserPrompt(req.Messages); got != "只回复\nOK" {
		t.Fatalf("prompt: %q", got)
	}
}

func TestBuildQoderBodyFromAnthropicRequest(t *testing.T) {
	raw := []byte(`{
		"model":"deepseek-v4.1-flash",
		"max_tokens":512,
		"system":[{"type":"text","text":"be brief"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"只回复"},{"type":"text","text":"OK"}]}
		]
	}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(body, &base); err != nil {
		t.Fatal(err)
	}
	if base["model_config"].(map[string]any)["key"] != "dfmodel" {
		t.Fatalf("model key: %v", base["model_config"])
	}
	if base["parameters"].(map[string]any)["max_tokens"].(float64) != 512 {
		t.Fatalf("max_tokens: %v", base["parameters"])
	}
	if got := base["chat_context"].(map[string]any)["text"].(map[string]any)["text"]; got != "只回复\nOK" {
		t.Fatalf("chat_context prompt: %v", got)
	}
	msgs := base["messages"].([]any)
	var sawSystem, sawUser bool
	for _, m := range msgs {
		mm := m.(map[string]any)
		switch mm["role"] {
		case "system":
			if mm["content"] == "be brief" {
				sawSystem = true
			}
		case "user":
			if mm["content"] == "只回复\nOK" {
				sawUser = true
			}
		}
	}
	if !sawSystem || !sawUser {
		t.Fatalf("missing system/user messages: %s", body)
	}
	name := base["business"].(map[string]any)["name"].(string)
	if len([]rune(name)) > 30 || !strings.Contains(name, "只回复") {
		t.Fatalf("business name: %q", name)
	}
}

func TestTextOnlyContentIgnoresImages(t *testing.T) {
	v := []any{
		map[string]any{"type": "text", "text": "看图"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/y.png"}},
	}
	if got := textOnlyContent(v); got != "看图" {
		t.Fatalf("want 看图, got %q", got)
	}
}

func TestMessageContentForQoderUploadsOpenAIImage(t *testing.T) {
	var gotMT string
	var gotData []byte
	up := func(data []byte, mt string) (string, error) {
		gotMT, gotData = mt, data
		return "https://oss.example/image.png", nil
	}
	content, err := messageContentForQoder([]any{
		map[string]any{"type": "text", "text": "什么颜色？"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": testPNGDataURL}},
	}, up)
	if err != nil {
		t.Fatal(err)
	}
	if gotMT != "image/png" || len(gotData) == 0 {
		t.Fatalf("uploader received mt=%q bytes=%d", gotMT, len(gotData))
	}
	blocks, ok := content.([]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("blocks: %#v", content)
	}
	img := blocks[1].(map[string]any)
	if img["type"] != "image_url" || img["image_url"].(map[string]any)["url"] != "https://oss.example/image.png" {
		t.Fatalf("image block: %#v", img)
	}
}

func TestMessageContentForQoderAnthropicImage(t *testing.T) {
	up := func(data []byte, mt string) (string, error) {
		return "https://oss.example/anthropic.png", nil
	}
	content, err := messageContentForQoder([]any{
		map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": "image/png",
				"data":       strings.TrimPrefix(testPNGDataURL, "data:image/png;base64,"),
			},
		},
	}, up)
	if err != nil {
		t.Fatal(err)
	}
	blocks := content.([]any)
	img := blocks[0].(map[string]any)
	if img["image_url"].(map[string]any)["url"] != "https://oss.example/anthropic.png" {
		t.Fatalf("image block: %#v", img)
	}
}

func TestMessageContentForQoderImageURLPassthrough(t *testing.T) {
	called := false
	up := func(data []byte, mt string) (string, error) {
		called = true
		return "", nil
	}
	content, err := messageContentForQoder([]any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/pic.jpg"}},
	}, up)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("http image url must not be re-uploaded")
	}
	img := content.([]any)[0].(map[string]any)
	if img["image_url"].(map[string]any)["url"] != "https://cdn.example/pic.jpg" {
		t.Fatalf("image block: %#v", img)
	}
}

func TestBuildQoderBodyWithImage(t *testing.T) {
	raw := []byte(`{"model":"dfmodel","messages":[{"role":"user","content":[{"type":"text","text":"什么颜色？"},{"type":"image_url","image_url":{"url":"` + testPNGDataURL + `"}}]}]}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	up := func(data []byte, mt string) (string, error) {
		return "https://oss.example/body.png", nil
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", up)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(body, &base); err != nil {
		t.Fatal(err)
	}
	if got := base["chat_context"].(map[string]any)["text"].(map[string]any)["text"]; got != "什么颜色？" {
		t.Fatalf("chat_context prompt: %v", got)
	}
	msgs := base["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	blocks := last["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("content blocks: %#v", blocks)
	}
	img := blocks[1].(map[string]any)
	if img["image_url"].(map[string]any)["url"] != "https://oss.example/body.png" {
		t.Fatalf("image block: %#v", img)
	}
}

func TestBuildQoderBodyImageOnlyUsesDefaultPrompt(t *testing.T) {
	raw := []byte(`{"model":"dfmodel","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + testPNGDataURL + `"}}]}]}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	up := func(data []byte, mt string) (string, error) {
		return "https://oss.example/only.png", nil
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", up)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(body, &base); err != nil {
		t.Fatal(err)
	}
	if got := base["chat_context"].(map[string]any)["text"].(map[string]any)["text"]; got != "请描述这张图片。" {
		t.Fatalf("default prompt: %v", got)
	}
}

func TestBuildQoderBodyUploadFailure(t *testing.T) {
	raw := []byte(`{"model":"dfmodel","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + testPNGDataURL + `"}}]}]}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	up := func(data []byte, mt string) (string, error) {
		return "", fmt.Errorf("upload boom")
	}
	if _, err := buildQoderBody(&req, "dfmodel", "personal_standard", up); err == nil || !strings.Contains(err.Error(), "upload boom") {
		t.Fatalf("want upload error, got %v", err)
	}
}

func testToolDef(name string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": "test tool " + name,
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
				"required":   []any{"city"},
			},
		},
	}
}

func decodeQoderBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var base map[string]any
	if err := json.Unmarshal(body, &base); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return base
}

func toolNameOf(t *testing.T, raw any) string {
	t.Helper()
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("tool entry is %T", raw)
	}
	fn, _ := m["function"].(map[string]any)
	name, _ := fn["name"].(string)
	return name
}

func TestBuildQoderBodyClientModePreservesToolProtocol(t *testing.T) {
	payload := map[string]any{
		"model":       "deepseek-v4.1-flash",
		"max_tokens":  512,
		"system":      []any{map[string]any{"type": "text", "text": "client system"}},
		"tools":       []any{testToolDef("get_weather")},
		"tool_choice": "auto",
		"messages": []any{
			map[string]any{"role": "user", "content": "重庆天气？"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"city":"重庆"}`,
					},
				},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "25C sunny"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := decodeQoderBody(t, body)

	tools, _ := base["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("client tools not forwarded: %d tools", len(tools))
	}
	if got := toolNameOf(t, tools[0]); got != "get_weather" {
		t.Fatalf("client tool name: %q", got)
	}
	if got := base["tool_choice"]; got != "auto" {
		t.Fatalf("tool_choice not preserved: %#v", got)
	}

	msgs, _ := base["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("want system+user+assistant+tool, got %d messages: %s", len(msgs), body)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "client system" {
		t.Fatalf("client system must replace Qoder base system, got %#v", first)
	}
	var assistant, toolMsg map[string]any
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		switch mm["role"] {
		case "assistant":
			assistant = mm
		case "tool":
			toolMsg = mm
		}
	}
	if assistant == nil {
		t.Fatal("assistant message with tool_calls was dropped")
	}
	tcs, _ := assistant["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("assistant tool_calls not preserved: %#v", assistant)
	}
	tc, _ := tcs[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Fatalf("tool call id: %#v", tc)
	}
	if toolMsg == nil {
		t.Fatal("tool result message missing")
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Fatalf("tool_call_id not preserved: %#v", toolMsg)
	}
	if toolMsg["content"] != "25C sunny" {
		t.Fatalf("tool result content: %#v", toolMsg)
	}
}

func TestBuildQoderBodyClientToolBudget(t *testing.T) {
	tools := make([]any, 0, 70)
	for i := 0; i < 70; i++ {
		tools = append(tools, testToolDef(fmt.Sprintf("tool_%d", i)))
	}
	payload := map[string]any{
		"model":    "dfmodel",
		"tools":    tools,
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}
	raw, _ := json.Marshal(payload)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := decodeQoderBody(t, body)
	got, _ := base["tools"].([]any)
	if len(got) != 64 {
		t.Fatalf("want 64 tools after budget, got %d", len(got))
	}
	if name := toolNameOf(t, got[0]); name != "tool_0" {
		t.Fatalf("first tool: %q", name)
	}
	if name := toolNameOf(t, got[63]); name != "tool_63" {
		t.Fatalf("last tool: %q", name)
	}
}

func TestBuildQoderBodyDropsOversizedClientTools(t *testing.T) {
	huge := testToolDef("huge_tool")
	huge["function"].(map[string]any)["description"] = strings.Repeat("x", 200*1024)
	payload := map[string]any{
		"model":    "dfmodel",
		"tools":    []any{huge},
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}
	raw, _ := json.Marshal(payload)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := decodeQoderBody(t, body)
	if got, ok := base["tools"]; ok {
		if arr, _ := got.([]any); len(arr) != 0 {
			t.Fatalf("oversized tool set should be dropped, got %d tools", len(arr))
		}
	}
}

func TestBuildQoderBodyNativeModeKeepsBaseTools(t *testing.T) {
	raw := []byte(`{"model":"dfmodel","messages":[{"role":"user","content":"hello"}]}`)
	var req openAIRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	body, err := buildQoderBody(&req, "dfmodel", "personal_standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := decodeQoderBody(t, body)
	tools, _ := base["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("native mode must keep Qoder base tools")
	}
	found := false
	for _, tool := range tools {
		if toolNameOf(t, tool) == "Bash" {
			found = true
		}
	}
	if !found {
		t.Fatal("native mode missing base Bash tool")
	}
}
