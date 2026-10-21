// models.go implements the ModelProvider capability for Qoder Global.
// Two client-facing models are advertised: claude-opus-5-5 mapped to Qoder's
// "ultimate" tier, and deepseek-v4.1-flash mapped to the upstream "dmodel"
// key. There is no dynamic model discovery: entitlement problems surface as
// upstream errors from the executor.
package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	clientModelID    = "claude-opus-5-5"
	upstreamUltimate = "ultimate"
	clientDeepseekID = "deepseek-v4.1-flash"
	upstreamDeepseek = "dmodel" // upstream serves deepseek-v4-pro behind this key
)

func globalModels() []pluginapi.ModelInfo {
	return []pluginapi.ModelInfo{
		{
			ID:                         clientModelID,
			Name:                       "Qoder Ultimate",
			ContextLength:              200000,
			MaxCompletionTokens:        8192,
			OwnedBy:                    providerName,
			SupportedGenerationMethods: []string{"chat"},
		},
		{
			ID:                         clientDeepseekID,
			Name:                       "DeepSeek V4 Pro (via Qoder)",
			ContextLength:              128000,
			MaxCompletionTokens:        8192,
			OwnedBy:                    providerName,
			SupportedGenerationMethods: []string{"chat"},
		},
	}
}

// cpaToUpstreamKey maps a client-facing model id to the upstream gateway key.
func cpaToUpstreamKey(cpaModel string) string {
	m := strings.ToLower(strings.TrimSpace(stripProviderPrefix(cpaModel)))
	switch m {
	case clientModelID, upstreamUltimate:
		return upstreamUltimate
	case clientDeepseekID, upstreamDeepseek:
		return upstreamDeepseek
	}
	return m
}

// isAllowedUpstreamModel is the executor-side guard. Unknown models are
// rejected instead of silently falling back to Auto upstream.
func isAllowedUpstreamModel(model string) bool {
	return model == upstreamUltimate || model == upstreamDeepseek
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
func cacheModelAliases(host pluginapi.HostConfigSummary) {
	entries := host.OAuthModelAlias[providerName]
	if len(entries) == 0 {
		// Host may key the channel case-insensitively; fall back to a scan.
		for channel, list := range host.OAuthModelAlias {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				entries = list
				break
			}
		}
	}
	byAlias := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		alias := strings.TrimSpace(e.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		byAlias[strings.ToLower(alias)] = name
	}
	modelAliasCache.Lock()
	modelAliasCache.byAlias = byAlias
	modelAliasCache.Unlock()
}

// resolveUpstreamModel maps an aliased requested model back to the real
// upstream model ID. Returns the input unchanged when nothing matches.
func resolveUpstreamModel(model string, attributes map[string]string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return model
	}
	key := strings.ToLower(m)
	if name, ok := parseModelAliasAttribute(attributes)[key]; ok {
		return name
	}
	modelAliasCache.RLock()
	name, ok := modelAliasCache.byAlias[key]
	modelAliasCache.RUnlock()
	if ok {
		return name
	}
	return m
}

// parseModelAliasAttribute decodes a per-auth alias override from auth
// attributes. Accepts JSON ([{"name":...,"alias":...}] or {alias:name}) or
// comma-separated "alias=name" pairs.
func parseModelAliasAttribute(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	raw := ""
	for _, k := range []string{"model_alias", "model-alias", "oauth-model-alias"} {
		if v := strings.TrimSpace(attributes[k]); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	add := func(name, alias string) {
		name, alias = strings.TrimSpace(name), strings.TrimSpace(alias)
		if name != "" && alias != "" && !strings.EqualFold(name, alias) {
			out[strings.ToLower(alias)] = name
		}
	}
	if strings.HasPrefix(raw, "[") {
		var list []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, e := range list {
				add(e.Name, e.Alias)
			}
			return out
		}
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			for alias, name := range m {
				add(name, alias)
			}
			return out
		}
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			add(kv[1], kv[0])
		}
	}
	return out
}

// filterExcludedModels removes models listed in oauth-excluded-models for
// the qoderwork provider. The host passes this config via HostConfigSummary.
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	// Try exact provider match, then case-insensitive scan.
	excluded := host.ExcludedModels[providerName]
	if len(excluded) == 0 {
		for channel, list := range host.ExcludedModels {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				excluded = list
				break
			}
		}
	}
	if len(excluded) == 0 {
		return models
	}
	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	// Use a fresh slice — models[:0] would alias the input's backing array.
	// Mutating it in place would corrupt the caller's slice (P0 bug in the
	// fork: after one filterExcludedModels call, the cached "full" list came
	// back filtered on the next fetch).
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}
