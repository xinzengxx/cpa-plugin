package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestCampaignHeadersUsesConfiguredFingerprint(t *testing.T) {
	setFingerprintForTest(`{"machine_id":"mid","machine_token":"mtok","machine_type":"mtype","machine_os":"mac"}`)
	defer setFingerprintForTest("")

	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := campaignHeaders(req, &storedAuth{Account: storedAccount{UID: "u1"}}); err != nil {
		t.Fatalf("campaignHeaders: %v", err)
	}
	if req.Header.Get("Cosy-MachineId") != "mid" {
		t.Fatalf("machine id = %q", req.Header.Get("Cosy-MachineId"))
	}
	if req.Header.Get("Cosy-MachineToken") != "mtok" || req.Header.Get("Cosy-MachineType") != "mtype" {
		t.Fatalf("fingerprint not applied: %v", req.Header)
	}
	// machine_code is optional and must fall back to the configured machine_id.
	if req.Header.Get("Cosy-MachineCode") != "mid" {
		t.Fatalf("machine code = %q", req.Header.Get("Cosy-MachineCode"))
	}
	if req.Header.Get("Cosy-MachineOS") != "mac" {
		t.Fatalf("machine os = %q", req.Header.Get("Cosy-MachineOS"))
	}
}

func TestCampaignHeadersFallsBackOnMalformedFingerprint(t *testing.T) {
	setFingerprintForTest(`{"machine_id":`)
	defer setFingerprintForTest("")

	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := campaignHeaders(req, &storedAuth{Account: storedAccount{UID: "u1"}}); err != nil {
		t.Fatalf("campaignHeaders: %v", err)
	}
	if got, want := req.Header.Get("Cosy-MachineId"), deriveMachineID("u1"); got != want {
		t.Fatalf("malformed fingerprint must fall back to derived id: got %q want %q", got, want)
	}
	if got, want := req.Header.Get("Cosy-MachineToken"), deriveMachineToken("u1"); got != want {
		t.Fatalf("malformed fingerprint must fall back to derived token: got %q want %q", got, want)
	}
}

func TestCampaignHeadersFallsBackWhenMachineIDMissing(t *testing.T) {
	setFingerprintForTest(`{"machine_token":"mtok","machine_type":"mtype"}`)
	defer setFingerprintForTest("")

	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := campaignHeaders(req, &storedAuth{Account: storedAccount{UID: "u1"}}); err != nil {
		t.Fatalf("campaignHeaders: %v", err)
	}
	if got, want := req.Header.Get("Cosy-MachineId"), deriveMachineID("u1"); got != want {
		t.Fatalf("incomplete fingerprint must fall back to derived id: got %q want %q", got, want)
	}
	if got := req.Header.Get("Cosy-MachineToken"); got != deriveMachineToken("u1") {
		t.Fatalf("incomplete fingerprint must not leak a partial token: got %q", got)
	}
	if got := req.Header.Get("Cosy-MachineCode"); got == "" {
		t.Fatal("Cosy-MachineCode must be set on the derived fallback")
	}
}

func TestCampaignHeadersDerivesStableIdentity(t *testing.T) {
	setFingerprintForTest("")
	sa := &storedAuth{Account: storedAccount{UID: "u1"}}
	first, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	second, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	_ = campaignHeaders(first, sa)
	_ = campaignHeaders(second, sa)
	if first.Header.Get("Cosy-MachineToken") == "" || first.Header.Get("Cosy-MachineType") == "" {
		t.Fatal("derived identity missing")
	}
	if first.Header.Get("Cosy-MachineToken") != second.Header.Get("Cosy-MachineToken") {
		t.Fatal("derived identity must be stable per UID")
	}
	// The derived path must still advertise the hostname (Task 1 behavior).
	if host, err := os.Hostname(); err == nil && host != "" {
		if got := first.Header.Get("Cosy-MachineHostname"); got != host {
			t.Fatalf("derived hostname = %q, want %q", got, host)
		}
	}
}

func setFingerprintForTest(raw string) {
	checkinFingerprintMu.Lock()
	checkinFingerprintJSON = raw
	checkinFingerprintMu.Unlock()
}

func TestConfigureParsesCheckinFingerprint(t *testing.T) {
	line := `checkin_fingerprint: '{"machine_id":"mid","machine_token":"mtok","machine_type":"mtype"}'`
	// config_yaml is a JSON-encoded []byte (base64 on the wire), matching the
	// host's plugin.register/reconfigure envelope.
	raw, err := json.Marshal(map[string][]byte{"config_yaml": []byte(line)})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configure(raw)
	defer setFingerprintForTest("")

	fp := configuredFingerprint()
	if fp == nil {
		t.Fatal("checkin_fingerprint was not parsed from plugin config")
	}
	if fp.MachineID != "mid" || fp.MachineToken != "mtok" || fp.MachineType != "mtype" {
		t.Fatalf("parsed fingerprint = %+v", fp)
	}
}
