package pinterest

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAnonymousCookieJarPolicy(t *testing.T) {
	jar := NewAnonymousCookieJar()
	pinURL, _ := url.Parse("https://www.pinterest.com/search/pins/")
	evilURL, _ := url.Parse("https://evil.example.com/")
	httpPinURL, _ := url.Parse("http://www.pinterest.com/")

	jar.SetCookies(pinURL, []*http.Cookie{
		{Name: "csrftoken", Value: "anon-csrf-123", Domain: ".pinterest.com", Path: "/"},
		{Name: "_auth", Value: "1", Domain: ".pinterest.com", Path: "/"},
		{Name: "_pinterest_sess", Value: "secret", Domain: ".pinterest.com", Path: "/"},
	})
	jar.SetCookies(evilURL, []*http.Cookie{
		{Name: "csrftoken", Value: "evil-csrf", Path: "/"},
	})
	jar.SetCookies(httpPinURL, []*http.Cookie{
		{Name: "csrftoken", Value: "insecure-csrf", Path: "/"},
	})

	cookies := jar.Cookies(pinURL)
	if len(cookies) != 1 || cookies[0].Name != "csrftoken" || cookies[0].Value != "anon-csrf-123" {
		t.Fatalf("expected only csrftoken for https pinterest, got %+v", cookies)
	}
	if got := jar.CSRFToken(pinURL); got != "anon-csrf-123" {
		t.Fatalf("expected CSRFToken=anon-csrf-123, got %q", got)
	}
	if got := jar.Cookies(evilURL); len(got) != 0 {
		t.Fatalf("expected no cookies for non-pinterest domain, got %+v", got)
	}
	if got := jar.Cookies(httpPinURL); len(got) != 0 {
		t.Fatalf("expected no cookies over plain HTTP, got %+v", got)
	}
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient(Config{BaseURL: "http://www.pinterest.com"}); err == nil {
		t.Fatalf("expected error for non-HTTPS base_url")
	}
	if _, err := NewClient(Config{BaseURL: "https://evil.example.com"}); err == nil {
		t.Fatalf("expected error for non-pinterest host")
	}
	if _, err := NewClient(Config{BaseURL: "https://ru.pinterest.com"}); err != nil {
		t.Fatalf("expected regional https pinterest host to succeed, got %v", err)
	}
}

func newMockServerClient(t *testing.T, handler http.HandlerFunc, allowUnverified bool) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{
		BaseURL:                   srv.URL,
		AllowUnverifiedPagination: allowUnverified,
		allowInsecureForTests:     true,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	return c, srv
}

func TestSearchScopesAndPagination(t *testing.T) {
	var seenMethods []string
	var seenData []string
	var seenCSRF []string

	c, _ := newMockServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenMethods = append(seenMethods, r.Method)
		seenCSRF = append(seenCSRF, r.Header.Get("X-CSRFToken"))

		if r.URL.Path != "/resource/BaseSearchResource/get/" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Fatalf("missing X-Requested-With header")
		}
		if r.Header.Get("X-Pinterest-AppState") != "active" {
			t.Fatalf("missing X-Pinterest-AppState header")
		}

		var rawData string
		if r.Method == http.MethodGet {
			rawData = r.URL.Query().Get("data")
			http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "fresh-csrf-token", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "_auth", Value: "should-be-dropped", Path: "/"})
		} else {
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm failed: %v", err)
			}
			rawData = r.FormValue("data")
		}
		seenData = append(seenData, rawData)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"resource_response": {
				"status": "success",
				"bookmark": "cursor-page-2",
				"data": {
					"results": [
						{
							"id": "181199584997439257",
							"title": "Lake Michigan Dunes",
							"dominant_color": "#264653",
							"images": {
								"236x": {"url": "https://i.pinimg.com/236x/a.jpg", "width": 236, "height": 354},
								"orig": {"url": "https://i.pinimg.com/originals/a.jpg", "width": 1000, "height": 1500}
							}
						}
					],
					"rankedGuides": [
						{"id": "g1", "display": "Sunset", "term": "michigan sunset"}
					],
					"oneBarModules": [
						{
							"id": "obm-1",
							"type": "onebarmodule",
							"display": {"display_text": ""},
							"action": {
								"title": "Filters",
								"filters": [{"display": {"display_text": "All Pins"}, "action": {"feed_url": "/search/pins/?q=michigan"}}]
							}
						},
						{
							"id": "obm-genai",
							"type": "onebarmodule",
							"display": {"display_text": "Less AI", "is_selected": false},
							"aux_data": {"module_type": "GENAI_FILTER"},
							"action": {
								"feed_url": "/search/pins/?q=michigan&filter_genai=true"
							}
						}
					]
				}
			}
		}`))
	}, false)

	ctx := context.Background()
	page, err := c.SearchPins(ctx, SearchParams{Query: "michigan", Scope: ScopePins})
	if err != nil {
		t.Fatalf("SearchPins failed: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].ID != "181199584997439257" {
		t.Fatalf("unexpected pin results: %+v", page.Results)
	}
	if page.Results[0].ImageURL != "https://i.pinimg.com/originals/a.jpg" {
		t.Fatalf("expected best orig image URL, got %q", page.Results[0].ImageURL)
	}
	if page.Results[0].ThumbnailURL != "https://i.pinimg.com/236x/a.jpg" || page.Results[0].ProxyThumbnailURL == "" {
		t.Fatalf("expected best non-orig thumbnail URL 236x, got %q", page.Results[0].ThumbnailURL)
	}
	if bestThumb, ok := BestThumbnailVariant(map[string]ImageVariant{
		"orig":   {URL: "https://i.pinimg.com/originals/x.jpg", Width: 2000, Height: 3000},
		"236x":   {URL: "https://i.pinimg.com/236x/x.jpg", Width: 236, Height: 354},
		"474x":   {URL: "https://i.pinimg.com/474x/x.jpg", Width: 474, Height: 711},
		"custom": {URL: "https://i.pinimg.com/custom/x.jpg", Width: 736, Height: 1104},
		"heic":   {URL: "https://i.pinimg.com/900x/x.heic", Width: 900, Height: 1350},
	}); !ok || bestThumb.URL != "https://i.pinimg.com/custom/x.jpg" {
		t.Fatalf("expected highest-width non-orig non-heic thumbnail dynamically, got %+v ok=%v", bestThumb, ok)
	}
	if page.Results[0].ProxyImageURL == "" {
		t.Fatalf("expected non-empty proxy image URL")
	}
	if page.Bookmark != "cursor-page-2" {
		t.Fatalf("expected bookmark=cursor-page-2, got %q", page.Bookmark)
	}
	if len(page.Tabs) != 2 || page.Tabs[0].Label != "Sunset" || page.Tabs[1].Label != "Less AI" || !page.Tabs[1].FilterGenAI || !page.Tabs[1].IsGenAIFilter {
		t.Fatalf("expected extracted rankedGuide and Less AI tab, got %+v", page.Tabs)
	}
	// Verify boolean false fields were NOT stripped from JSON options.
	if !strings.Contains(seenData[0], `"filter_genai":false`) || !strings.Contains(seenData[0], `"static_feed":false`) ||
		!strings.Contains(seenData[0], `"gated":false`) || !strings.Contains(seenData[0], `"no_fetch_context_on_resource":false`) {
		t.Fatalf("expected false boolean options preserved in JSON, got %s", seenData[0])
	}

	// Default client blocks unverified search pagination.
	_, err = c.SearchPins(ctx, SearchParams{Query: "michigan", Scope: ScopePins, Bookmark: "cursor-page-2"})
	pErr, ok := AsError(err)
	if !ok || pErr.Code != CodePaginationNotVerified {
		t.Fatalf("expected CodePaginationNotVerified, got %v", err)
	}

	// Explicit per-call AllowUnverified sends POST continuation + csrftoken emitted above.
	allow := true
	page2, err := c.SearchPins(ctx, SearchParams{
		Query:           "michigan",
		Scope:           ScopePins,
		Bookmark:        "cursor-page-2",
		AllowUnverified: &allow,
	})
	if err != nil {
		t.Fatalf("continuation failed: %v", err)
	}
	if page2.EvidenceGrade != "C" {
		t.Fatalf("expected grade C on continuation, got %q", page2.EvidenceGrade)
	}
	if seenMethods[0] != "GET" || seenMethods[1] != "POST" {
		t.Fatalf("expected GET then POST, got %v", seenMethods)
	}
	if seenCSRF[1] != "fresh-csrf-token" {
		t.Fatalf("expected X-CSRFToken from anonymous csrftoken cookie, got %q", seenCSRF[1])
	}
	if !strings.Contains(seenData[1], `"bookmarks":["cursor-page-2"]`) {
		t.Fatalf("expected bookmarks array in POST continuation, got %s", seenData[1])
	}

	// Verify boards search uses null page_size by default.
	if _, err := c.SearchBoards(ctx, SearchParams{Query: "arch"}); err != nil {
		t.Fatalf("SearchBoards failed: %v", err)
	}
	if !strings.Contains(seenData[2], `"page_size":null`) || !strings.Contains(seenData[2], `"rs":"content_type_filter"`) {
		t.Fatalf("unexpected boards search options: %s", seenData[2])
	}

	// Verify FilterGenAI=true sends "filter_genai":true upstream.
	if _, err := c.SearchPins(ctx, SearchParams{Query: "michigan", Scope: ScopePins, FilterGenAI: true}); err != nil {
		t.Fatalf("SearchPins FilterGenAI=true failed: %v", err)
	}
	if !strings.Contains(seenData[len(seenData)-1], `"filter_genai":true`) {
		t.Fatalf("expected filter_genai:true in upstream options, got %s", seenData[len(seenData)-1])
	}
}

func TestAllRemainingResources(t *testing.T) {
	seenResources := make(map[string]string)

	c, _ := newMockServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenResources[r.URL.Path] = r.URL.Query().Get("data")
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/resource/AdvancedTypeaheadResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":{"items":[{"query":"michigan lakes","label":"michigan lakes","type":"query"}]}}}`))
		case "/resource/SearchTabResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":[{"id":"tab-1","label":"All Pins","query":"michigan","scope":"pins"}]}}`))
		case "/resource/UserResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":{"id":"99001","username":"puremichigan","full_name":"Pure Michigan","pin_count":420,"board_count":18}}}`))
		case "/resource/BoardsFeedResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","bookmark":"board-cursor-2","data":[{"id":"-8761351838208535046","name":"","owner":{}},{"id":"b1","name":"Great Lakes","url":"/puremichigan/great-lakes/","pin_count":55}]}}`))
		case "/resource/UserPinsResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":[{"id":"p1","title":"Mackinac","images":{"736x":{"url":"https://i.pinimg.com/736x/m.jpg","width":736,"height":900}}}]}}`))
		case "/resource/UserActivityPinsResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":[{"id":"p2","title":"Created Video","videos":{"video_list":{"V_720P":{"url":"https://v1.pinimg.com/videos/iht/expMp4/v.mp4","width":720,"height":1280,"duration":12.5,"thumbnail":"https://i.pinimg.com/videos/thumbnails/originals/t.jpg"}}}}]}}`))
		case "/resource/BoardResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":{"id":"b1","name":"Great Lakes","url":"/puremichigan/great-lakes/","pin_count":55}}}`))
		case "/resource/BoardFeedResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","bookmark":"bf-next","data":[{"id":"p3","title":"Tahquamenon","images":{"474x":{"url":"https://i.pinimg.com/474x/t.jpg","width":474,"height":600}}}]}}`))
		case "/resource/PinResource/get/":
			if r.Header.Get("X-Pinterest-Pws-Handler") != "www/pin/[id].js" {
				t.Fatalf("expected X-Pinterest-Pws-Handler=www/pin/[id].js, got %q", r.Header.Get("X-Pinterest-Pws-Handler"))
			}
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","data":{"id":"181199584997439257","title":"Meadows necklace","dominant_color":"#edeeec","reaction_counts":{"1":9},"repin_count":29,"aggregated_pin_data":{"id":"5172215728905962662","aggregated_stats":{"saves":44,"comments":2}},"images":{"orig":{"url":"https://i.pinimg.com/originals/8d/b4/68/neck.jpg","width":800,"height":1000}},"pinner":{"first_name":"Victoria","username":"victoria"},"board":{"id":"b99","name":"Jewelry","url":"/victoria/jewelry/"}}}}`))
		case "/resource/UnifiedCommentsResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","bookmark":"cmt-2","data":[{"id":"c1","type":"aggregatedcomment","text":"Gorgeous resin work!","like_count":4,"comment_count":1,"user":{"first_name":"Elena","username":"elena","image_medium_url":"https://i.pinimg.com/75x75_RS/elena.jpg"}}]}}`))
		case "/resource/AggregatedCommentReplyFeedResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","bookmark":"rep-2","data":[{"id":"r1","type":"aggregatedcomment","text":"Thank you!","like_count":2,"user":{"first_name":"Victoria","username":"victoria"}}]}}`))
		case "/resource/BestPinsFeedAltResource/get/":
			_, _ = w.Write([]byte(`{"resource_response":{"status":"success","bookmark":"idea-2","data":[{"id":"p9","title":"Resin Craft","images":{"736x":{"url":"https://i.pinimg.com/736x/resin.jpg","width":736,"height":920}}}]}}`))
		default:
			t.Fatalf("unexpected endpoint path: %s", r.URL.Path)
		}
	}, false)

	ctx := context.Background()

	suggestions, err := c.Suggest(ctx, "mich", "pins", 5)
	if err != nil || len(suggestions) != 1 || suggestions[0].Query != "michigan lakes" {
		t.Fatalf("Suggest unexpected: err=%v, suggestions=%+v", err, suggestions)
	}

	tabs, err := c.SearchTabs(ctx, "michigan", "pins")
	if err != nil || len(tabs) != 1 || tabs[0].Label != "All Pins" {
		t.Fatalf("SearchTabs unexpected: err=%v, tabs=%+v", err, tabs)
	}

	profile, err := c.PublicProfile(ctx, "@puremichigan")
	if err != nil || profile.ID != "99001" || profile.Username != "puremichigan" {
		t.Fatalf("PublicProfile unexpected: err=%v, profile=%+v", err, profile)
	}
	if !strings.Contains(seenResources["/resource/UserResource/get/"], `"field_set_key":"unauth_profile"`) {
		t.Fatalf("missing unauth_profile fieldset in UserResource call")
	}

	boards, err := c.PublicProfileBoards(ctx, "puremichigan", "cursor-1")
	if err != nil || len(boards.Results) != 1 || boards.Bookmark != "board-cursor-2" || boards.EvidenceGrade != "A-P" {
		t.Fatalf("PublicProfileBoards unexpected: err=%v, boards=%+v", err, boards)
	}
	if !strings.Contains(seenResources["/resource/BoardsFeedResource/get/"], `"bookmarks":["cursor-1"]`) {
		t.Fatalf("expected bookmarks in BoardsFeedResource call")
	}

	pins, err := c.PublicProfilePins(ctx, "puremichigan", 25, "", nil)
	if err != nil || len(pins.Results) != 1 || pins.Results[0].ID != "p1" {
		t.Fatalf("PublicProfilePins unexpected: err=%v, pins=%+v", err, pins)
	}

	created, err := c.PublicCreatedPins(ctx, "puremichigan", "99001", 25, "", nil)
	if err != nil || len(created.Results) != 1 || !created.Results[0].IsVideo {
		t.Fatalf("PublicCreatedPins unexpected: err=%v, created=%+v", err, created)
	}
	if created.Results[0].VideoURL != "https://v1.pinimg.com/videos/iht/expMp4/v.mp4" {
		t.Fatalf("expected best video URL, got %q", created.Results[0].VideoURL)
	}

	board, err := c.PublicBoard(ctx, "puremichigan", "great-lakes")
	if err != nil || board.ID != "b1" || board.Slug != "great-lakes" {
		t.Fatalf("PublicBoard unexpected: err=%v, board=%+v", err, board)
	}

	boardPins, err := c.PublicBoardPins(ctx, "b1", "/puremichigan/great-lakes/", 25, "", nil)
	if err != nil || len(boardPins.Results) != 1 || boardPins.Results[0].ID != "p3" {
		t.Fatalf("PublicBoardPins unexpected: err=%v, boardPins=%+v", err, boardPins)
	}

	if got := ExtractPinID("meadows-necklace--181199584997439257"); got != "181199584997439257" {
		t.Fatalf("ExtractPinID slug mismatch: %q", got)
	}
	pin, err := c.PublicPin(ctx, "meadows-necklace--181199584997439257")
	if err != nil || pin.ID != "181199584997439257" || pin.Title != "Meadows necklace" || pin.Pinner.FullName != "Victoria" {
		t.Fatalf("PublicPin unexpected: err=%v, pin=%+v", err, pin)
	}
	if pin.Likes != 9 || pin.Saves != 44 || pin.Repins != 29 || pin.AggregatedPinID != "5172215728905962662" || pin.Board.Slug != "jewelry" {
		t.Fatalf("expected deep pin stats & board metadata, got %+v", pin)
	}
	if !strings.Contains(seenResources["/resource/PinResource/get/"], `"field_set_key":"detailed"`) ||
		!strings.Contains(seenResources["/resource/PinResource/get/"], `"add_fields":`) {
		t.Fatalf("missing detailed fieldset in PinResource call: %s", seenResources["/resource/PinResource/get/"])
	}

	comments, err := c.PublicPinComments(ctx, "181199584997439257", "", "")
	if err != nil || len(comments.Comments) != 1 || comments.Comments[0].Text != "Gorgeous resin work!" || comments.Bookmark != "cmt-2" {
		t.Fatalf("PublicPinComments unexpected: err=%v, comments=%+v", err, comments)
	}

	replies, err := c.PublicCommentReplies(ctx, "c1", "")
	if err != nil || len(replies.Comments) != 1 || replies.Comments[0].Text != "Thank you!" || replies.Bookmark != "rep-2" {
		t.Fatalf("PublicCommentReplies unexpected: err=%v, replies=%+v", err, replies)
	}

	// Verify AggregatedPinData base64 node_id decoding used by e1_ search pins.
	e1Pin, err := NormalizePin(json.RawMessage(`{"id":"e1_abc","title":"Roasted Carrots","aggregated_pin_data":{"node_id":"QWdncmVnYXRlZFBpbkRhdGE6NTExMjA1NDU0OTI4NzYzMzA5OA=="},"pin_join":{"visual_annotation":["Roasted carrots","Sheet pan veggies"]}}`))
	if err != nil || e1Pin.AggregatedPinID != "5112054549287633098" || len(e1Pin.VisualTags) != 2 {
		t.Fatalf("expected base64 node_id & visual_annotation decoding, got %+v (err=%v)", e1Pin, err)
	}

	ideas, err := c.PublicIdeas(ctx, "resin-crafts", "908182459161", "")
	if err != nil || len(ideas.Results) != 1 || ideas.Results[0].ID != "p9" || ideas.Bookmark != "idea-2" {
		t.Fatalf("PublicIdeas unexpected: err=%v, ideas=%+v", err, ideas)
	}
}

func TestErrorClassifications(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		status   int
		ctype    string
		retry    string
		body     string
		wantCode ErrorCode
	}{
		{
			name:     "403 denied",
			status:   403,
			ctype:    "application/json",
			body:     `{"message":"forbidden"}`,
			wantCode: CodeGuestAccessBlocked,
		},
		{
			name:     "429 rate limited",
			status:   429,
			ctype:    "application/json",
			retry:    "60",
			body:     `{"message":"slow down"}`,
			wantCode: CodeRateLimited,
		},
		{
			name:     "200 html challenge",
			status:   200,
			ctype:    "text/html; charset=utf-8",
			body:     `<html><body>captcha</body></html>`,
			wantCode: CodeChallengeRequired,
		},
		{
			name:     "invalid envelope",
			status:   200,
			ctype:    "application/json",
			body:     `{"unexpected":true}`,
			wantCode: CodeProtocolError,
		},
		{
			name:     "resource status failure",
			status:   200,
			ctype:    "application/json",
			body:     `{"resource_response":{"status":"failure","message":"login required"}}`,
			wantCode: CodeGuestAccessBlocked,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newMockServerClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.retry != "" {
					w.Header().Set("Retry-After", tc.retry)
				}
				w.Header().Set("Content-Type", tc.ctype)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, false)

			_, err := c.PublicProfile(ctx, "puremichigan")
			pErr, ok := AsError(err)
			if !ok {
				t.Fatalf("expected *pinterest.Error, got %T (%v)", err, err)
			}
			if pErr.Code != tc.wantCode {
				t.Fatalf("expected code %s, got %s (%s)", tc.wantCode, pErr.Code, pErr.Message)
			}
			if tc.retry != "" && pErr.RetryAfter != tc.retry {
				t.Fatalf("expected RetryAfter=%s, got %s", tc.retry, pErr.RetryAfter)
			}
		})
	}
}

func TestMediaValidationSSRFAndHLSRewrite(t *testing.T) {
	validURLs := []string{
		"https://i.pinimg.com/originals/aa/bb/cc.jpg",
		"https://v1.pinimg.com/videos/iht/hls/master.m3u8",
		"https://i.pinimg.com:443/736x/12/34.webp",
	}
	for _, u := range validURLs {
		if _, err := ValidateMediaURL(u); err != nil {
			t.Fatalf("expected %q to be valid, got %v", u, err)
		}
	}

	invalidURLs := []string{
		"http://i.pinimg.com/a.jpg",
		"https://evil.example.com/a.jpg",
		"https://i.pinimg.com:8443/a.jpg",
		"https://user:pass@i.pinimg.com/a.jpg",
		"https://127.0.0.1/a.jpg",
	}
	for _, u := range invalidURLs {
		if _, err := ValidateMediaURL(u); err == nil {
			t.Fatalf("expected %q to be rejected", u)
		}
	}

	disallowedIPs := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.5.4",
		"192.168.1.10",
		"169.254.169.254",
		"100.64.0.1",
		"0.0.0.0",
		"::1",
		"fe80::1",
		"fc00::1",
	}
	for _, s := range disallowedIPs {
		if !IsDisallowedIP(net.ParseIP(s)) {
			t.Fatalf("expected IP %s to be disallowed", s)
		}
	}
	if IsDisallowedIP(net.ParseIP("151.101.1.1")) {
		t.Fatalf("expected public IP 151.101.1.1 to be allowed")
	}

	// Test redirect refusal in MediaFetcher.
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:8080/secret", http.StatusFound)
	}))
	defer redirectSrv.Close()

	mf := NewMediaFetcher(DefaultTimeout, DefaultUserAgent)
	mf.allowLoopbackMediaForTests = true
	_, err := mf.Fetch(context.Background(), redirectSrv.URL+"/img.jpg", "")
	if err == nil {
		t.Fatalf("expected redirect to be refused")
	}

	// Test HLS manifest rewriting & audio/mp4 (.cmfa) segment fetching.
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".cmfa") {
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("cmaf-audio-bytes"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer okSrv.Close()
	resp, err := mf.Fetch(context.Background(), okSrv.URL+"/ok.jpg", "")
	if err != nil {
		t.Fatalf("expected loopback test media fetch to succeed, got %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "jpeg-bytes" {
		t.Fatalf("unexpected media body: %q", string(body))
	}

	audioResp, err := mf.Fetch(context.Background(), okSrv.URL+"/track_audio.cmfa", "bytes=0-15")
	if err != nil {
		t.Fatalf("expected audio/mp4 (.cmfa) fetch to succeed, got %v", err)
	}
	defer audioResp.Body.Close()
	if audioResp.ContentType != "audio/mp4" {
		t.Fatalf("expected audio/mp4 content type, got %q", audioResp.ContentType)
	}

	base, _ := url.Parse("https://v1.pinimg.com/videos/iht/hls/master.m3u8")
	manifest := strings.Join([]string{
		"#EXTM3U",
		`#EXT-X-MEDIA:TYPE=AUDIO,URI="audio.m3u8"`,
		"720p/index.m3u8",
		"https://v1.pinimg.com/videos/iht/hls/seg0.ts",
		"https://evil.example.com/leak.ts",
	}, "\n")
	rewritten := RewriteHLSManifest(manifest, base, "/api/v1/media/proxy?url=")
	if !strings.Contains(rewritten, "/api/v1/media/proxy?url=https%3A%2F%2Fv1.pinimg.com%2Fvideos%2Fiht%2Fhls%2Faudio.m3u8") {
		t.Fatalf("expected audio URI attribute rewritten, got:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "/api/v1/media/proxy?url=https%3A%2F%2Fv1.pinimg.com%2Fvideos%2Fiht%2Fhls%2F720p%2Findex.m3u8") {
		t.Fatalf("expected relative playlist rewritten, got:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "# blocked non-allowlisted segment URI") || strings.Contains(rewritten, "evil.example.com") {
		t.Fatalf("expected external segment URL blocked, got:\n%s", rewritten)
	}

	// Ensure unused json import isn't left behind if any; verify json marshaling of Pin.
	var p Pin
	if _, err := json.Marshal(p); err != nil {
		t.Fatalf("json marshal Pin failed: %v", err)
	}
}

func TestExtractPinIDPreservesDoubleHyphenOpaqueIDs(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"181199584997439257", "181199584997439257"},
		{"meadows-necklace--181199584997439257", "181199584997439257"},
		{"e1_AfqPShx5RnTbTQyvkjDJ--ZmdoiIVjrQepld14FRe1aeJFhrUCNGaA", "e1_AfqPShx5RnTbTQyvkjDJ--ZmdoiIVjrQepld14FRe1aeJFhrUCNGaA"},
		{"banana-dock--e1_AfqPShx5RnTbTQyvkjDJ--ZmdoiIVjrQepld14FRe1aeJFhrUCNGaA", "e1_AfqPShx5RnTbTQyvkjDJ--ZmdoiIVjrQepld14FRe1aeJFhrUCNGaA"},
	}
	for _, tc := range cases {
		if got := ExtractPinID(tc.in); got != tc.want {
			t.Fatalf("ExtractPinID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
