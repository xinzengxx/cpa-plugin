package main

import "testing"

func TestCPAToUpstreamKey(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":       "ultimate",
		"qoder/claude-opus-5-5": "ultimate",
		"Claude-Opus-5-5":       "ultimate",
		"ultimate":              "ultimate",
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
