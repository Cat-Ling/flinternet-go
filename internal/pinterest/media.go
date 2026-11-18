package pinterest

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
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

type dnsFlight struct {
	done  chan struct{}
	addrs []net.IPAddr
	err   error
}

// MediaFetcher safely proxies images, MP4 videos, and HLS playlists from Pinterest CDNs.
type MediaFetcher struct {
	httpClient                 *http.Client
	userAgent                  string
	maxBytes                   int64
	allowLoopbackMediaForTests bool

	dnsMu       sync.RWMutex
	dnsCache    map[string]cachedDNSAddrs
	dnsInflight map[string]*dnsFlight
}

func (f *MediaFetcher) lookupValidatedIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	now := time.Now()
	f.dnsMu.RLock()
	entry, ok := f.dnsCache[host]
	f.dnsMu.RUnlock()
	if ok && now.Before(entry.expires) && len(entry.addrs) > 0 {
		return entry.addrs, nil
	}

	f.dnsMu.Lock()
	if entry, ok := f.dnsCache[host]; ok && time.Now().Before(entry.expires) && len(entry.addrs) > 0 {
		f.dnsMu.Unlock()
		return entry.addrs, nil
	}
	if f.dnsInflight == nil {
		f.dnsInflight = make(map[string]*dnsFlight)
	}
	if flight, pending := f.dnsInflight[host]; pending {
		f.dnsMu.Unlock()
		select {
		case <-flight.done:
			return flight.addrs, flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	flight := &dnsFlight{done: make(chan struct{})}
	f.dnsInflight[host] = flight
	f.dnsMu.Unlock()

	// Decouple shared lookup from a single caller's cancellation while keeping a bounded timeout.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	cancel()

	if err == nil {
		for _, ipAddr := range ips {
			if IsDisallowedIP(ipAddr.IP) {
				err = fmt.Errorf("blocked connection to non-public IP %s", ipAddr.IP.String())
				ips = nil
				break
			}
		}
	}
	if err == nil && len(ips) > 1 {
		sort.SliceStable(ips, func(i, j int) bool {
			return (ips[i].IP.To4() != nil) && (ips[j].IP.To4() == nil)
		})
	}

	flight.addrs = ips
	flight.err = err

	f.dnsMu.Lock()
	if err == nil && len(ips) > 0 {
		if f.dnsCache == nil {
			f.dnsCache = make(map[string]cachedDNSAddrs)
		}
		f.dnsCache[host] = cachedDNSAddrs{addrs: ips, expires: time.Now().Add(60 * time.Second)}
	}
	delete(f.dnsInflight, host)
	close(flight.done)
	f.dnsMu.Unlock()

	if err != nil {
		return nil, err
	}
	return ips, nil
}

// NewMediaFetcher builds a MediaFetcher with HTTP/2 multiplexing, pooled sockets,
// DNS-rebinding protection, and no redirect following.
func NewMediaFetcher(timeout time.Duration, userAgent string) *MediaFetcher {
	return newMediaFetcherWithTransport(timeout, userAgent, nil)
}

func newMediaFetcherWithTransport(timeout time.Duration, userAgent string, customTransport http.RoundTripper) *MediaFetcher {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	f := &MediaFetcher{
		userAgent:   userAgent,
		maxBytes:    DefaultMaxMediaBytes,
		dnsCache:    make(map[string]cachedDNSAddrs),
		dnsInflight: make(map[string]*dnsFlight),
	}

	transport := customTransport
	if transport == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		transport = &http.Transport{
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          128,
			MaxIdleConnsPerHost:   32,
			MaxConnsPerHost:       64,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
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
				perIPTimeout := min(3*time.Second, timeout)
				for _, ipAddr := range ips {
					attemptCtx := ctx
					var attemptCancel context.CancelFunc
					if len(ips) > 1 {
						attemptCtx, attemptCancel = context.WithTimeout(ctx, perIPTimeout)
					}
					conn, dialErr := dialer.DialContext(attemptCtx, network, net.JoinHostPort(ipAddr.IP.String(), port))
					if attemptCancel != nil {
						attemptCancel()
					}
					if dialErr == nil {
						return conn, nil
					}
					err = dialErr
					if ctx.Err() != nil {
						break
					}
				}
				if err == nil {
					err = errors.New("no resolved addresses available")
				}
				return nil, err
			},
		}
	}

	f.httpClient = &http.Client{
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
	isHLSPath := strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,video/*,audio/*,application/vnd.apple.mpegurl,*/*")
	req.Header.Set("Referer", DefaultBaseURL+"/")
	req.Header.Set("Origin", DefaultBaseURL)
	if rangeHeader != "" && !isHLSPath {
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

	bodyReader := resp.Body
	contentLength := resp.ContentLength
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			resp.Body.Close()
			return nil, newError(CodeProtocolError, fmt.Sprintf("invalid gzip media response: %v", gzErr), resp.StatusCode, rawCT, "", gzErr)
		}
		bodyReader = &gzipReadCloser{gz: gz, body: resp.Body}
		contentLength = -1
	}

	limited := &limitedReadCloser{
		reader: io.LimitReader(bodyReader, f.maxBytes+1),
		closer: bodyReader,
		max:    f.maxBytes,
	}

	return &MediaResponse{
		StatusCode:    resp.StatusCode,
		ContentType:   mediaType,
		ContentLength: contentLength,
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

type gzipReadCloser struct {
	gz   *gzip.Reader
	body io.Closer
}

func (g *gzipReadCloser) Read(p []byte) (int, error) {
	return g.gz.Read(p)
}

func (g *gzipReadCloser) Close() error {
	gzErr := g.gz.Close()
	bodyErr := g.body.Close()
	if gzErr != nil {
		return gzErr
	}
	return bodyErr
}
