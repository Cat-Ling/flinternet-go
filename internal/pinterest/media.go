package pinterest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DefaultMaxMediaBytes = 100 * 1024 * 1024 // 100 MiB

var allowedMediaHosts = map[string]struct{}{
	"i.pinimg.com":  {},
	"v1.pinimg.com": {},
}

var disallowedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT RFC 6598
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF Protocol Assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // Benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("2001:db8::/32"),   // Documentation IPv6
}

// ValidateMediaURL restricts media fetching strictly to HTTPS URLs on port 443
// targeting i.pinimg.com or v1.pinimg.com without embedded credentials.
func ValidateMediaURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, newError(CodeValidationError, "media url is required", 0, "", "", nil)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, newError(CodeValidationError, "invalid media url", 0, "", "", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, newError(CodeValidationError, "media url must use HTTPS", 0, "", "", nil)
	}
	if u.User != nil {
		return nil, newError(CodeValidationError, "media url must not include userinfo", 0, "", "", nil)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if _, ok := allowedMediaHosts[host]; !ok {
		return nil, newError(CodeValidationError, "media url host must be i.pinimg.com or v1.pinimg.com", 0, "", "", nil)
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, newError(CodeValidationError, "media url port must be 443", 0, "", "", nil)
	}
	return u, nil
}

// IsDisallowedIP returns true if ip belongs to loopback, private, link-local,
// multicast, CGNAT, documentation, or unspecified ranges.
func IsDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() || addr.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range disallowedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type cachedDNSAddrs struct {
	addrs   []net.IPAddr
	expires time.Time
}

// MediaFetcher safely proxies images, MP4 videos, and HLS playlists from Pinterest CDNs.
type MediaFetcher struct {
	httpClient                 *http.Client
	userAgent                  string
	maxBytes                   int64
	allowLoopbackMediaForTests bool

	dnsMu    sync.RWMutex
	dnsCache map[string]cachedDNSAddrs
}

func (f *MediaFetcher) lookupValidatedIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	now := time.Now()
	f.dnsMu.RLock()
	entry, ok := f.dnsCache[host]
	f.dnsMu.RUnlock()
	if ok && now.Before(entry.expires) && len(entry.addrs) > 0 {
		return entry.addrs, nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ipAddr := range ips {
		if IsDisallowedIP(ipAddr.IP) {
			return nil, fmt.Errorf("blocked connection to non-public IP %s", ipAddr.IP.String())
		}
	}

	f.dnsMu.Lock()
	f.dnsCache[host] = cachedDNSAddrs{addrs: ips, expires: now.Add(60 * time.Second)}
	f.dnsMu.Unlock()
	return ips, nil
}

// NewMediaFetcher builds a MediaFetcher with HTTP/2 multiplexing, pooled sockets,
// DNS-rebinding protection, and no redirect following.
func NewMediaFetcher(timeout time.Duration, userAgent string) *MediaFetcher {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	f := &MediaFetcher{
		userAgent: userAgent,
		maxBytes:  DefaultMaxMediaBytes,
		dnsCache:  make(map[string]cachedDNSAddrs),
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if f.allowLoopbackMediaForTests {
				return dialer.DialContext(ctx, network, address)
			}
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := f.lookupValidatedIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ipAddr := range ips {
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
				if dialErr == nil {
					return conn, nil
				}
				err = dialErr
			}
			if err == nil {
				err = errors.New("no resolved addresses available")
			}
			return nil, err
		},
	}

	f.httpClient = &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return f
}

// MediaResponse holds a validated media response ready to stream or rewrite.
type MediaResponse struct {
	StatusCode    int
	ContentType   string
	ContentLength int64
	ContentRange  string
	AcceptRanges  string
	Body          io.ReadCloser
	SourceURL     *url.URL
	IsHLSManifest bool
}

func isAllowedMediaContentType(mediaType string) bool {
	if mediaType == "" {
		return true
	}
	if strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "audio/") {
		return true
	}
	switch mediaType {
	case "application/vnd.apple.mpegurl",
		"application/x-mpegurl",
		"application/mp4",
		"application/octet-stream",
		"binary/octet-stream":
		return true
	default:
		return false
	}
}

func isHLSResponse(u *url.URL, mediaType string) bool {
	if mediaType == "application/vnd.apple.mpegurl" || mediaType == "application/x-mpegurl" {
		return true
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// Fetch opens an upstream Pinterest CDN media stream without cookies or auth headers.
func (f *MediaFetcher) Fetch(ctx context.Context, rawURL, rangeHeader string) (*MediaResponse, error) {
	u, err := ValidateMediaURL(rawURL)
	if err != nil && !f.allowLoopbackMediaForTests {
		return nil, err
	}
	if err != nil && f.allowLoopbackMediaForTests {
		u, err = url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, newError(CodeProtocolError, "failed to create media request", 0, "", "", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,video/*,audio/*,application/vnd.apple.mpegurl,*/*")
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, newError(CodeHTTPError, fmt.Sprintf("media fetch failed: %v", err), 0, "", "", err)
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, newError(CodeHTTPError, "media redirect refused; resolve allowlisted URL explicitly", resp.StatusCode, resp.Header.Get("Content-Type"), "", nil)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, newError(CodeHTTPError, fmt.Sprintf("media CDN returned HTTP %d", resp.StatusCode), resp.StatusCode, resp.Header.Get("Content-Type"), "", nil)
	}

	rawCT := resp.Header.Get("Content-Type")
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(rawCT, ";", 2)[0]))
	if !isAllowedMediaContentType(mediaType) {
		resp.Body.Close()
		return nil, newError(CodeProtocolError, fmt.Sprintf("unexpected media content type %q", mediaType), resp.StatusCode, rawCT, "", nil)
	}
	if resp.ContentLength > f.maxBytes {
		resp.Body.Close()
		return nil, newError(CodeProtocolError, fmt.Sprintf("media exceeded %d-byte limit", f.maxBytes), resp.StatusCode, rawCT, "", nil)
	}

	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	limited := &limitedReadCloser{
		reader: io.LimitReader(resp.Body, f.maxBytes+1),
		closer: resp.Body,
		max:    f.maxBytes,
	}

	return &MediaResponse{
		StatusCode:    resp.StatusCode,
		ContentType:   mediaType,
		ContentLength: resp.ContentLength,
		ContentRange:  resp.Header.Get("Content-Range"),
		AcceptRanges:  resp.Header.Get("Accept-Ranges"),
		Body:          limited,
		SourceURL:     u,
		IsHLSManifest: isHLSResponse(u, mediaType),
	}, nil
}

// RewriteHLSManifest rewrites relative and absolute CDN URLs inside an .m3u8 manifest
// so segments and sub-playlists continue flowing safely through the local proxy.
func RewriteHLSManifest(manifest string, baseURL *url.URL, proxyPrefix string) string {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(manifest))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if strings.Contains(line, `URI="`) {
				line = rewriteManifestURIAttribute(line, baseURL, proxyPrefix)
			}
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if resolved, ok := resolveProxyTarget(trimmed, baseURL, proxyPrefix); ok {
			out.WriteString(resolved)
		} else {
			out.WriteString("# blocked non-allowlisted segment URI")
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func rewriteManifestURIAttribute(line string, baseURL *url.URL, proxyPrefix string) string {
	const marker = `URI="`
	idx := strings.Index(line, marker)
	if idx < 0 {
		return line
	}
	start := idx + len(marker)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return line
	}
	rawURI := line[start : start+end]
	resolved, ok := resolveProxyTarget(rawURI, baseURL, proxyPrefix)
	if !ok {
		return line
	}
	return line[:start] + resolved + line[start+end:]
}

func resolveProxyTarget(rawRef string, baseURL *url.URL, proxyPrefix string) (string, bool) {
	ref, err := url.Parse(rawRef)
	if err != nil {
		return "", false
	}
	abs := baseURL.ResolveReference(ref)
	if _, err := ValidateMediaURL(abs.String()); err != nil {
		return "", false
	}
	return proxyPrefix + url.QueryEscape(abs.String()), true
}

type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
	read   int64
	max    int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.reader.Read(p)
	l.read += int64(n)
	if l.read > l.max {
		return 0, newError(CodeProtocolError, fmt.Sprintf("media exceeded %d-byte limit", l.max), 0, "", "", nil)
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.closer.Close()
}
