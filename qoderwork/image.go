// image.go implements Qoder CN's vision pipeline: upload an image to the
// gateway's /algo/api/v2/image/upload endpoint and return the OSS URL that the
// chat body can reference in a messages[].image_url block.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	qoderUploadPath     = "/api/v2/image/upload"
	qoderUploadURL      = "https://gateway.qoder.com.cn/algo/api/v2/image/upload"
	maxImageBytes       = 10 << 20
	maxImagesPerRequest = 5
)

// imageUploader is injected into buildQoderBody so tests can run without
// network access; production passes uploadQoderImage bound to the auth.
type imageUploader func(data []byte, mediaType string) (string, error)

func decodeBase64Image(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	return base64.StdEncoding.DecodeString(s)
}

// parseImageDataURL splits data:image/png;base64,... into media type + bytes.
func parseImageDataURL(u string) (mediaType string, data []byte, ok bool) {
	if !strings.HasPrefix(u, "data:") {
		return "", nil, false
	}
	comma := strings.Index(u, ",")
	if comma < 0 {
		return "", nil, false
	}
	meta := u[5:comma]
	if !strings.Contains(meta, ";base64") {
		return "", nil, false
	}
	mt := strings.TrimSpace(strings.SplitN(meta, ";", 2)[0])
	if mt == "" {
		return "", nil, false
	}
	raw, err := decodeBase64Image(u[comma+1:])
	if err != nil {
		return "", nil, false
	}
	return mt, raw, true
}

func normalizeImageMediaType(mt string) (string, string, error) {
	mt = strings.ToLower(strings.TrimSpace(strings.SplitN(mt, ";", 2)[0]))
	switch mt {
	case "image/png":
		return "image/png", "png", nil
	case "image/jpeg", "image/jpg":
		return "image/jpeg", "jpg", nil
	case "image/gif":
		return "image/gif", "gif", nil
	case "image/webp":
		return "image/webp", "webp", nil
	}
	return "", "", fmt.Errorf("unsupported image media type %q (png/jpeg/gif/webp only)", mt)
}

func buildImageMultipart(data []byte, mediaType, boundary, filename string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\nContent-Type: %s\r\n\r\n", boundary, filename, mediaType)
	buf.Write(data)
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)
	return buf.Bytes()
}

// uploadQoderImage PUTs a multipart image and returns the OSS URL. The COSY
// signature for this endpoint signs the multipart body's byte length, not the
// body itself (verified against the CN client on 2026-10-20).
func uploadQoderImage(sa *storedAuth, data []byte, mediaType string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("image is empty")
	}
	if len(data) > maxImageBytes {
		return "", fmt.Errorf("image too large: %d bytes (max %d)", len(data), maxImageBytes)
	}
	mt, ext, err := normalizeImageMediaType(mediaType)
	if err != nil {
		return "", err
	}
	sess, err := cosySessionFor(sa)
	if err != nil {
		return "", err
	}
	rid := strings.ReplaceAll(uuid.NewString(), "-", "")
	boundary := "----qodercli-" + rid
	body := buildImageMultipart(data, mt, boundary, "image."+ext)
	_, date, bearer, err := sess.buildBearerFor(strconv.Itoa(len(body)), qoderUploadPath)
	if err != nil {
		return "", err
	}
	url := qoderUploadURL + "?request_id=" + rid
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	headers := map[string]string{
		"cosy-data-policy":    "AGREE",
		"cosy-machinetype":    sess.MachineType,
		"cosy-clienttype":     "5",
		"cosy-date":           date,
		"cosy-user":           sa.Account.UID,
		"cosy-key":            sess.CosyKey,
		"cosy-clientip":       "169.254.198.161",
		"authorization":       bearer,
		"accept-encoding":     "identity",
		"cosy-version":        "0.1.43",
		"cosy-machineid":      sess.MachineID,
		"cosy-machinetoken":   sess.MachineToken,
		"login-version":       "v2",
		"content-type":        "multipart/form-data; boundary=" + boundary,
		"content-length":      strconv.Itoa(len(body)),
		"accept":              "application/json",
		"AI-CLIENT-TIMESTAMP": date,
		"X-Request-ID":        rid,
		"user-agent":          "Go-http-client/2.0",
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hostHTTPDo(req)
	if err != nil {
		return "", fmt.Errorf("image upload: %w", err)
	}
	raw := resp.Body
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("image upload HTTP %d: %s", resp.StatusCode, truncateRedacted(string(raw), 300))
	}
	var out struct {
		URL    string `json:"url"`
		Result struct {
			URL    string `json:"url"`
			OSSURL string `json:"oss_url"`
		} `json:"result"`
		Data struct {
			URL    string `json:"url"`
			OSSURL string `json:"oss_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("image upload parse: %w", err)
	}
	for _, u := range []string{out.URL, out.Result.URL, out.Result.OSSURL, out.Data.URL, out.Data.OSSURL} {
		if strings.TrimSpace(u) != "" {
			return strings.TrimSpace(u), nil
		}
	}
	return "", fmt.Errorf("image upload response missing url: %s", truncateRedacted(string(raw), 200))
}
