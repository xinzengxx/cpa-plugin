package main

import (
	"net/http"
	"testing"
)

func TestCampaignHeadersUsesConfiguredFingerprint(t *testing.T) {
	setFingerprintForTest(`{"machine_id":"mid","machine_token":"mtok","machine_type":"mtype","machine_os":"mac"}`)
	defer setFingerprintForTest("")

	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := campaignHeaders(req, &storedAuth{Account: storedAccount{UID: "u1"}}); err != nil {
		t.Fatalf("campaignHeaders: %v", err)
	}
	if req.Header.Get("Cosy-MachineToken") != "mtok" || req.Header.Get("Cosy-MachineType") != "mtype" {
		t.Fatalf("fingerprint not applied: %v", req.Header)
	}
	if req.Header.Get("Cosy-MachineOS") != "mac" {
		t.Fatalf("machine os = %q", req.Header.Get("Cosy-MachineOS"))
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
}

func setFingerprintForTest(raw string) {
	checkinFingerprintMu.Lock()
	checkinFingerprintJSON = raw
	checkinFingerprintMu.Unlock()
}
