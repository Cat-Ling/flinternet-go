package server

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/flinternet/flinternet/internal/pinterest"
)

const (
	maxSnapshotTokenChars    = 1500
	maxDecodeTokenChars      = 4096
	maxDecompressedJSONBytes = 16 * 1024
)

// pinSnapshot holds compact, self-contained persistent metadata and media
// descriptors for e1_ pins so links survive server restarts without upstream
// search correlation or exposing browsing history.
type pinSnapshot struct {
	Img      string   `json:"i,omitempty"`
	Fallback string   `json:"f,omitempty"`
	HLS      string   `json:"v,omitempty"`
	MP4      string   `json:"m,omitempty"`
	Width    int      `json:"w,omitempty"`
	Height   int      `json:"h,omitempty"`
	Duration float64  `json:"d,omitempty"`
	Color    string   `json:"c,omitempty"`
	Title    string   `json:"t,omitempty"`
	Desc     string   `json:"s,omitempty"`
	AggID    string   `json:"a,omitempty"`
	PUser    string   `json:"pu,omitempty"`
	PName    string   `json:"pn,omitempty"`
	PAvatar  string   `json:"pa,omitempty"`
	BUser    string   `json:"bu,omitempty"`
	BSlug    string   `json:"bs,omitempty"`
	BName    string   `json:"bn,omitempty"`
	Domain   string   `json:"dm,omitempty"`
	Link     string   `json:"ln,omitempty"`
	Likes    int      `json:"lk,omitempty"`
	Saves    int      `json:"sv,omitempty"`
	Repins   int      `json:"rp,omitempty"`
	Comments int      `json:"cc,omitempty"`
	Tags     []string `json:"tg,omitempty"`
}

// shareCodec optionally encrypts self-contained pin snapshots using AES-256-GCM
// bound to the pin ID as associated data.
type shareCodec struct {
	aead cipher.AEAD
}

func newShareCodec(secret string) (*shareCodec, error) {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return nil, nil
	}
	key := sha256.Sum256([]byte("flinternet:share:v2:" + trimmed))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &shareCodec{aead: aead}, nil
}

var imageTierToCode = map[string]string{
	"originals":  "o",
	"736x":       "7",
	"564x":       "5",
	"474x":       "4",
	"236x":       "2",
	"280x280_RS": "r2",
	"140x140_RS": "r1",
	"75x75_RS":   "r7",
}

var codeToImageTier = func() map[string]string {
	m := make(map[string]string, len(imageTierToCode))
	for k, v := range imageTierToCode {
		m[v] = k
	}
	return m
}()

var extToCode = map[string]string{
	".jpg":  "j",
	".jpeg": "j",
	".png":  "p",
	".webp": "w",
	".gif":  "g",
	".heic": "h",
}

var codeToExt = map[string]string{
	"j": ".jpg",
	"p": ".png",
	"w": ".webp",
	"g": ".gif",
	"h": ".heic",
}

func isLowerHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func shardedCDNPath(hex32 string) string {
	return hex32[0:2] + "/" + hex32[2:4] + "/" + hex32[4:6] + "/" + hex32
}

func encodeImageSpec(rawURL string) string {
	const prefix = "https://i.pinimg.com/"
	if !strings.HasPrefix(rawURL, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(rawURL, prefix)
	if qIdx := strings.IndexByte(rest, '?'); qIdx >= 0 {
		rest = rest[:qIdx]
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 5 {
		tierCode, okTier := imageTierToCode[parts[0]]
		file := parts[4]
		if dot := strings.LastIndexByte(file, '.'); okTier && dot == 32 {
			hexID := strings.ToLower(file[:32])
			extCode, okExt := extToCode[strings.ToLower(file[32:])]
			if okExt && isLowerHex32(hexID) &&
				parts[1] == hexID[0:2] && parts[2] == hexID[2:4] && parts[3] == hexID[4:6] {
				return tierCode + extCode + ":" + hexID
			}
		}
	}
	return "r:" + rest
}

func decodeCleanCDNRest(hostPrefix, specPrefix, spec string) string {
	raw := strings.TrimSpace(strings.TrimPrefix(spec, specPrefix))
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, "\\?#\r\n\t ") {
		return ""
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, "..") {
		return ""
	}
	cleaned := path.Clean("/" + raw)
	if cleaned == "/" || strings.TrimPrefix(cleaned, "/") != raw {
		return ""
	}
	candidate := hostPrefix + raw
	u, err := pinterest.ValidateMediaURL(candidate)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return u.String()
}

func decodeImageSpec(spec string) string {
	if spec == "" {
		return ""
	}
	if strings.HasPrefix(spec, "r:") {
		return decodeCleanCDNRest("https://i.pinimg.com/", "r:", spec)
	}
	kind, hexID, ok := strings.Cut(spec, ":")
	if !ok || len(kind) < 2 || !isLowerHex32(hexID) {
		return ""
	}
	tierCode := kind[:len(kind)-1]
	extCode := kind[len(kind)-1:]
	tier, okTier := codeToImageTier[tierCode]
	ext, okExt := codeToExt[extCode]
	if !okTier || !okExt {
		return ""
	}
	candidate := "https://i.pinimg.com/" + tier + "/" + shardedCDNPath(hexID) + ext
	if _, err := pinterest.ValidateMediaURL(candidate); err == nil {
		return candidate
	}
	return ""
}

func encodeVideoSpec(rawURL string) string {
	const prefix = "https://v1.pinimg.com/"
	if !strings.HasPrefix(rawURL, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(rawURL, prefix)
	if qIdx := strings.IndexByte(rest, '?'); qIdx >= 0 {
		rest = rest[:qIdx]
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 7 && parts[0] == "videos" && parts[2] == "hls" {
		kindCode := ""
		suffix := ".m3u8"
		switch parts[1] {
		case "iht":
			kindCode = "ih"
		case "mc":
			kindCode = "mh"
		case "v2":
			kindCode = "vm"
			suffix = "_mobile.m3u8"
		}
		if kindCode != "" && strings.HasSuffix(parts[6], suffix) {
			hexID := strings.ToLower(strings.TrimSuffix(parts[6], suffix))
			if isLowerHex32(hexID) && parts[3] == hexID[0:2] && parts[4] == hexID[2:4] && parts[5] == hexID[4:6] {
				return kindCode + ":" + hexID
			}
		}
	}
	return "v:" + rest
}

func decodeVideoSpec(spec string) string {
	if spec == "" {
		return ""
	}
	if strings.HasPrefix(spec, "v:") {
		return decodeCleanCDNRest("https://v1.pinimg.com/", "v:", spec)
	}
	kind, hexID, ok := strings.Cut(spec, ":")
	if !ok || !isLowerHex32(hexID) {
		return ""
	}
	var pathTail string
	switch kind {
	case "ih":
		pathTail = "videos/iht/hls/" + shardedCDNPath(hexID) + ".m3u8"
	case "mh":
		pathTail = "videos/mc/hls/" + shardedCDNPath(hexID) + ".m3u8"
	case "vm":
		pathTail = "videos/v2/hls/" + shardedCDNPath(hexID) + "_mobile.m3u8"
	default:
		return ""
	}
	candidate := "https://v1.pinimg.com/" + pathTail
	if _, err := pinterest.ValidateMediaURL(candidate); err == nil {
		return candidate
	}
	return ""
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

func snapshotFromPin(p pinterest.Pin) pinSnapshot {
	var tags []string
	if len(p.Hashtags) > 0 {
		tags = append(tags, p.Hashtags...)
	} else if len(p.VisualTags) > 0 {
		limit := min(5, len(p.VisualTags))
		tags = append(tags, p.VisualTags[:limit]...)
	}

	snap := pinSnapshot{
		Img:      encodeImageSpec(p.ImageURL),
		HLS:      encodeVideoSpec(p.HLSVideoURL),
		MP4:      encodeVideoSpec(p.MP4VideoURL),
		Width:    p.Width,
		Height:   p.Height,
		Duration: p.Duration,
		Color:    p.DominantColor,
		Title:    firstNonEmptyQuery(p.Title, p.RichTitle, p.SEOTitle),
		Desc:     firstNonEmptyQuery(p.Description, p.RichDescription),
		AggID:    p.AggregatedPinID,
		PUser:    p.Pinner.Username,
		PName:    p.Pinner.FullName,
		PAvatar:  encodeImageSpec(p.Pinner.AvatarURL),
		BUser:    p.Board.Username,
		BSlug:    p.Board.Slug,
		BName:    p.Board.Name,
		Domain:   p.Domain,
		Link:     p.Link,
		Likes:    p.Likes,
		Saves:    p.Saves,
		Repins:   p.Repins,
		Comments: p.CommentCount,
		Tags:     tags,
	}
	if p.FallbackImageURL != "" && p.FallbackImageURL != p.ImageURL {
		snap.Fallback = encodeImageSpec(p.FallbackImageURL)
	}
	return snap
}

func compressRawDeflate(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressRawDeflate(data []byte, limit int64) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, errors.New("decompressed snapshot exceeds size limit")
	}
	return out, nil
}

func encodeSnapshotBytes(codec *shareCodec, pinID string, snap pinSnapshot) (string, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	compressed, err := compressRawDeflate(raw)
	if err != nil {
		return "", err
	}

	var payload []byte
	if codec != nil {
		nonce := make([]byte, codec.aead.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return "", err
		}
		sealed := codec.aead.Seal(nonce, nonce, compressed, []byte(pinID))
		payload = append([]byte{'e'}, sealed...)
	} else if len(compressed) < len(raw) {
		payload = append([]byte{'z'}, compressed...)
	} else {
		payload = append([]byte{'j'}, raw...)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func encodeBoundedPinToken(codec *shareCodec, p pinterest.Pin) (string, error) {
	snap := snapshotFromPin(p)
	variants := []func(*pinSnapshot){
		func(_ *pinSnapshot) {},
		func(s *pinSnapshot) { s.Tags = nil },
		func(s *pinSnapshot) { s.Desc = truncateRunes(s.Desc, 160) },
		func(s *pinSnapshot) {
			s.Desc = ""
			s.PAvatar = ""
			s.Title = truncateRunes(s.Title, 140)
		},
		func(s *pinSnapshot) {
			s.Link = ""
			s.BName = ""
			s.Title = truncateRunes(s.Title, 100)
		},
	}
	for i, trim := range variants {
		trim(&snap)
		tok, err := encodeSnapshotBytes(codec, p.ID, snap)
		if err != nil {
			return "", err
		}
		if len(tok) <= maxSnapshotTokenChars || i == len(variants)-1 {
			return tok, nil
		}
	}
	return "", nil
}

func sanitizeText(s string, maxRunes int, allowNewlines bool) string {
	if s == "" || !utf8.ValidString(s) {
		return ""
	}
	var b strings.Builder
	b.Grow(min(len(s), maxRunes*2))
	count := 0
	for _, r := range s {
		if r == 0 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		if r < 0x20 {
			if allowNewlines && r == '\n' {
				b.WriteRune('\n')
				count++
			} else if r == '\t' {
				b.WriteByte(' ')
				count++
			}
			continue
		}
		b.WriteRune(r)
		count++
		if maxRunes > 0 && count >= maxRunes {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func sanitizePathSegment(s string, maxLen int) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || len(trimmed) > maxLen || trimmed == "." || trimmed == ".." {
		return ""
	}
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '-' || c == '.') {
			return ""
		}
	}
	return trimmed
}

func sanitizeDigits(s string, maxLen int) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || len(trimmed) > maxLen {
		return ""
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return ""
		}
	}
	return trimmed
}

func sanitizeHexColor(s string) string {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) != 4 && len(trimmed) != 7 {
		return ""
	}
	if trimmed[0] != '#' {
		return ""
	}
	for i := 1; i < len(trimmed); i++ {
		c := trimmed[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return ""
		}
	}
	return strings.ToLower(trimmed)
}

func sanitizeExternalURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 512 {
		return ""
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] <= 0x20 || trimmed[i] == 0x7f {
			return ""
		}
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "https" && scheme != "http") || u.User != nil || u.Hostname() == "" {
		return ""
	}
	u.Scheme = scheme
	return u.String()
}

func sanitizeDomain(s string) string {
	trimmed := sanitizeText(s, 100, false)
	if trimmed == "" || strings.ContainsAny(trimmed, "/\\?#:<>\"' ") {
		return ""
	}
	return trimmed
}

func sanitizeDim(v int) int {
	if v <= 0 || v > 20000 {
		return 0
	}
	return v
}

func sanitizeCount(v int) int {
	if v <= 0 || v > 1_000_000_000 {
		return 0
	}
	return v
}

func sanitizeDuration(d float64) float64 {
	if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 || d > 86400 {
		return 0
	}
	return d
}

func isSafePinID(pinID string) bool {
	if pinID == "" || len(pinID) > 160 || strings.ContainsAny(pinID, "/\\?#<>\"' \t\r\n") {
		return false
	}
	for i := 0; i < len(pinID); i++ {
		if pinID[i] < 0x20 || pinID[i] >= 0x7f {
			return false
		}
	}
	return true
}

// decodePinSnapshot decodes and strictly sanitizes a ?p= token.
// authenticated is true only when verified via AES-256-GCM with ShareKey.
// Plain compressed/uncompressed keyless tokens remain accepted even when ShareKey
// is later enabled so previously shared links keep working without cache poisoning.
func decodePinSnapshot(codec *shareCodec, pinID, token string) (pin pinterest.Pin, authenticated bool, ok bool) {
	trimmedToken := strings.TrimSpace(token)
	if !isSafePinID(pinID) || trimmedToken == "" || len(trimmedToken) > maxDecodeTokenChars {
		return pinterest.Pin{}, false, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(trimmedToken)
	if err != nil || len(decoded) < 2 {
		return pinterest.Pin{}, false, false
	}
	mode := decoded[0]
	body := decoded[1:]

	var jsonBytes []byte
	switch mode {
	case 'e':
		if codec == nil || len(body) <= codec.aead.NonceSize() {
			return pinterest.Pin{}, false, false
		}
		ns := codec.aead.NonceSize()
		compressed, err := codec.aead.Open(nil, body[:ns], body[ns:], []byte(pinID))
		if err != nil {
			return pinterest.Pin{}, false, false
		}
		jsonBytes, err = decompressRawDeflate(compressed, maxDecompressedJSONBytes)
		if err != nil {
			return pinterest.Pin{}, false, false
		}
		authenticated = true
	case 'z':
		jsonBytes, err = decompressRawDeflate(body, maxDecompressedJSONBytes)
		if err != nil {
			return pinterest.Pin{}, false, false
		}
	case 'j':
		if len(body) > maxDecompressedJSONBytes {
			return pinterest.Pin{}, false, false
		}
		jsonBytes = body
	default:
		return pinterest.Pin{}, false, false
	}

	if !utf8.Valid(jsonBytes) {
		return pinterest.Pin{}, false, false
	}
	var snap pinSnapshot
	if err := json.Unmarshal(jsonBytes, &snap); err != nil {
		return pinterest.Pin{}, false, false
	}

	imgURL := decodeImageSpec(snap.Img)
	fbURL := decodeImageSpec(snap.Fallback)
	if fbURL == "" {
		fbURL = imgURL
	}
	hlsURL := decodeVideoSpec(snap.HLS)
	mp4URL := decodeVideoSpec(snap.MP4)
	videoURL := firstNonEmptyQuery(hlsURL, mp4URL)
	if imgURL == "" && videoURL == "" {
		return pinterest.Pin{}, false, false
	}

	width := sanitizeDim(snap.Width)
	height := sanitizeDim(snap.Height)
	if width == 0 || height == 0 {
		width = 0
		height = 0
	}
	duration := sanitizeDuration(snap.Duration)
	color := sanitizeHexColor(snap.Color)
	title := sanitizeText(snap.Title, 200, false)
	desc := sanitizeText(snap.Desc, 1000, true)
	aggID := sanitizeDigits(snap.AggID, 32)
	pUser := sanitizePathSegment(snap.PUser, 100)
	pName := sanitizeText(snap.PName, 120, false)
	bUser := sanitizePathSegment(snap.BUser, 100)
	bSlug := sanitizePathSegment(snap.BSlug, 100)
	bName := sanitizeText(snap.BName, 120, false)
	domain := sanitizeDomain(snap.Domain)
	safeLink := sanitizeExternalURL(snap.Link)
	avatarURL := decodeImageSpec(snap.PAvatar)

	var tags []string
	if len(snap.Tags) > 0 {
		limit := min(10, len(snap.Tags))
		for _, rawTag := range snap.Tags[:limit] {
			if cleanTag := sanitizeText(rawTag, 60, false); cleanTag != "" {
				tags = append(tags, cleanTag)
			}
		}
	}

	images := make(map[string]pinterest.ImageVariant)
	if imgURL != "" {
		images["orig"] = pinterest.ImageVariant{
			URL:      imgURL,
			ProxyURL: pinterest.ProxyPath(imgURL),
			Width:    width,
			Height:   height,
		}
	}
	videos := make(map[string]pinterest.VideoVariant)
	if hlsURL != "" {
		videos["V_HLSV4"] = pinterest.VideoVariant{
			Key:       "V_HLSV4",
			URL:       hlsURL,
			ProxyURL:  pinterest.ProxyPath(hlsURL),
			Width:     width,
			Height:    height,
			Duration:  duration,
			Thumbnail: fbURL,
		}
	}
	if mp4URL != "" {
		videos["V_720P"] = pinterest.VideoVariant{
			Key:      "V_720P",
			URL:      mp4URL,
			ProxyURL: pinterest.ProxyPath(mp4URL),
			Width:    width,
			Height:   height,
			Duration: duration,
		}
	}

	var boardURL string
	if bUser != "" && bSlug != "" {
		boardURL = "/" + bUser + "/" + bSlug + "/"
	} else {
		bUser = ""
		bSlug = ""
	}

	return pinterest.Pin{
		ID:                    pinID,
		AggregatedPinID:       aggID,
		Type:                  "pin",
		Title:                 title,
		Description:           desc,
		Domain:                domain,
		Link:                  safeLink,
		DominantColor:         color,
		VisualTags:            tags,
		Likes:                 sanitizeCount(snap.Likes),
		Saves:                 sanitizeCount(snap.Saves),
		Repins:                sanitizeCount(snap.Repins),
		CommentCount:          sanitizeCount(snap.Comments),
		Images:                images,
		ImageURL:              imgURL,
		ProxyImageURL:         pinterest.ProxyPath(imgURL),
		FallbackImageURL:      fbURL,
		ProxyFallbackImageURL: pinterest.ProxyPath(fbURL),
		Width:                 width,
		Height:                height,
		IsVideo:               videoURL != "",
		Duration:              duration,
		Videos:                videos,
		VideoURL:              videoURL,
		ProxyVideoURL:         pinterest.ProxyPath(videoURL),
		HLSVideoURL:           hlsURL,
		ProxyHLSVideoURL:      pinterest.ProxyPath(hlsURL),
		MP4VideoURL:           mp4URL,
		ProxyMP4VideoURL:      pinterest.ProxyPath(mp4URL),
		Pinner: pinterest.OwnerSummary{
			Username:       pUser,
			FullName:       pName,
			AvatarURL:      avatarURL,
			ProxyAvatarURL: pinterest.ProxyPath(avatarURL),
		},
		Board: pinterest.BoardSummary{
			Name:     bName,
			URL:      boardURL,
			Username: bUser,
			Slug:     bSlug,
		},
	}, authenticated, true
}
