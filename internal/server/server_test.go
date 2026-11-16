package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flinternet/flinternet/internal/pinterest"
	"github.com/flinternet/flinternet/web"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestServer(t *testing.T) (*Server, *int) {
	return newTestServerWithShareKey(t, "")
}

func newTestServerWithShareKey(t *testing.T, shareKey string) (*Server, *int) {
	t.Helper()
	upstreamCalls := 0

	mockTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		upstreamCalls++
		rec := httptest.NewRecorder()
		switch req.URL.Path {
		case "/resource/BaseSearchResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			rawData := req.URL.Query().Get("data")
			if strings.Contains(rawData, `"scope":"users"`) {
				_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"results":[{"id":"u-55","username":"nordicstudio","full_name":"Nordic Studio","follower_count":1500,"image_large_url":"https://i.pinimg.com/140x140_RS/nordic.jpg"}]}}}`)
				break
			}
			_, _ = rec.WriteString(`{
				"resource_response": {
					"status": "success",
					"bookmark": "search-next-1",
					"data": {
						"results": [
							{
								"id": "e1_pin-101",
								"title": "Nordic Cabin",
								"dominant_color": "#1e293b",
								"images": {
									"236x": {"url": "https://i.pinimg.com/236x/cabin.jpg", "width": 236, "height": 354},
									"orig": {"url": "https://i.pinimg.com/originals/cabin.jpg", "width": 1200, "height": 1800}
								}
							}
						],
						"rankedGuides": [
							{"id": "guide-1", "display": "Wood", "term": "nordic cabin wood"}
						]
					}
				}
			}`)
		case "/resource/AdvancedTypeaheadResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"items":[{"query":"nordic cabin","label":"nordic cabin","type":"query"}]}}}`)
		case "/resource/SearchTabResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"t1","label":"Architecture","query":"nordic cabin architecture","scope":"pins"}]}}`)
		case "/resource/UserResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"id":"u-55","username":"nordicstudio","full_name":"Nordic Studio","pin_count":120,"board_count":10}}}`)
		case "/resource/BoardsFeedResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","bookmark":"b-next","data":[{"id":"brd-9","name":"Cabins","url":"/nordicstudio/cabins/","pin_count":42}]}}`)
		case "/resource/UserPinsResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"pin-202","title":"Sauna","images":{"736x":{"url":"https://i.pinimg.com/736x/sauna.jpg","width":736,"height":920}}}]}}`)
		case "/resource/UserActivityPinsResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"pin-303","title":"Created Timber","images":{"474x":{"url":"https://i.pinimg.com/474x/timber.jpg","width":474,"height":600}}}]}}`)
		case "/resource/BoardResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"id":"brd-9","name":"Cabins","url":"/nordicstudio/cabins/","pin_count":42}}}`)
		case "/resource/BoardFeedResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"e1_pin-404","title":"A-Frame","images":{"orig":{"url":"https://i.pinimg.com/originals/aframe.jpg","width":1000,"height":1400}}}]}}`)
		case "/resource/PinResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"id":"pin-999","title":"Nordic Lamp","description":"Warm birch pendant","reaction_counts":{"1":12},"repin_count":40,"aggregated_pin_data":{"id":"agg-999","aggregated_stats":{"saves":88,"comments":1}},"pinner":{"username":"nordicstudio","full_name":"Nordic Studio"},"board":{"id":"brd-9","name":"Cabins","url":"/nordicstudio/cabins/"},"images":{"orig":{"url":"https://i.pinimg.com/originals/lamp.jpg","width":900,"height":1200}}}}}`)
		case "/resource/UnifiedCommentsResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"c1","type":"aggregatedcomment","text":"Love the birch shade","like_count":3,"user":{"first_name":"Sam","username":"sam"}}]}}`)
		case "/resource/BestPinsFeedAltResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","bookmark":"ideas-next","data":[{"id":"e1_pin-505","title":"Nordic Idea","images":{"orig":{"url":"https://i.pinimg.com/originals/idea.jpg","width":800,"height":1000}}}]}}`)
		default:
			rec.WriteHeader(http.StatusNotFound)
		}
		return rec.Result(), nil
	})

	client, err := pinterest.NewClient(pinterest.Config{
		BaseURL:                   pinterest.DefaultBaseURL,
		AllowUnverifiedPagination: true,
		HTTPClient:                &http.Client{Transport: mockTransport},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		t.Fatalf("web.StaticFS failed: %v", err)
	}
	tmplFS, err := web.TemplatesFS()
	if err != nil {
		t.Fatalf("web.TemplatesFS failed: %v", err)
	}

	srv, err := New(Config{
		Client:      client,
		StaticFS:    staticFS,
		TemplatesFS: tmplFS,
		CacheTTL:    time.Minute,
		ShareKey:    shareKey,
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	return srv, &upstreamCalls
}

func doRequest(t *testing.T, handler http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAllServerEndpointsAndSSRPages(t *testing.T) {
	srv, upstreamCalls := newTestServer(t)
	h := srv.Handler()

	// Health & Endpoints
	res := doRequest(t, h, http.MethodGet, "/api/v1/health", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"guest-anonymous-only"`) {
		t.Fatalf("unexpected health response: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/endpoints", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "BaseSearchResource/get") {
		t.Fatalf("unexpected endpoints response: %d %s", res.Code, res.Body.String())
	}

	// Cold-cache e1_ pins without a ?p= snapshot token must NOT hit Pinterest or correlate via ?q=.
	callsBeforeCold := *upstreamCalls
	res = doRequest(t, h, http.MethodGet, "/pin/e1_pin-101/?q=cabin-cold&s=pins", nil)
	if res.Code != http.StatusNotFound || *upstreamCalls != callsBeforeCold {
		t.Fatalf("expected 404 with 0 upstream calls without ?p= snapshot token, got code=%d calls=%d", res.Code, *upstreamCalls-callsBeforeCold)
	}

	// Search GET + cache verification
	res = doRequest(t, h, http.MethodGet, "/api/v1/search?q=cabin&scope=pins", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("search GET failed: %d %s", res.Code, res.Body.String())
	}
	var sr searchResponse
	if err := json.Unmarshal(res.Body.Bytes(), &sr); err != nil {
		t.Fatalf("decode searchResponse failed: %v", err)
	}
	if sr.Count != 1 || sr.Pins[0].ID != "e1_pin-101" || sr.Cached {
		t.Fatalf("unexpected initial search response: %+v", sr)
	}

	callsBeforeCacheHit := *upstreamCalls
	res = doRequest(t, h, http.MethodGet, "/api/v1/search?q=cabin&scope=pins", nil)
	if err := json.Unmarshal(res.Body.Bytes(), &sr); err != nil {
		t.Fatalf("decode cached searchResponse failed: %v", err)
	}
	if !sr.Cached || *upstreamCalls != callsBeforeCacheHit {
		t.Fatalf("expected second search request to hit cache")
	}

	// Search POST continuation
	postBody := bytes.NewBufferString(`{"q":"cabin","scope":"pins","bookmark":"search-next-1"}`)
	res = doRequest(t, h, http.MethodPost, "/api/v1/search", postBody)
	if res.Code != http.StatusOK {
		t.Fatalf("search POST failed: %d %s", res.Code, res.Body.String())
	}

	// Suggest & Search Tabs
	res = doRequest(t, h, http.MethodGet, "/api/v1/suggest?term=nord", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "nordic cabin") {
		t.Fatalf("suggest failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/search/tabs?q=nordic&scope=pins", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Architecture") {
		t.Fatalf("search/tabs failed: %d %s", res.Code, res.Body.String())
	}

	// Profile, Boards, Pins, Created Pins
	res = doRequest(t, h, http.MethodGet, "/api/v1/users/nordicstudio", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"u-55"`) {
		t.Fatalf("profile failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/users/nordicstudio/boards", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"b-next"`) {
		t.Fatalf("user boards failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/users/nordicstudio/pins", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pin-202"`) {
		t.Fatalf("user pins failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/users/nordicstudio/created", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pin-303"`) {
		t.Fatalf("user created pins failed: %d %s", res.Code, res.Body.String())
	}

	// Board metadata, Board pins, Board feed
	res = doRequest(t, h, http.MethodGet, "/api/v1/boards/nordicstudio/cabins", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"brd-9"`) {
		t.Fatalf("board metadata failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/boards/nordicstudio/cabins/pins", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"e1_pin-404"`) {
		t.Fatalf("board pins failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/board-feed?board_id=brd-9", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"e1_pin-404"`) {
		t.Fatalf("board-feed failed: %d %s", res.Code, res.Body.String())
	}

	// Pin Detail, Comments API, Ideas API
	res = doRequest(t, h, http.MethodGet, "/api/v1/pins/nordic-lamp--pin-999?related=1", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"Nordic Lamp"`) || !strings.Contains(res.Body.String(), `"e1_pin-101"`) {
		t.Fatalf("direct PinResource lookup failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/pins/pin-999/comments", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"Love the birch shade"`) {
		t.Fatalf("pin comments API failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/ideas/nordic-design/908182459161", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"e1_pin-505"`) {
		t.Fatalf("ideas API failed: %d %s", res.Code, res.Body.String())
	}

	// SSR HTML Pages & Privacy Redirector Paths
	ssrChecks := []struct {
		path     string
		contains string
	}{
		{"/", "Flinternet"},
		{"/search/pins/?q=cabin", "Nordic Cabin"},
		{"/search/people/?q=nordic", "@nordicstudio"},
		{"/pin/nordic-lamp--pin-999/", "Warm birch pendant"},
		{"/pin/pin-999/comments/", "Love the birch shade"},
		{"/nordicstudio/", "Cabins"},
		{"/nordicstudio/_created/", "Created Timber"},
		{"/nordicstudio/_saved/", "Sauna"},
		{"/nordicstudio/cabins/", "A-Frame"},
		{"/ideas/", "/ideas/?bookmark=b-next"},
		{"/ideas/nordic-design/908182459161/", "Nordic Idea"},
		{"/ideas/nordic-design/908182459161/", "bookmark=ideas-next"},
		{"/legal", "Legal Notice"},
		{"/search.php?q=cabin", "Nordic Cabin"},
	}
	for _, tc := range ssrChecks {
		res = doRequest(t, h, http.MethodGet, tc.path, nil)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), tc.contains) {
			t.Fatalf("SSR route %q failed (code=%d): expected %q in body", tc.path, res.Code, tc.contains)
		}
	}

	homeHTML := doRequest(t, h, http.MethodGet, "/", nil).Body.String()
	for _, unwanted := range []string{"REST API Catalog", "Anonymous Read-Only Guest Client", "Brutalist Architecture"} {
		if strings.Contains(homeHTML, unwanted) {
			t.Fatalf("unexpected homepage clutter %q in /", unwanted)
		}
	}
	if !strings.Contains(homeHTML, `<meta name="twitter:card" content="summary">`) ||
		!strings.Contains(homeHTML, `<meta property="og:image" content="http://example.com/static/android-chrome-512x512.png">`) {
		t.Fatalf("expected absolute OG/Twitter tags on /")
	}

	pinHTML := doRequest(t, h, http.MethodGet, "/pin/nordic-lamp--pin-999/", nil).Body.String()
	for _, want := range []string{
		`<meta property="og:url" content="http://example.com/pin/nordic-lamp--pin-999/">`,
		`<meta property="og:image" content="http://example.com/api/v1/media/proxy?`,
		`<meta property="og:image:width" content="900">`,
		`<meta property="og:image:height" content="1200">`,
		`<meta name="twitter:card" content="summary_large_image">`,
	} {
		if !strings.Contains(pinHTML, want) {
			t.Fatalf("expected meta tag %q in pin HTML", want)
		}
	}

	// Omnibar @user redirect
	res = doRequest(t, h, http.MethodGet, "/search/pins/?q=@nordicstudio", nil)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "/nordicstudio/" {
		t.Fatalf("expected 302 redirect to /nordicstudio/, got %d (%q)", res.Code, res.Header().Get("Location"))
	}

	// Static CSS/JS, Binternet compat, SSRF proxy guard, and 404s
	res = doRequest(t, h, http.MethodGet, "/static/style.css", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for /static/style.css, got %d", res.Code)
	}
	res = doRequest(t, h, http.MethodGet, "/static/missing.css", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing static asset, got %d", res.Code)
	}
	res = doRequest(t, h, http.MethodGet, "/api.php?q=cabin", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"e1_pin-101"`) {
		t.Fatalf("binternet /api.php failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/image_proxy.php?url=http://127.0.0.1:8080/secret", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected SSRF URL to be rejected with 400, got %d %s", res.Code, res.Body.String())
	}
	// Ensure keyless mode emits self-contained ?p= tokens and never ?c= or ?q=.
	searchHTMLNoKey := doRequest(t, h, http.MethodGet, "/search/pins/?q=cabin", nil).Body.String()
	if !strings.Contains(searchHTMLNoKey, "/pin/e1_pin-101/?p=") || strings.Contains(searchHTMLNoKey, "/pin/e1_pin-101/?c=") || strings.Contains(searchHTMLNoKey, "/pin/e1_pin-101/?q=") {
		t.Fatalf("expected self-contained ?p= token without ?c= or ?q= on keyless /pin/ link")
	}

	res = doRequest(t, h, http.MethodGet, "/api/v1/does-not-exist", nil)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("expected JSON 404 for unknown API route, got %d %s", res.Code, res.Body.String())
	}
}

func extractPinSnapshotToken(t *testing.T, htmlBody, pinID string) string {
	t.Helper()
	marker := "/pin/" + pinID + "/?p="
	idx := strings.Index(htmlBody, marker)
	if idx < 0 {
		t.Fatalf("expected self-contained ?p= link for %s", pinID)
	}
	rest := htmlBody[idx+len(marker):]
	end := strings.IndexAny(rest, `"'&<`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func TestSelfContainedPinSnapshotsAcrossRestarts(t *testing.T) {
	// 1) Keyless mode (empty ShareKey): base64url compressed snapshot, 0 upstream calls across restarts, zero ?q= leaks.
	srvNoKey1, _ := newTestServer(t)
	hNoKey1 := srvNoKey1.Handler()

	searchNoKeyHTML := doRequest(t, hNoKey1, http.MethodGet, "/search/pins/?q=cabin", nil).Body.String()
	if strings.Contains(searchNoKeyHTML, "/pin/e1_pin-101/?q=") {
		t.Fatalf("search query leaked onto pin link in keyless mode")
	}
	keylessSearchTok := extractPinSnapshotToken(t, searchNoKeyHTML, "e1_pin-101")
	boardNoKeyHTML := doRequest(t, hNoKey1, http.MethodGet, "/nordicstudio/cabins/", nil).Body.String()
	keylessBoardTok := extractPinSnapshotToken(t, boardNoKeyHTML, "e1_pin-404")
	ideasNoKeyHTML := doRequest(t, hNoKey1, http.MethodGet, "/ideas/nordic-design/908182459161/", nil).Body.String()
	keylessIdeasTok := extractPinSnapshotToken(t, ideasNoKeyHTML, "e1_pin-505")

	srvNoKey2, upstreamNoKey2 := newTestServer(t)
	hNoKey2 := srvNoKey2.Handler()
	for _, tc := range []struct {
		id, tok, wantTitle string
	}{
		{"e1_pin-101", keylessSearchTok, "Nordic Cabin"},
		{"e1_pin-404", keylessBoardTok, "A-Frame"},
		{"e1_pin-505", keylessIdeasTok, "Nordic Idea"},
	} {
		// API lookup must restore purely from ?p= with 0 upstream calls and without polluting pinCache.
		callsBefore := *upstreamNoKey2
		apiRes := doRequest(t, hNoKey2, http.MethodGet, "/api/v1/pins/"+tc.id+"?p="+tc.tok, nil)
		if apiRes.Code != http.StatusOK || !strings.Contains(apiRes.Body.String(), tc.wantTitle) {
			t.Fatalf("keyless API restart restore failed for %s: code=%d body=%s", tc.id, apiRes.Code, apiRes.Body.String())
		}
		if *upstreamNoKey2 != callsBefore {
			t.Fatalf("expected 0 upstream calls restoring %s from ?p=, got %d", tc.id, *upstreamNoKey2-callsBefore)
		}
		if _, cached := srvNoKey2.pinCache.Get(tc.id); cached {
			t.Fatalf("keyless unauthenticated ?p= decode must not populate global pinCache")
		}
		// SSR page also resolves on fresh instance.
		res := doRequest(t, hNoKey2, http.MethodGet, "/pin/"+tc.id+"/?p="+tc.tok, nil)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), tc.wantTitle) {
			t.Fatalf("keyless SSR restart lookup failed for %s: code=%d", tc.id, res.Code)
		}
	}

	// Opaque e1_ pin IDs that naturally contain "--" must never be truncated as SEO slugs.
	doubleHyphenID := "e1_AfqPShx5RnTbTQyvkjDJ--ZmdoiIVjrQepld14FRe1aeJFhrUCNGaA"
	dhTok, err := encodeSnapshotBytes(nil, doubleHyphenID, pinSnapshot{
		Img:   "oj:f9accde677cff536eea02649e4d55ebb",
		Title: "Double Hyphen Opaque Pin",
	})
	if err != nil {
		t.Fatalf("encode double-hyphen snapshot failed: %v", err)
	}
	dhRes := doRequest(t, hNoKey2, http.MethodGet, "/pin/"+doubleHyphenID+"/?p="+dhTok, nil)
	if dhRes.Code != http.StatusOK || !strings.Contains(dhRes.Body.String(), "Double Hyphen Opaque Pin") {
		t.Fatalf("double-hyphen e1_ pin ID failed: code=%d", dhRes.Code)
	}

	evilTok, _ := encodeSnapshotBytes(nil, "e1_xss", pinSnapshot{
		Img:   "oj:b1b717714fc20ded79a7580af314718d",
		Title: `<script>alert(1)</script>`,
		Color: `red;background:url(https://evil.test)`,
		PUser: `/evil.test`,
		Link:  `javascript:alert(1)`,
		AggID: `99" onclick="alert(1)`,
	})
	xssRes := doRequest(t, hNoKey2, http.MethodGet, "/pin/e1_xss/?p="+evilTok, nil)
	if xssRes.Code != http.StatusOK {
		t.Fatalf("expected 200 rendering sanitized snapshot, got %d", xssRes.Code)
	}
	if _, cached := srvNoKey2.pinCache.Get("e1_xss"); cached {
		t.Fatalf("keyless unauthenticated SSR ?p= decode must not populate global pinCache")
	}
	xssBody := xssRes.Body.String()
	if strings.Contains(xssBody, `<script>alert(1)</script>`) ||
		strings.Contains(xssBody, `href="//evil.test/`) ||
		strings.Contains(xssBody, `javascript:alert(1)`) ||
		strings.Contains(xssBody, `url(https://evil.test)`) ||
		strings.Contains(xssBody, `ZgotmplZ`) {
		t.Fatalf("unsafe crafted payload leaked into SSR HTML: %s", xssBody)
	}

	// 2) Keyed mode (ShareKey set): AES-256-GCM encrypted snapshot, rejects keyless/tampered/cross-pin/wrong-key tokens.
	srv1, _ := newTestServerWithShareKey(t, "test-secret-key-2026")
	h1 := srv1.Handler()

	searchHTML := doRequest(t, h1, http.MethodGet, "/search/pins/?q=cabin", nil).Body.String()
	if strings.Contains(searchHTML, "/pin/e1_pin-101/?q=") {
		t.Fatalf("plaintext search query leaked onto pin link")
	}
	searchTok := extractPinSnapshotToken(t, searchHTML, "e1_pin-101")
	boardHTML := doRequest(t, h1, http.MethodGet, "/nordicstudio/cabins/", nil).Body.String()
	boardTok := extractPinSnapshotToken(t, boardHTML, "e1_pin-404")
	ideasHTML := doRequest(t, h1, http.MethodGet, "/ideas/nordic-design/908182459161/", nil).Body.String()
	ideasTok := extractPinSnapshotToken(t, ideasHTML, "e1_pin-505")

	// Fresh server with the SAME ShareKey.
	srv2, upstream2 := newTestServerWithShareKey(t, "test-secret-key-2026")
	h2 := srv2.Handler()

	for _, badPath := range []string{
		"/pin/e1_pin-101/?p=" + boardTok,
		"/pin/e1_pin-101/?p=invalid-token",
		"/pin/e1_pin-101/",
	} {
		res := doRequest(t, h2, http.MethodGet, badPath, nil)
		if res.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %q, got %d", badPath, res.Code)
		}
	}
	if *upstream2 != 0 {
		t.Fatalf("expected 0 upstream calls for rejected tokens, got %d", *upstream2)
	}

	// Previously shared keyless token continues working even after ShareKey is later enabled, without polluting pinCache.
	legacyRes := doRequest(t, h2, http.MethodGet, "/api/v1/pins/e1_pin-101?p="+keylessSearchTok, nil)
	if legacyRes.Code != http.StatusOK || !strings.Contains(legacyRes.Body.String(), "Nordic Cabin") {
		t.Fatalf("expected previously shared keyless token to work after enabling ShareKey, got %d", legacyRes.Code)
	}
	if _, cached := srv2.pinCache.Get("e1_pin-101"); cached {
		t.Fatalf("legacy unauthenticated token must not populate global pinCache on keyed server")
	}

	// Fresh server with a DIFFERENT ShareKey must also reject encrypted tokens.
	srvWrongKey, upstreamWrongKey := newTestServerWithShareKey(t, "wrong-secret-key")
	resWrong := doRequest(t, srvWrongKey.Handler(), http.MethodGet, "/pin/e1_pin-101/?p="+searchTok, nil)
	if resWrong.Code != http.StatusNotFound || *upstreamWrongKey != 0 {
		t.Fatalf("expected 404 and 0 upstream calls on wrong ShareKey, got code=%d calls=%d", resWrong.Code, *upstreamWrongKey)
	}

	for _, tc := range []struct {
		id, tok, wantTitle string
	}{
		{"e1_pin-101", searchTok, "Nordic Cabin"},
		{"e1_pin-404", boardTok, "A-Frame"},
		{"e1_pin-505", ideasTok, "Nordic Idea"},
	} {
		callsBefore := *upstream2
		apiRes := doRequest(t, h2, http.MethodGet, "/api/v1/pins/"+tc.id+"?p="+tc.tok, nil)
		if apiRes.Code != http.StatusOK || !strings.Contains(apiRes.Body.String(), tc.wantTitle) {
			t.Fatalf("keyed API restart lookup failed for %s: code=%d", tc.id, apiRes.Code)
		}
		if *upstream2 != callsBefore {
			t.Fatalf("expected 0 upstream calls restoring keyed %s, got %d", tc.id, *upstream2-callsBefore)
		}
		res := doRequest(t, h2, http.MethodGet, "/pin/"+tc.id+"/?p="+tc.tok, nil)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), tc.wantTitle) {
			t.Fatalf("keyed SSR restart lookup failed for %s: code=%d", tc.id, res.Code)
		}
	}
}

func TestLessAIDefaultAndCookiePersistence(t *testing.T) {
	var seenFilterGenAI []bool
	mockTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/resource/BaseSearchResource/get/" {
			rawData := req.URL.Query().Get("data")
			fg := strings.Contains(rawData, `"filter_genai":true`)
			seenFilterGenAI = append(seenFilterGenAI, fg)
			title := "Unfiltered Pin"
			if fg {
				title = "Clean Non-AI Pin"
			}
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","bookmark":"next-1","data":{"results":[{"id":"e1_test-1","title":"` + title + `","images":{"orig":{"url":"https://i.pinimg.com/originals/test.jpg","width":800,"height":1000}}}],"rankedGuides":[{"id":"g1","display":"Art","term":"art"}]}}}`)
			return rec.Result(), nil
		}
		rec.WriteHeader(http.StatusNotFound)
		return rec.Result(), nil
	})

	client, err := pinterest.NewClient(pinterest.Config{
		BaseURL:                   pinterest.DefaultBaseURL,
		AllowUnverifiedPagination: true,
		HTTPClient:                &http.Client{Transport: mockTransport},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	staticFS, _ := web.StaticFS()
	tmplFS, _ := web.TemplatesFS()
	srv, err := New(Config{
		Client:      client,
		StaticFS:    staticFS,
		TemplatesFS: tmplFS,
		CacheTTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	h := srv.Handler()

	// 1. Default search (no query param, no cookie): Less AI is ON by default.
	res := doRequest(t, h, http.MethodGet, "/search/pins/?q=wallpaper", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Clean Non-AI Pin") {
		t.Fatalf("expected default Less AI ON search result, got code=%d body=%s", res.Code, res.Body.String())
	}
	if (!strings.Contains(res.Body.String(), `/search/pins/?q=wallpaper&filter_genai=false`) &&
		!strings.Contains(res.Body.String(), `/search/pins/?q=wallpaper&amp;filter_genai=false`)) ||
		!strings.Contains(res.Body.String(), `class="guide-chip active"`) {
		t.Fatalf("expected active Less AI toggle chip linking to filter_genai=false in:\n%s", res.Body.String())
	}

	// 2. Toggle OFF explicitly -> returns unfiltered results and sets flinternet_less_ai=0 cookie.
	res = doRequest(t, h, http.MethodGet, "/search/pins/?q=wallpaper&filter_genai=false", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Unfiltered Pin") {
		t.Fatalf("expected Less AI OFF result, got code=%d", res.Code)
	}
	var savedCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == lessAICookieName {
			savedCookie = c
		}
	}
	if savedCookie == nil || savedCookie.Value != "0" {
		t.Fatalf("expected flinternet_less_ai=0 cookie, got %+v", savedCookie)
	}
	if !strings.Contains(res.Body.String(), `/search/pins/?q=wallpaper&filter_genai=true`) &&
		!strings.Contains(res.Body.String(), `/search/pins/?q=wallpaper&amp;filter_genai=true`) {
		t.Fatalf("expected inactive Less AI toggle chip linking to filter_genai=true in:\n%s", res.Body.String())
	}

	// 3. Subsequent search carrying cookie remembers Less AI = OFF without needing ?filter_genai in URL.
	reqWithCookie := httptest.NewRequest(http.MethodGet, "/search/pins/?q=mountains", nil)
	reqWithCookie.AddCookie(&http.Cookie{Name: lessAICookieName, Value: "0"})
	recCookie := httptest.NewRecorder()
	h.ServeHTTP(recCookie, reqWithCookie)
	if recCookie.Code != http.StatusOK || !strings.Contains(recCookie.Body.String(), "Unfiltered Pin") {
		t.Fatalf("expected cookie preference to keep Less AI OFF, got code=%d body=%s", recCookie.Code, recCookie.Body.String())
	}

	// 4. Toggle back ON explicitly -> sets flinternet_less_ai=1 cookie.
	res = doRequest(t, h, http.MethodGet, "/search/pins/?q=mountains&filter_genai=true", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Clean Non-AI Pin") {
		t.Fatalf("expected Less AI ON after toggling back on")
	}
	var onCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == lessAICookieName {
			onCookie = c
		}
	}
	if onCookie == nil || onCookie.Value != "1" {
		t.Fatalf("expected flinternet_less_ai=1 cookie, got %+v", onCookie)
	}

	want := []bool{true, false, false, true}
	if len(seenFilterGenAI) != len(want) {
		t.Fatalf("unexpected upstream filter_genai sequence: got %v, want %v", seenFilterGenAI, want)
	}
	for i := range want {
		if seenFilterGenAI[i] != want[i] {
			t.Fatalf("step %d upstream filter_genai=%v, want %v", i, seenFilterGenAI[i], want[i])
		}
	}
}
