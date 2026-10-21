package main

import (
	"encoding/json"
	"testing"
)

// The fingerprint reaches the plugin through config_yaml on
// plugin.register/reconfigure, same as the Global plugin.
func TestConfigureParsesCheckinFingerprint(t *testing.T) {
	restore := swapFingerprint("")
	defer restore()

	line := `checkin_fingerprint: '{"machine_id":"mid","machine_token":"mtok","machine_type":"mtype"}'`
	raw, err := json.Marshal(map[string][]byte{"config_yaml": []byte(line)})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configure(raw)

	fp := configuredFingerprint()
	if fp == nil {
		t.Fatal("checkin_fingerprint was not parsed from plugin config")
	}
	if fp.MachineID != "mid" || fp.MachineToken != "mtok" || fp.MachineType != "mtype" {
		t.Fatalf("parsed fingerprint = %+v", fp)
	}
}

func TestConfiguredFingerprintRejectsIncomplete(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"malformed":     `{"machine_id":`,
		"missing token": `{"machine_id":"mid","machine_type":"mtype"}`,
		"missing id":    `{"machine_token":"mtok","machine_type":"mtype"}`,
		"missing type":  `{"machine_id":"mid","machine_token":"mtok"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			restore := swapFingerprint(raw)
			defer restore()
			if fp := configuredFingerprint(); fp != nil {
				t.Fatalf("fingerprint %q must be rejected, got %+v", raw, fp)
			}
		})
	}
}
