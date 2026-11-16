package pinterest

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AnonymousCookieJar starts empty and accepts only a naturally emitted
// csrftoken cookie from Pinterest HTTPS hosts. Identity cookies such as _auth
// and _pinterest_sess are always rejected.
type AnonymousCookieJar struct {
	mu              sync.RWMutex
	tokens          map[string]storedCookie
	testAllowedHost string
}

type storedCookie struct {
	cookie  *http.Cookie
	expires time.Time
}

// NewAnonymousCookieJar constructs an empty anonymous cookie jar.
func NewAnonymousCookieJar() *AnonymousCookieJar {
	return &AnonymousCookieJar{
		tokens: make(map[string]storedCookie),
	}
}

func isPinterestHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "pinterest.com" || strings.HasSuffix(h, ".pinterest.com")
}

func (j *AnonymousCookieJar) isAllowedURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if j.testAllowedHost != "" && host == j.testAllowedHost {
		return true
	}
	return strings.EqualFold(u.Scheme, "https") && isPinterestHost(host)
}

func (j *AnonymousCookieJar) isAllowedCookieDomain(cookieDomain, reqHost string) bool {
	if cookieDomain == "" {
		return true
	}
	domain := strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
	if j.testAllowedHost != "" && domain == j.testAllowedHost {
		return true
	}
	if !isPinterestHost(domain) {
		return false
	}
	reqHost = strings.ToLower(reqHost)
	return reqHost == domain || strings.HasSuffix(reqHost, "."+domain)
}

// SetCookies stores only non-empty csrftoken cookies for allowed Pinterest HTTPS origins.
func (j *AnonymousCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if !j.isAllowedURL(u) {
		return
	}
	reqHost := strings.ToLower(u.Hostname())
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()

	for _, c := range cookies {
		if c == nil || !strings.EqualFold(c.Name, "csrftoken") {
			continue
		}
		if !j.isAllowedCookieDomain(c.Domain, reqHost) {
			continue
		}
		domainKey := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		if domainKey == "" {
			domainKey = reqHost
		}
		if c.MaxAge < 0 || (!c.Expires.IsZero() && !c.Expires.After(now)) {
			delete(j.tokens, domainKey)
			continue
		}
		var exp time.Time
		if c.MaxAge > 0 {
			exp = now.Add(time.Duration(c.MaxAge) * time.Second)
		} else if !c.Expires.IsZero() {
			exp = c.Expires
		}
		copied := *c
		j.tokens[domainKey] = storedCookie{cookie: &copied, expires: exp}
	}
}

// Cookies returns only the active csrftoken cookie if u is an allowed Pinterest HTTPS origin.
func (j *AnonymousCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if !j.isAllowedURL(u) {
		return nil
	}
	reqHost := strings.ToLower(u.Hostname())
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()

	var result []*http.Cookie
	for domainKey, entry := range j.tokens {
		if !entry.expires.IsZero() && !now.Before(entry.expires) {
			delete(j.tokens, domainKey)
			continue
		}
		if reqHost == domainKey || strings.HasSuffix(reqHost, "."+domainKey) {
			copied := *entry.cookie
			result = append(result, &copied)
		}
	}
	return result
}

// CSRFToken returns the current anonymous csrftoken value for u, if any.
func (j *AnonymousCookieJar) CSRFToken(u *url.URL) string {
	for _, c := range j.Cookies(u) {
		if strings.EqualFold(c.Name, "csrftoken") && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// Clear removes all stored cookies from the jar.
func (j *AnonymousCookieJar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	clear(j.tokens)
}
