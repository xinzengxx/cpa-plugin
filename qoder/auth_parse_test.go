// auth_parse_test.go covers the ownership contract of handleParseAuth:
// qoder credentials are claimed, qoderwork/workbuddy credentials are not —
// including the type-less legacy files that share the nested {auth,account}
// shape.
package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type parseAuthEnvelope struct {
	OK     bool `json:"ok"`
	Result struct {
		Handled bool               `json:"handled"`
		Auth    pluginapi.AuthData `json:"auth"`
	} `json:"result"`
}

func runParseAuth(t *testing.T, fileName, provider string, raw string) parseAuthEnvelope {
	t.Helper()
	req, err := json.Marshal(pluginapi.AuthParseRequest{
		Provider: provider,
		FileName: fileName,
		RawJSON:  []byte(raw),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	out, err := handleParseAuth(req)
	if err != nil {
		t.Fatalf("handleParseAuth: %v", err)
	}
	var env parseAuthEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode envelope (%s): %v", out, err)
	}
	return env
}

func TestHandleParseAuthOwnership(t *testing.T) {
	const qoderCred = `{"auth":{"accessToken":"dt-x","refreshToken":"drt-x"},"account":{"uid":"uid-1","nickname":"alice"}}`
	tests := []struct {
		name     string
		fileName string
		provider string
		raw      string
		handled  bool
	}{
		{
			name:     "declared qoder type is claimed",
			fileName: "anything.json",
			raw:      `{"type":"qoder",` + qoderCred[1:],
			handled:  true,
		},
		{
			name:     "declared qoderwork type is rejected",
			fileName: "qoder-uid-1.json",
			raw:      `{"type":"qoderwork",` + qoderCred[1:],
			handled:  false,
		},
		{
			name:     "declared workbuddy type is rejected",
			fileName: "qoder-uid-1.json",
			raw:      `{"type":"workbuddy",` + qoderCred[1:],
			handled:  false,
		},
		{
			name:     "typeless qoder-<uid> file is claimed by prefix",
			fileName: "qoder-uid-1.json",
			raw:      qoderCred,
			handled:  true,
		},
		{
			name:     "typeless qoderwork file is rejected by prefix",
			fileName: "qoderwork-x.json",
			raw:      qoderCred,
			handled:  false,
		},
		{
			name:     "typeless nested qoderwork credential is rejected",
			fileName: "qoderwork-legacy.json",
			raw:      `{"auth":{"accessToken":"jt-legacy","refreshToken":"jrt-legacy"},"account":{"uid":"workbuddy-uid"}}`,
			handled:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := runParseAuth(t, tc.fileName, tc.provider, tc.raw)
			if !env.OK {
				t.Fatalf("envelope not ok")
			}
			if env.Result.Handled != tc.handled {
				t.Fatalf("handled = %v, want %v", env.Result.Handled, tc.handled)
			}
			if tc.handled {
				if env.Result.Auth.FileName != tc.fileName {
					t.Fatalf("claimed auth file name = %q, want %q", env.Result.Auth.FileName, tc.fileName)
				}
				if env.Result.Auth.Provider != providerName {
					t.Fatalf("claimed provider = %q, want %q", env.Result.Auth.Provider, providerName)
				}
			}
		})
	}
}

// TestHandleParseAuthDeclaredQoderKeepsFileName guards the echo-back rule that
// prevents host-side rename duplicates.
func TestHandleParseAuthDeclaredQoderKeepsFileName(t *testing.T) {
	env := runParseAuth(t, "qoder-abc.json", "", `{"type":"qoder","auth":{"accessToken":"dt-x"},"account":{"uid":"abc"}}`)
	if !env.OK || !env.Result.Handled {
		t.Fatalf("qoder file not claimed: %+v", env)
	}
	if env.Result.Auth.FileName != "qoder-abc.json" {
		t.Fatalf("file name = %q", env.Result.Auth.FileName)
	}
	if env.Result.Auth.ID != "" {
		t.Fatalf("ID must stay empty (host derives from path), got %q", env.Result.Auth.ID)
	}
}
