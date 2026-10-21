// body.go constructs the QoderWork agent_chat_generation request body from
// OpenAI-style chat completion inputs.
//
// The base template lives in baseprompt.json (embedded). Per-request we
// overwrite request/session ids, timestamps, model key, and the user prompt.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

//go:embed baseprompt.json
var basepromptJSON []byte

// cpaToUpstreamKey maps CPA-facing model names to the gateway's current keys.
// Only Qwen and DeepSeek are mapped: the model list and executor guard both
// reject every other family, so an unknown name is left for the guard to catch.
func cpaToUpstreamKey(cpaModel string) string {
	switch cpaModel {
	case "qmodel_38max", "qwen3.8-max", "qwen3.8-max-preview", "qmodel_preview":
		return "qmodel_38max"
	case "qfmodel", "qwen3.8-flash":
		return "qfmodel"
	case "qmodel_latest", "qwen3.7-max":
		return "qmodel_latest"
	case "qmodel", "qwen3.7-plus":
		return "qmodel"
	case "q37fmodel", "qwen3.7-flash":
		return "q37fmodel"
	case "dmodel", "deepseek-v4-pro":
		return "dmodel"
	case "dfmodel", "deepseek-v4-flash", "deepseek-v4.1-flash":
		return "dfmodel"
	}
	return cpaModel
}

// openAIMessage accepts both OpenAI string content and provider-translated
// content-block arrays (Anthropic /v1/messages reaches the plugin this way).
type openAIMessage struct {
	Role       string          `json:"role"`
	Content    any             `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// openAIRequest is the CPA-facing chat completion request. System and
// MaxTokens cover the Anthropic /v1/messages shape as well.
type openAIRequest struct {
	Model      string            `json:"model"`
	Messages   []openAIMessage   `json:"messages"`
	System     any               `json:"system"`
	Stream     bool              `json:"stream"`
	MaxTokens  int64             `json:"max_tokens"`
	Tools      []json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage   `json:"tool_choice,omitempty"`
}

const (
	maxClientTools     = 64
	maxClientToolBytes = 128 * 1024
)

// selectClientTools keeps a bounded prefix of the client tool list. Qoder's
// agent endpoint shares the model context with the tool schemas, and clients
// such as ZCode can send 100+ MCP tools in one request.
func selectClientTools(tools []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, minInt(len(tools), maxClientTools))
	total := 0
	for _, tool := range tools {
		if len(out) >= maxClientTools {
			break
		}
		if total+len(tool) > maxClientToolBytes {
			break
		}
		out = append(out, tool)
		total += len(tool)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// normalizeContent flattens string content and text/tool_result/tool_use/image
// blocks into plain text for the Qoder agent template.
func normalizeContent(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := normalizeContent(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		switch typ, _ := x["type"].(string); typ {
		case "text":
			if s, ok := x["text"].(string); ok {
				return s
			}
		case "tool_result":
			if c, ok := x["content"]; ok {
				return normalizeContent(c)
			}
			if s, ok := x["text"].(string); ok {
				return s
			}
		case "tool_use":
			name, _ := x["name"].(string)
			input, _ := json.Marshal(x["input"])
			return "[tool_use " + name + "] " + string(input)
		case "image", "image_url":
			return "[image]"
		case "document":
			return "[document]"
		}
		if s, ok := x["text"].(string); ok {
			return s
		}
		if c, ok := x["content"]; ok {
			return normalizeContent(c)
		}
	}
	return ""
}

// textOnlyContent is normalizeContent without image placeholders, used for
// chat_context.text.text so the model doesn't see a literal "[image]" next to
// a real image attachment.
func textOnlyContent(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if b, ok := item.(map[string]any); ok {
				typ, _ := b["type"].(string)
				if typ == "image" || typ == "image_url" {
					continue
				}
			}
			if s := normalizeContent(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		typ, _ := x["type"].(string)
		if typ == "image" || typ == "image_url" {
			return ""
		}
		return normalizeContent(x)
	}
	return ""
}

func containsImage(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if containsImage(item) {
				return true
			}
		}
	case map[string]any:
		if typ, _ := x["type"].(string); typ == "image" || typ == "image_url" {
			return true
		}
		if c, ok := x["content"]; ok && containsImage(c) {
			return true
		}
	}
	return false
}

// extractLatestUserPrompt returns the text of the last user message, ignoring
// image blocks (the image itself is carried in messages[]).
func extractLatestUserPrompt(messages []openAIMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			if s := textOnlyContent(messages[i].Content); s != "" {
				return s
			}
		}
	}
	return ""
}

func imageURLFromBlock(block map[string]any) string {
	if iu, ok := block["image_url"].(map[string]any); ok {
		if u, ok := iu["url"].(string); ok {
			return strings.TrimSpace(u)
		}
	}
	if u, ok := block["url"].(string); ok {
		return strings.TrimSpace(u)
	}
	return ""
}

func imageBlockForQoder(url string) map[string]any {
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": url},
	}
}

// messageContentForQoder converts one CPA message into the Qoder messages[]
// content shape. Text blocks stay text; image blocks are uploaded and become
// image_url blocks; tool blocks are flattened to text.
func messageContentForQoder(v any, upload imageUploader) (any, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case []any:
		blocks := make([]any, 0, len(x))
		images := 0
		addText := func(s string) {
			if s != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": s})
			}
		}
		addImage := func(u string) {
			blocks = append(blocks, imageBlockForQoder(u))
		}
		for _, item := range x {
			switch b := item.(type) {
			case string:
				addText(b)
			case map[string]any:
				typ, _ := b["type"].(string)
				switch typ {
				case "text":
					if s, ok := b["text"].(string); ok {
						addText(s)
					}
				case "image_url":
					images++
					if images > maxImagesPerRequest {
						return nil, fmt.Errorf("too many images: max %d", maxImagesPerRequest)
					}
					u := imageURLFromBlock(b)
					if u == "" {
						return nil, fmt.Errorf("image_url block missing url")
					}
					if strings.HasPrefix(u, "data:") {
						if upload == nil {
							addText("[image]")
							continue
						}
						mt, data, ok := parseImageDataURL(u)
						if !ok {
							return nil, fmt.Errorf("invalid image data URL")
						}
						if len(data) > maxImageBytes {
							return nil, fmt.Errorf("image too large: %d bytes (max %d)", len(data), maxImageBytes)
						}
						uploaded, err := upload(data, mt)
						if err != nil {
							return nil, err
						}
						u = uploaded
					} else if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
						return nil, fmt.Errorf("unsupported image url %q", u)
					}
					addImage(u)
				case "image":
					images++
					if images > maxImagesPerRequest {
						return nil, fmt.Errorf("too many images: max %d", maxImagesPerRequest)
					}
					source, _ := b["source"].(map[string]any)
					srcType, _ := source["type"].(string)
					switch srcType {
					case "base64":
						mt, _ := source["media_type"].(string)
						dataStr, _ := source["data"].(string)
						data, err := decodeBase64Image(dataStr)
						if err != nil {
							return nil, fmt.Errorf("image base64: %w", err)
						}
						if len(data) > maxImageBytes {
							return nil, fmt.Errorf("image too large: %d bytes (max %d)", len(data), maxImageBytes)
						}
						if upload == nil {
							addText("[image]")
							continue
						}
						uploaded, err := upload(data, mt)
						if err != nil {
							return nil, err
						}
						addImage(uploaded)
					case "url":
						u, _ := source["url"].(string)
						u = strings.TrimSpace(u)
						if u == "" {
							return nil, fmt.Errorf("image url source missing url")
						}
						if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
							return nil, fmt.Errorf("unsupported image url %q", u)
						}
						addImage(u)
					default:
						return nil, fmt.Errorf("unsupported image source type %q", srcType)
					}
				default:
					addText(normalizeContent(b))
				}
			default:
				addText(normalizeContent(item))
			}
		}
		if len(blocks) == 0 {
			return "", nil
		}
		hasImage := false
		for _, b := range blocks {
			if m, ok := b.(map[string]any); ok && m["type"] == "image_url" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			parts := make([]string, 0, len(blocks))
			for _, b := range blocks {
				if m, ok := b.(map[string]any); ok && m["type"] == "text" {
					if s, ok := m["text"].(string); ok && s != "" {
						parts = append(parts, s)
					}
				}
			}
			return strings.Join(parts, "\n"), nil
		}
		return blocks, nil
	case map[string]any:
		return messageContentForQoder([]any{x}, upload)
	}
	return "", nil
}

// buildQoderBody renders the upstream agent_chat_generation body for one request.
// modelKey is the upstream key (already mapped via cpaToUpstreamKey).
func buildQoderBody(req *openAIRequest, modelKey, userType string, upload imageUploader) ([]byte, error) {
	var base map[string]any
	if err := json.Unmarshal(basepromptJSON, &base); err != nil {
		return nil, fmt.Errorf("baseprompt decode: %w", err)
	}

	prompt := extractLatestUserPrompt(req.Messages)
	if prompt == "" {
		hasImage := false
		for _, m := range req.Messages {
			if containsImage(m.Content) {
				hasImage = true
				break
			}
		}
		if !hasImage {
			return nil, fmt.Errorf("no user message in request")
		}
		prompt = "请描述这张图片。"
	}

	nid := uuid.NewString()
	base["request_id"] = nid
	base["chat_record_id"] = nid
	base["request_set_id"] = uuid.NewString()
	base["session_id"] = uuid.NewString()
	base["stream"] = true
	base["aliyun_user_type"] = userType
	base["agent_id"] = "agent_common"

	// model_config
	if mc, ok := base["model_config"].(map[string]any); ok {
		mc["key"] = modelKey
	}
	if req.MaxTokens > 0 {
		if params, ok := base["parameters"].(map[string]any); ok {
			params["max_tokens"] = req.MaxTokens
		}
	}

	// chat_context.text.text + chat_context.extra.originalContent.text
	if cc, ok := base["chat_context"].(map[string]any); ok {
		if txt, ok := cc["text"].(map[string]any); ok {
			txt["text"] = prompt
		}
		if extra, ok := cc["extra"].(map[string]any); ok {
			if oc, ok := extra["originalContent"].(map[string]any); ok {
				oc["text"] = prompt
			}
			if mc, ok := extra["modelConfig"].(map[string]any); ok {
				mc["key"] = modelKey
			}
		}
	}

	// Client-agent mode: the client supplies its own system prompt and tool
	// table, so Qoder's built-in agent prompt/tools must not be mixed in.
	clientMode := req.Tools != nil
	if clientMode {
		selected := selectClientTools(req.Tools)
		if len(selected) > 0 {
			tools := make([]any, 0, len(selected))
			for _, tool := range selected {
				tools = append(tools, tool)
			}
			base["tools"] = tools
			if len(req.ToolChoice) > 0 {
				base["tool_choice"] = req.ToolChoice
			}
		} else {
			delete(base, "tools")
		}
		if len(selected) == 0 {
			delete(base, "tool_choice")
		}
	}

	// messages: in client mode the client system prompt replaces the Qoder
	// agent system prompt; otherwise keep the template system prompt.
	var outMsgs []any
	if clientMode {
		var sysParts []string
		if s := normalizeContent(req.System); s != "" {
			sysParts = append(sysParts, s)
		}
		for _, m := range req.Messages {
			if strings.EqualFold(strings.TrimSpace(m.Role), "system") {
				if s := normalizeContent(m.Content); s != "" {
					sysParts = append(sysParts, s)
				}
			}
		}
		sys := joinSystemParts(sysParts)
		if sys == "" {
			sys = "You are a helpful assistant."
		}
		outMsgs = append(outMsgs, map[string]any{"role": "system", "content": sys})
	} else {
		if msgs, ok := base["messages"].([]any); ok {
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok {
					if role, _ := mm["role"].(string); role == "system" {
						outMsgs = append(outMsgs, m)
					}
				}
			}
		}
		if sys := normalizeContent(req.System); sys != "" {
			outMsgs = append(outMsgs, map[string]any{"role": "system", "content": sys})
		}
	}
	// Append the actual conversation. Text stays text; image blocks are
	// uploaded and kept as messages[].image_url so the vision model sees them.
	for _, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if clientMode && role == "system" {
			continue
		}
		content, err := messageContentForQoder(m.Content, upload)
		if err != nil {
			return nil, err
		}
		msg := map[string]any{"role": m.Role, "content": content}
		if role == "assistant" && len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if role == "tool" {
			// Qoder validates tool_call_id on tool messages; when a client
			// omits it, keep the result as text instead of sending a request
			// the upstream will reject.
			if m.ToolCallID == "" {
				text := normalizeContent(m.Content)
				if text == "" {
					continue
				}
				msg["role"] = "user"
				msg["content"] = "[tool result]\n" + text
				outMsgs = append(outMsgs, msg)
				continue
			}
			msg["tool_call_id"] = m.ToolCallID
			msg["content"] = normalizeContent(m.Content)
		}
		if content == "" && role != "tool" && !(role == "assistant" && len(m.ToolCalls) > 0) {
			continue
		}
		outMsgs = append(outMsgs, msg)
	}
	base["messages"] = outMsgs

	// business
	if biz, ok := base["business"].(map[string]any); ok {
		biz["id"] = uuid.NewString()
		biz["begin_at"] = time.Now().UnixMilli()
		biz["name"] = truncateRunes(prompt, 30)
	}

	return json.Marshal(base)
}

func joinSystemParts(parts []string) string {
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return strings.Join(out, "\n\n")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
