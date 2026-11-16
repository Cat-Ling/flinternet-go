package pinterest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://www.pinterest.com"
	DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	DefaultTimeout   = 20 * time.Second
	maxResourceBody  = 16 * 1024 * 1024
)

// Config configures a guest-only Pinterest client.
type Config struct {
	BaseURL                   string
	UserAgent                 string
	AppVersion                string
	Timeout                   time.Duration
	AllowUnverifiedPagination bool
	HTTPClient                *http.Client

	// allowInsecureForTests permits http://127.0.0.1 or localhost inside package unit tests.
	allowInsecureForTests bool
}

// Client performs read-only unauthenticated requests against Pinterest's web-resource API.
type Client struct {
	baseURL                   *url.URL
	baseURLStr                string
	userAgent                 string
	appVersion                string
	timeout                   time.Duration
	allowUnverifiedPagination bool
	jar                       *AnonymousCookieJar
	httpClient                *http.Client
	mediaFetcher              *MediaFetcher
}

// NewClient initializes an isolated, cookie-restricted Pinterest guest client.
func NewClient(cfg Config) (*Client, error) {
	baseStr := strings.TrimSpace(cfg.BaseURL)
	if baseStr == "" {
		baseStr = DefaultBaseURL
	}
	baseStr = strings.TrimRight(baseStr, "/")

	parsed, err := url.Parse(baseStr)
	if err != nil {
		return nil, newError(CodeValidationError, "invalid base_url", 0, "", "", err)
	}
	if !cfg.allowInsecureForTests {
		if !strings.EqualFold(parsed.Scheme, "https") || !isPinterestHost(parsed.Hostname()) {
			return nil, newError(CodeValidationError, "base_url must be an HTTPS pinterest.com host", 0, "", "", nil)
		}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = DefaultUserAgent
	}

	jar := NewAnonymousCookieJar()
	if cfg.allowInsecureForTests {
		jar.testAllowedHost = strings.ToLower(parsed.Hostname())
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: timeout,
		}
	}
	// Never inherit caller cookie jars or cross-site session state.
	clonedClient := *httpClient
	clonedClient.Jar = jar
	if clonedClient.Timeout <= 0 {
		clonedClient.Timeout = timeout
	}
	if clonedClient.CheckRedirect == nil {
		clonedClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if !cfg.allowInsecureForTests && (!strings.EqualFold(req.URL.Scheme, "https") || !isPinterestHost(req.URL.Hostname())) {
				return errors.New("refusing redirect outside pinterest.com")
			}
			return nil
		}
	}

	return &Client{
		baseURL:                   parsed,
		baseURLStr:                baseStr,
		userAgent:                 ua,
		appVersion:                strings.TrimSpace(cfg.AppVersion),
		timeout:                   timeout,
		allowUnverifiedPagination: cfg.AllowUnverifiedPagination,
		jar:                       jar,
		httpClient:                &clonedClient,
		mediaFetcher:              NewMediaFetcher(timeout, ua),
	}, nil
}

// BaseURL returns the normalized Pinterest web host URL.
func (c *Client) BaseURL() string {
	return c.baseURLStr
}

// AllowUnverifiedPagination reports whether unverified guest continuations are enabled.
func (c *Client) AllowUnverifiedPagination() bool {
	return c.allowUnverifiedPagination
}

// Media returns the SSRF-hardened Pinterest CDN media fetcher.
func (c *Client) Media() *MediaFetcher {
	return c.mediaFetcher
}

type resourcePayload[T any] struct {
	Options T        `json:"options"`
	Context struct{} `json:"context"`
}

type requestOpts struct {
	sourceURL string
	method    string
	handler   string
	referer   string
}

func validateSourcePath(sourceURL string) (string, error) {
	if sourceURL == "" || !strings.HasPrefix(sourceURL, "/") || strings.HasPrefix(sourceURL, "//") {
		return "", newError(CodeValidationError, "source_url must be a same-site absolute path beginning with a single slash", 0, "", "", nil)
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "", newError(CodeValidationError, "external source_url is not allowed", 0, "", "", err)
	}
	return sourceURL, nil
}

func validateResourceName(resource string) error {
	if !strings.HasSuffix(resource, "Resource") {
		return newError(CodeValidationError, "invalid resource name", 0, "", "", nil)
	}
	prefix := strings.TrimSuffix(resource, "Resource")
	if prefix == "" {
		return newError(CodeValidationError, "invalid resource name", 0, "", "", nil)
	}
	for _, r := range prefix {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return newError(CodeValidationError, "invalid resource name", 0, "", "", nil)
		}
	}
	return nil
}

func encodeResourceData[T any](options T) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(resourcePayload[T]{Options: options}); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func doResourceRequest[T any](ctx context.Context, c *Client, resource string, options T, opts requestOpts) (*resourceEnvelope, json.RawMessage, error) {
	if err := validateResourceName(resource); err != nil {
		return nil, nil, err
	}
	path, err := validateSourcePath(opts.sourceURL)
	if err != nil {
		return nil, nil, err
	}

	dataJSON, err := encodeResourceData(options)
	if err != nil {
		return nil, nil, newError(CodeProtocolError, "failed to encode resource request options", 0, "", "", err)
	}

	method := strings.ToUpper(strings.TrimSpace(opts.method))
	if method == "" {
		method = http.MethodGet
	}

	endpoint := c.baseURLStr + "/resource/" + resource + "/get/"
	var req *http.Request
	switch method {
	case http.MethodGet:
		q := url.Values{}
		q.Set("source_url", path)
		q.Set("data", dataJSON)
		q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	case http.MethodPost:
		form := url.Values{}
		form.Set("source_url", path)
		form.Set("data", dataJSON)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	default:
		return nil, nil, newError(CodeValidationError, fmt.Sprintf("unsupported HTTP method %q", method), 0, "", "", nil)
	}
	if err != nil {
		return nil, nil, newError(CodeProtocolError, "failed to build request", 0, "", "", err)
	}

	referer := opts.referer
	if referer == "" {
		referer = c.baseURLStr + path
	}
	req.Header.Set("Accept", "application/json, text/javascript, */*, q=0.01")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("DNT", "1")
	req.Header.Set("Referer", referer)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Pinterest-AppState", "active")
	req.Header.Set("X-Pinterest-Source-Url", path)
	if opts.handler != "" {
		req.Header.Set("X-Pinterest-Pws-Handler", opts.handler)
	}
	if c.appVersion != "" {
		req.Header.Set("X-App-Version", c.appVersion)
	}
	if csrf := c.jar.CSRFToken(c.baseURL); csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, newError(CodeHTTPError, fmt.Sprintf("network error calling %s: %v", resource, err), 0, "", "", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResourceBody+1))
	if err != nil {
		return nil, nil, newError(CodeProtocolError, fmt.Sprintf("failed reading %s response body", resource), resp.StatusCode, resp.Header.Get("Content-Type"), "", err)
	}
	if len(bodyBytes) > maxResourceBody {
		return nil, nil, newError(CodeProtocolError, "resource response exceeded maximum size limit", resp.StatusCode, resp.Header.Get("Content-Type"), "", nil)
	}

	ctype := resp.Header.Get("Content-Type")
	retryAfter := resp.Header.Get("Retry-After")

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, newError(CodeRateLimited, "Pinterest returned HTTP 429; respect Retry-After and stop", http.StatusTooManyRequests, ctype, retryAfter, nil)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, nil, newError(CodeGuestAccessBlocked, fmt.Sprintf("Pinterest denied guest access (HTTP %d); no bypass attempted", resp.StatusCode), resp.StatusCode, ctype, retryAfter, nil)
	}
	if resp.StatusCode >= 400 {
		preview := string(bodyBytes)
		if len(preview) > 160 {
			preview = preview[:160]
		}
		return nil, nil, newError(CodeHTTPError, fmt.Sprintf("Pinterest returned HTTP %d: %s", resp.StatusCode, preview), resp.StatusCode, ctype, retryAfter, nil)
	}
	if !strings.Contains(strings.ToLower(ctype), "json") {
		return nil, nil, newError(CodeChallengeRequired, fmt.Sprintf("Expected JSON but received %s; possible interstitial or challenge", firstNonEmpty(ctype, "unknown content type")), resp.StatusCode, ctype, "", nil)
	}

	var envelope resourceEnvelope
	if err := json.Unmarshal(bodyBytes, &envelope); err != nil {
		return nil, nil, newError(CodeProtocolError, "Pinterest response declared JSON but could not be decoded", resp.StatusCode, ctype, "", err)
	}
	if envelope.ResourceResponse == nil {
		return nil, nil, newError(CodeProtocolError, "missing resource_response envelope", resp.StatusCode, ctype, "", nil)
	}

	status := strings.ToLower(strings.TrimSpace(envelope.ResourceResponse.Status))
	if status != "" && status != "success" && status != "ok" {
		msg := firstNonEmpty(envelope.ResourceResponse.Message, "resource-level error")
		if len(msg) > 240 {
			msg = msg[:240]
		}
		return nil, nil, newError(CodeGuestAccessBlocked, fmt.Sprintf("Pinterest resource was not successful: %s", msg), resp.StatusCode, ctype, "", nil)
	}

	return &envelope, json.RawMessage(bodyBytes), nil
}
