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
