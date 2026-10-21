package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamErrorWire(t *testing.T) {
	raw := streamErrorWire("stream-1", "Authorization: Bearer abcdefghijklmnop")
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("wire JSON: %v", err)
	}
	if m["stream_id"] != "stream-1" {
		t.Fatalf("stream_id: %#v", m)
	}
	msg, _ := m["error"].(string)
	if msg == "" {
		t.Fatalf("error message missing: %#v", m)
	}
	if strings.Contains(msg, "abcdefghijklmnop") {
		t.Fatalf("bearer token leaked: %s", msg)
	}
	if _, ok := m["payload"]; ok {
		t.Fatalf("error wire must not carry a payload: %#v", m)
	}
}

func TestSSEUsageCollectorTracksOutput(t *testing.T) {
	c := &sseUsageCollector{}
	c.feed(`{"choices":[{"delta":{"role":"assistant"}}]}`)
	if c.hasOutput {
		t.Fatal("role-only chunk must not count as output")
	}
	c.feed(`{"choices":[{"delta":{"content":"hi"}}]}`)
	if !c.hasOutput {
		t.Fatal("content chunk must count as output")
	}

	tool := &sseUsageCollector{}
	tool.feed(`{"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"get_weather"}}]}}]}`)
	if !tool.hasOutput {
		t.Fatal("tool call chunk must count as output")
	}

	reasoning := &sseUsageCollector{}
	reasoning.feed(`{"choices":[{"delta":{"reasoning_content":"think"}}]}`)
	if !reasoning.hasOutput {
		t.Fatal("reasoning chunk must count as output")
	}
}
