package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/flinternet/flinternet/internal/pinterest"
)

const (
	sourceSearch      = "s"
	sourceBoard       = "b"
	sourceUserPins    = "up"
	sourceUserCreated = "uc"
	sourceIdeas       = "i"
)

type pinSource struct {
	Kind     string `json:"k"`
	Target   string `json:"t"`
	Sub      string `json:"u,omitempty"`
	Bookmark string `json:"b,omitempty"`
	AggID    string `json:"a,omitempty"`
	MediaKey string `json:"m,omitempty"`
}

func (s pinSource) cacheKey() string {
	return s.Kind + "|" + s.Target + "|" + s.Sub + "|" + s.Bookmark
}

// shareCodec encrypts and authenticates pin source locators using AES-256-GCM
// bound to the pin ID as associated data so tokens cannot be forged, decoded,
// or transplanted across pins without knowing the server's share key.
type shareCodec struct {
	aead cipher.AEAD
}

func newShareCodec(secret string) (*shareCodec, error) {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return nil, nil
	}
	key := sha256.Sum256([]byte("flinternet:share:v1:" + trimmed))
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

func (c *shareCodec) seal(pinID string, src pinSource) (string, error) {
	if c == nil || pinID == "" || src.Kind == "" || src.Target == "" {
		return "", nil
	}
	raw, err := json.Marshal(src)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, raw, []byte(pinID))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *shareCodec) open(pinID, token string) (pinSource, bool) {
	if c == nil || pinID == "" || token == "" {
		return pinSource{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) <= c.aead.NonceSize() {
		return pinSource{}, false
	}
	nonceSize := c.aead.NonceSize()
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, []byte(pinID))
	if err != nil {
		return pinSource{}, false
	}
	var src pinSource
	if err := json.Unmarshal(plain, &src); err != nil || src.Kind == "" || src.Target == "" {
		return pinSource{}, false
	}
	return src, true
}

func extractMediaKey(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		base = base[:dot]
	}
	base = strings.TrimSuffix(base, "_mobile")
	return strings.ToLower(base)
}

func pinMediaKey(p pinterest.Pin) string {
	return extractMediaKey(firstNonEmptyQuery(p.FallbackImageURL, p.ImageURL, p.VideoURL))
}

// matchCorrelatedPin resolves a pin inside its source page, falling back from
// exact pin ID to content-addressed media + aggregated ID fingerprints if
// Pinterest rotates ephemeral e1_ tokens between sessions.
func matchCorrelatedPin(pins []pinterest.Pin, pinID string, src pinSource) (pinterest.Pin, bool) {
	for _, p := range pins {
		if p.ID == pinID {
			return p, true
		}
	}
	if src.AggID != "" && src.MediaKey != "" {
		for _, p := range pins {
			if p.AggregatedPinID == src.AggID && pinMediaKey(p) == src.MediaKey {
				return p, true
			}
		}
	}
	if src.MediaKey != "" {
		var matched pinterest.Pin
		hits := 0
		for _, p := range pins {
			if pinMediaKey(p) == src.MediaKey {
				matched = p
				hits++
			}
		}
		if hits == 1 {
			return matched, true
		}
	}
	if src.AggID != "" {
		for _, p := range pins {
			if p.AggregatedPinID == src.AggID {
				return p, true
			}
		}
	}
	return pinterest.Pin{}, false
}

type flightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

type singleflightGroup[T any] struct {
	mu    sync.Mutex
	calls map[string]*flightCall[T]
}

func (g *singleflightGroup[T]) do(key string, fn func() (T, error)) (T, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*flightCall[T])
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.val, c.err
	}
	c := &flightCall[T]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	close(c.done)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return c.val, c.err
}
