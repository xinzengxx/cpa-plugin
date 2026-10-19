package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseImageDataURL(t *testing.T) {
	mt, data, ok := parseImageDataURL(testPNGDataURL)
	if !ok || mt != "image/png" || len(data) == 0 {
		t.Fatalf("parse: mt=%q bytes=%d ok=%v", mt, len(data), ok)
	}
	if _, _, ok := parseImageDataURL("https://example.com/a.png"); ok {
		t.Fatal("http url must not parse as data URL")
	}
	if _, _, ok := parseImageDataURL("data:image/png,notbase64"); ok {
		t.Fatal("non-base64 data URL must not parse")
	}
}

func TestNormalizeImageMediaType(t *testing.T) {
	cases := map[string]string{
		"image/png":  "image/png",
		"image/jpg":  "image/jpeg",
		"image/jpeg": "image/jpeg",
		"image/gif":  "image/gif",
		"image/webp": "image/webp",
	}
	for in, want := range cases {
		got, _, err := normalizeImageMediaType(in)
		if err != nil || got != want {
			t.Fatalf("%s: got %q err=%v", in, got, err)
		}
	}
	if _, _, err := normalizeImageMediaType("image/bmp"); err == nil {
		t.Fatal("bmp must be rejected")
	}
}

func TestBuildImageMultipart(t *testing.T) {
	body := buildImageMultipart([]byte("abc"), "image/png", "----qodercli-test", "image.png")
	if !bytes.HasPrefix(body, []byte("------qodercli-test\r\n")) {
		t.Fatalf("multipart prefix: %q", body[:40])
	}
	s := string(body)
	for _, want := range []string{`name="file"`, `filename="image.png"`, "Content-Type: image/png", "abc", "------qodercli-test--"} {
		if !strings.Contains(s, want) {
			t.Fatalf("multipart missing %q: %s", want, s)
		}
	}
}
