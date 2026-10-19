package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestQoderModelFilterKeepsOnlyQwenAndDeepSeek(t *testing.T) {
	models := filterAllowedModels(wbModels())
	if len(models) != 7 {
		t.Fatalf("want 7 allowed models, got %d", len(models))
	}
	for _, m := range models {
		if m.ID == "auto" || m.ID == "gmodel" || m.ID == "gfmodel" || m.ID == "gm51model" ||
			m.ID == "kmodel" || m.ID == "kmodel_latest" || m.ID == "mmodel" {
			t.Fatalf("non-Qwen/DeepSeek model leaked: %s", m.ID)
		}
	}
}

func TestQoderClientAliasesCoverDeepSeekWorkBuddyNames(t *testing.T) {
	models := withClientModelAliases(filterAllowedModels(wbModels()))
	have := make(map[string]bool, len(models))
	for _, m := range models {
		have[m.ID] = true
	}
	for _, id := range []string{"qwen3.8-max", "qwen3.7-flash", "deepseek-v4-pro", "deepseek-v4-flash", "deepseek-v4.1-flash"} {
		if !have[id] {
			t.Fatalf("missing client alias %s", id)
		}
	}
}

func TestDeepSeekAliasesMapToQoderKeys(t *testing.T) {
	cases := map[string]string{
		"deepseek-v4-pro":     "dmodel",
		"deepseek-v4-flash":   "dfmodel",
		"deepseek-v4.1-flash": "dfmodel",
		"qwen3.8-max":         "qmodel_38max",
		"qwen3.7-flash":       "q37fmodel",
	}
	for in, want := range cases {
		if got := cpaToUpstreamKey(in); got != want {
			t.Fatalf("%s: want %s, got %s", in, want, got)
		}
	}
}

func TestUpstreamGuardRejectsOtherFamilies(t *testing.T) {
	for _, id := range []string{"qmodel_38max", "qfmodel", "qmodel_latest", "qmodel", "q37fmodel", "dmodel", "dfmodel"} {
		if !isAllowedUpstreamModel(id) {
			t.Fatalf("%s should be allowed", id)
		}
	}
	for _, id := range []string{"auto", "gmodel", "gm51model", "kmodel", "mmodel"} {
		if isAllowedUpstreamModel(id) {
			t.Fatalf("%s should be rejected", id)
		}
	}
}

func TestAllowedQoderModelByDisplayName(t *testing.T) {
	if !allowedQoderModel(pluginapi.ModelInfo{ID: "future", Name: "Qwen4-Max"}) {
		t.Fatal("future Qwen model should be allowed by display name")
	}
	if !allowedQoderModel(pluginapi.ModelInfo{ID: "future", Name: "DeepSeek-V5"}) {
		t.Fatal("future DeepSeek model should be allowed by display name")
	}
	if allowedQoderModel(pluginapi.ModelInfo{ID: "future", Name: "GLM-6"}) {
		t.Fatal("GLM must not be allowed")
	}
}
