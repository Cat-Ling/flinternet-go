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
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"pin-404","title":"A-Frame","images":{"orig":{"url":"https://i.pinimg.com/originals/aframe.jpg","width":1000,"height":1400}}}]}}`)
		case "/resource/PinResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":{"id":"pin-999","title":"Nordic Lamp","description":"Warm birch pendant","reaction_counts":{"1":12},"repin_count":40,"aggregated_pin_data":{"id":"agg-999","aggregated_stats":{"saves":88,"comments":1}},"pinner":{"username":"nordicstudio","full_name":"Nordic Studio"},"board":{"id":"brd-9","name":"Cabins","url":"/nordicstudio/cabins/"},"images":{"orig":{"url":"https://i.pinimg.com/originals/lamp.jpg","width":900,"height":1200}}}}}`)
		case "/resource/UnifiedCommentsResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","data":[{"id":"c1","type":"aggregatedcomment","text":"Love the birch shade","like_count":3,"user":{"first_name":"Sam","username":"sam"}}]}}`)
		case "/resource/BestPinsFeedAltResource/get/":
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"resource_response":{"status":"success","bookmark":"ideas-next","data":[{"id":"pin-505","title":"Nordic Idea","images":{"orig":{"url":"https://i.pinimg.com/originals/idea.jpg","width":800,"height":1000}}}]}}`)
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

	// Cold-cache refresh of an e1_ search pin with ?q=&s= rehydrates without 404.
	res = doRequest(t, h, http.MethodGet, "/pin/e1_pin-101/?q=cabin-cold&s=pins", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Nordic Cabin") {
		t.Fatalf("cold e1_ pin refresh failed: %d", res.Code)
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
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pin-404"`) {
		t.Fatalf("board pins failed: %d %s", res.Code, res.Body.String())
	}
	res = doRequest(t, h, http.MethodGet, "/api/v1/board-feed?board_id=brd-9", nil)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pin-404"`) {
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
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"pin-505"`) {
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
	res = doRequest(t, h, http.MethodGet, "/api/v1/does-not-exist", nil)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("expected JSON 404 for unknown API route, got %d %s", res.Code, res.Body.String())
	}
}
