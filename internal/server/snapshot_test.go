package server

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/flinternet/flinternet/internal/pinterest"
)

func TestMediaSpecRoundTripAndValidation(t *testing.T) {
	imgCases := []struct {
		url      string
		wantSpec string
	}{
		{"https://i.pinimg.com/originals/b1/b7/17/b1b717714fc20ded79a7580af314718d.jpg", "oj:b1b717714fc20ded79a7580af314718d"},
		{"https://i.pinimg.com/736x/9e/e2/81/9ee281534cc158ecb1f413a35e293ee1.png", "7p:9ee281534cc158ecb1f413a35e293ee1"},
		{"https://i.pinimg.com/280x280_RS/30/52/20/305220a1685dd84587ed52ac0de3651f.jpg", "r2j:305220a1685dd84587ed52ac0de3651f"},
		{"https://i.pinimg.com/custom/path/avatar.jpg", "r:custom/path/avatar.jpg"},
	}
	for _, tc := range imgCases {
		spec := encodeImageSpec(tc.url)
		if spec != tc.wantSpec {
			t.Fatalf("encodeImageSpec(%q) = %q, want %q", tc.url, spec, tc.wantSpec)
		}
		if got := decodeImageSpec(spec); got != tc.url {
			t.Fatalf("decodeImageSpec(%q) = %q, want %q", spec, got, tc.url)
		}
	}

	vidCases := []struct {
		url      string
		wantSpec string
	}{
		{"https://v1.pinimg.com/videos/iht/hls/34/53/4f/34534fe6edc6a7cfa3eecbeba0f89e7e.m3u8", "ih:34534fe6edc6a7cfa3eecbeba0f89e7e"},
		{"https://v1.pinimg.com/videos/mc/hls/c8/1b/f1/c81bf19c9cec41262dca4491641097e5.m3u8", "mh:c81bf19c9cec41262dca4491641097e5"},
		{"https://v1.pinimg.com/videos/v2/hls/d8/2b/dd/d82bdda6d07a8ff6ff66194674f5093a_mobile.m3u8", "vm:d82bdda6d07a8ff6ff66194674f5093a"},
		{"https://v1.pinimg.com/videos/iht/expMp4/aa/bb/cc/aabbcc_t4.mp4", "v:videos/iht/expMp4/aa/bb/cc/aabbcc_t4.mp4"},
	}
	for _, tc := range vidCases {
		spec := encodeVideoSpec(tc.url)
		if spec != tc.wantSpec {
			t.Fatalf("encodeVideoSpec(%q) = %q, want %q", tc.url, spec, tc.wantSpec)
		}
		if got := decodeVideoSpec(spec); got != tc.url {
			t.Fatalf("decodeVideoSpec(%q) = %q, want %q", spec, got, tc.url)
		}
	}

	// SSRF guard: arbitrary external host specs must be rejected.
	if decodeImageSpec("https://evil.example.com/x.jpg") != "" || encodeImageSpec("https://evil.example.com/x.jpg") != "" {
		t.Fatalf("expected non-pinimg image URL to be rejected")
	}
	if decodeVideoSpec("https://evil.example.com/v.m3u8") != "" || encodeVideoSpec("https://evil.example.com/v.m3u8") != "" {
		t.Fatalf("expected non-pinimg video URL to be rejected")
	}
}

func TestPinSnapshotKeylessKeyedAndSizeBudget(t *testing.T) {
	sample := pinterest.Pin{
		ID:               "e1_AfqPShwrQphp4xo3U0QODnap",
		AggregatedPinID:  "3646447484740074368",
		Title:            "Northern Michigan Coastline Sunset",
		Description:      strings.Repeat("Lake Michigan waves crashing on the shore. ", 40),
		ImageURL:         "https://i.pinimg.com/originals/63/87/42/638742c36a8ba5799b0c7bf56d49a2c3.jpg",
		FallbackImageURL: "https://i.pinimg.com/originals/63/87/42/638742c36a8ba5799b0c7bf56d49a2c3.jpg",
		HLSVideoURL:      "https://v1.pinimg.com/videos/iht/hls/34/53/4f/34534fe6edc6a7cfa3eecbeba0f89e7e.m3u8",
		Width:            720,
		Height:           1280,
		Duration:         14.5,
		Likes:            42,
		Saves:            120,
		CommentCount:     3,
		VisualTags:       []string{"Michigan", "Lake", "Sunset", "Travel", "Outdoors"},
		Pinner: pinterest.OwnerSummary{
			Username:  "puremichigan",
			FullName:  "Pure Michigan",
			AvatarURL: "https://i.pinimg.com/140x140_RS/aa/bb/cc/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg",
		},
		Board: pinterest.BoardSummary{
			Username: "puremichigan",
			Slug:     "great-lakes",
			Name:     "Great Lakes",
		},
	}

	// 1. Keyless mode (nil codec -> deflated base64url).
	keylessTok, err := encodeBoundedPinToken(nil, sample)
	if err != nil || keylessTok == "" || len(keylessTok) > maxSnapshotTokenChars {
		t.Fatalf("keyless encode failed: len=%d err=%v", len(keylessTok), err)
	}
	restoredKeyless, authKeyless, ok := decodePinSnapshot(nil, sample.ID, keylessTok)
	if !ok || authKeyless || restoredKeyless.Title != sample.Title || restoredKeyless.HLSVideoURL != sample.HLSVideoURL ||
		restoredKeyless.AggregatedPinID != sample.AggregatedPinID || restoredKeyless.Pinner.Username != "puremichigan" ||
		!restoredKeyless.IsVideo || restoredKeyless.Likes != 42 {
		t.Fatalf("keyless restore mismatch: ok=%v auth=%v restored=%+v", ok, authKeyless, restoredKeyless)
	}

	// 2. Keyed mode (AES-256-GCM + deflate + AAD bound to pin ID).
	codec, err := newShareCodec("super-secret-share-key")
	if err != nil || codec == nil {
		t.Fatalf("newShareCodec failed: %v", err)
	}
	keyedTok, err := encodeBoundedPinToken(codec, sample)
	if err != nil || keyedTok == "" || len(keyedTok) > maxSnapshotTokenChars {
		t.Fatalf("keyed encode failed: len=%d err=%v", len(keyedTok), err)
	}
	if strings.Contains(keyedTok, "Michigan") || strings.Contains(keyedTok, "puremichigan") {
		t.Fatalf("plaintext metadata leaked in encrypted token")
	}

	restoredKeyed, authKeyed, ok := decodePinSnapshot(codec, sample.ID, keyedTok)
	if !ok || !authKeyed || restoredKeyed.Title != sample.Title || restoredKeyed.ImageURL != sample.ImageURL || restoredKeyed.Board.Slug != "great-lakes" {
		t.Fatalf("keyed restore mismatch: ok=%v auth=%v restored=%+v", ok, authKeyed, restoredKeyed)
	}

	// Previously shared keyless token must still decode cleanly (as unauthenticated) even after enabling ShareKey.
	legacyRestored, legacyAuth, legacyOK := decodePinSnapshot(codec, sample.ID, keylessTok)
	if !legacyOK || legacyAuth || legacyRestored.Title != sample.Title {
		t.Fatalf("legacy keyless link broke after enabling ShareKey: ok=%v auth=%v", legacyOK, legacyAuth)
	}

	// Cross-pin, wrong-key, and encrypted->no-key rejections.
	if _, _, ok := decodePinSnapshot(codec, "e1_other_pin", keyedTok); ok {
		t.Fatalf("expected cross-pin AAD check to fail")
	}
	if _, _, ok := decodePinSnapshot(nil, sample.ID, keyedTok); ok {
		t.Fatalf("expected encrypted token to fail when codec is nil")
	}
	otherCodec, _ := newShareCodec("wrong-key")
	if _, _, ok := decodePinSnapshot(otherCodec, sample.ID, keyedTok); ok {
		t.Fatalf("expected wrong key to reject token")
	}
}

func TestUntrustedSnapshotSanitizationAndBombProtection(t *testing.T) {
	// Malicious raw JSON token attempting XSS, CSS injection, open redirects, path traversal, and invalid stats.
	maliciousJSON := `{"i":"oj:b1b717714fc20ded79a7580af314718d","c":"red;background:url(https://evil.com)","pu":"/evil.com","bu":"../../admin","bs":"bad?next=1","ln":"javascript:alert(1)","a":"123<script>","t":"<script>alert(1)</script>\u202e\u0000","dm":"evil.com/phish","w":-50,"h":999999,"d":-5,"lk":-1,"tg":["<img src=x onerror=alert(1)>"]}`
	evilTok := base64.RawURLEncoding.EncodeToString(append([]byte{'j'}, maliciousJSON...))

	pin, auth, ok := decodePinSnapshot(nil, "e1_safe-1", evilTok)
	if !ok || auth {
		t.Fatalf("expected valid unauthenticated image pin after sanitizing dangerous optional fields (ok=%v auth=%v)", ok, auth)
	}
	if pin.DominantColor != "" || pin.Pinner.Username != "" || pin.Board.Username != "" ||
		pin.Board.Slug != "" || pin.Board.URL != "" || pin.Link != "" || pin.AggregatedPinID != "" ||
		pin.Domain != "" || pin.Width != 0 || pin.Height != 0 || pin.Duration != 0 || pin.Likes != 0 {
		t.Fatalf("malicious snapshot fields survived sanitization: %+v", pin)
	}
	if strings.ContainsRune(pin.Title, '\u202e') || strings.ContainsRune(pin.Title, 0) {
		t.Fatalf("control/bidi chars survived sanitization in title: %q", pin.Title)
	}

	// Path traversal or query injection inside r: / v: media specs must reject the snapshot cleanly.
	for _, badMediaSpec := range []string{
		`{"i":"r:originals/../../secret.jpg"}`,
		`{"i":"r:originals/foo.jpg?evil=1"}`,
		`{"v":"v:videos/../../evil.m3u8"}`,
	} {
		tok := base64.RawURLEncoding.EncodeToString(append([]byte{'j'}, badMediaSpec...))
		if _, _, ok := decodePinSnapshot(nil, "e1_badmedia", tok); ok {
			t.Fatalf("expected path/query injection in media spec %s to be rejected", badMediaSpec)
		}
	}

	// Decompression bomb (>16KB inflated) must be rejected cleanly.
	bombPayload := `{"i":"oj:b1b717714fc20ded79a7580af314718d","s":"` + strings.Repeat("A", 20000) + `"}`
	compressedBomb, err := compressRawDeflate([]byte(bombPayload))
	if err != nil {
		t.Fatalf("compress bomb fixture: %v", err)
	}
	bombTok := base64.RawURLEncoding.EncodeToString(append([]byte{'z'}, compressedBomb...))
	if _, _, ok := decodePinSnapshot(nil, "e1_bomb", bombTok); ok {
		t.Fatalf("expected oversized decompressed payload to be rejected")
	}

	// Oversized raw token (>4096 chars) must be rejected immediately.
	if _, _, ok := decodePinSnapshot(nil, "e1_huge", strings.Repeat("A", maxDecodeTokenChars+10)); ok {
		t.Fatalf("expected oversized token to be rejected")
	}
}
