package main

import "testing"

func TestCPAToUpstreamKey(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":           "ultimate",
		"qoder/claude-opus-5-5":     "ultimate",
		"Claude-Opus-5-5":           "ultimate",
		"ultimate":                  "ultimate",
		"deepseek-v4.1-flash":       "dmodel",
		"DeepSeek-V4.1-Flash":       "dmodel",
		"qoder/deepseek-v4.1-flash": "dmodel",
		"dmodel":                    "dmodel",
	}
	for in, want := range cases {
		if got := cpaToUpstreamKey(in); got != want {
			t.Errorf("cpaToUpstreamKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownModelRejectedByGuard(t *testing.T) {
	for _, in := range []string{"sonus", "cantus", "auto", "qwen3.8-max", "", "deepseek-v4-pro", "dfmodel"} {
		if isAllowedUpstreamModel(cpaToUpstreamKey(in)) {
			t.Errorf("%q must not pass the executor guard", in)
		}
	}
	// The two advertised models must pass.
	for _, in := range []string{"claude-opus-5-5", "deepseek-v4.1-flash", "dmodel"} {
		if !isAllowedUpstreamModel(cpaToUpstreamKey(in)) {
			t.Errorf("%q must pass the executor guard", in)
		}
	}
}

func TestGlobalModelsAdvertisesTwoModels(t *testing.T) {
	models := globalModels()
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].ID != "claude-opus-5-5" {
		t.Fatalf("model id = %q, want claude-opus-5-5", models[0].ID)
	}
	if models[1].ID != "deepseek-v4.1-flash" {
		t.Fatalf("model id = %q, want deepseek-v4.1-flash", models[1].ID)
	}
	if models[0].OwnedBy != "qoder" || models[1].OwnedBy != "qoder" {
		t.Fatalf("owned_by = %q / %q, want qoder", models[0].OwnedBy, models[1].OwnedBy)
	}
}
