package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/flinternet/flinternet/internal/pinterest"
)

func parseTemplates(templatesFS fs.FS) (*template.Template, error) {
	if templatesFS == nil {
		return nil, nil
	}
	funcMap := template.FuncMap{
		"proxyURL": func(u string) string {
			if p := pinterest.ProxyPath(u); p != "" {
				return p
			}
			return u
		},
		"formatCount": func(n int) string {
			if n >= 1_000_000 {
				return fmt.Sprintf("%.1fM", float64(n)/1_000_000.0)
			}
			if n >= 1_000 {
				return fmt.Sprintf("%.1fK", float64(n)/1_000.0)
			}
			return fmt.Sprintf("%d", n)
		},
		"formatDuration": func(sec float64) string {
			if sec <= 0 {
				return ""
			}
			total := int(sec + 0.5)
			return fmt.Sprintf("%d:%02d", total/60, total%60)
		},
	}
	return template.New("").Funcs(funcMap).ParseFS(templatesFS, "*.html")
}

type pageBase struct {
	Version       string
	Query         string
	Scope         string
	FilterGenAI   bool
	Tabs          []pinterest.SearchTab
	HasVideo      bool
	IsSafari      bool
	BaseOrigin    string
	OgURL         string
	OgTitle       string
	OgDescription string
	OgImage       string
	OgImageWidth  int
	OgImageHeight int
	OgImageAlt    string
	OgType        string
	OgVideo       string
	OgVideoWidth  int
	OgVideoHeight int
	TwitterCard   string
	PinTokens     map[string]string
}

func requestOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-Host"), ",", 2)[0])
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	scheme := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-Proto"), ",", 2)[0])
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host
}

func absoluteAssetURL(origin, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if proxied := pinterest.ProxyPath(trimmed); proxied != "" {
		trimmed = proxied
	}
	if strings.HasPrefix(trimmed, "/") && origin != "" {
		return origin + trimmed
	}
	return trimmed
}

func truncateMetaText(s string, maxRunes int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if maxRunes <= 0 {
		return cleaned
	}
	runes := []rune(cleaned)
	if len(runes) <= maxRunes {
		return cleaned
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func (p *pageBase) populateMeta(r *http.Request) {
	origin := requestOrigin(r)
	p.BaseOrigin = origin
	if r != nil && p.OgURL == "" {
		p.OgURL = origin + r.URL.RequestURI()
	}
	if p.OgDescription != "" {
		p.OgDescription = truncateMetaText(p.OgDescription, 220)
	}
	if p.OgImage == "" {
		p.OgImage = absoluteAssetURL(origin, "/static/android-chrome-512x512.png")
		p.OgImageWidth = 512
		p.OgImageHeight = 512
		if p.TwitterCard == "" {
			p.TwitterCard = "summary"
		}
	} else {
		p.OgImage = absoluteAssetURL(origin, p.OgImage)
		if p.TwitterCard == "" {
			p.TwitterCard = "summary_large_image"
		}
	}
	if p.OgVideo != "" {
		p.OgVideo = absoluteAssetURL(origin, p.OgVideo)
	}
}

type pinGridView struct {
	Query     string
	Scope     string
	IsSafari  bool
	Pins      []pinterest.Pin
	PinTokens map[string]string
}

type searchPageData struct {
	pageBase
	Pins     []pinterest.Pin
	Boards   []pinterest.Board
	Users    []pinterest.UserProfile
	Bookmark string
}

type pinPageData struct {
	pageBase
	Pin         pinterest.Pin
	RelatedPins []pinterest.Pin
	RelatedGrid pinGridView
}

type commentsPageData struct {
	Version    string
	PinID      string
	Aid        string
	RepliesFor string
	Result     *pinterest.CommentResult
}

type userPageData struct {
	pageBase
	Profile  *pinterest.UserProfile
	Tab      string
	Boards   []pinterest.Board
	Pins     []pinterest.Pin
	Bookmark string
}

type boardPageData struct {
	pageBase
	Username string
	Slug     string
	Board    *pinterest.Board
	Pins     []pinterest.Pin
	Bookmark string
}

type ideasPageData struct {
	pageBase
	Title      string
	Slug       string
	InterestID string
	Pins       []pinterest.Pin
	Boards     []pinterest.Board
	Bookmark   string
}

type errorPageData struct {
	pageBase
	Status  int
	Message string
	Detail  string
}

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if targetURL, ok := resolveDirectCDNRedirect(r.URL.Path, r.URL.RawQuery); ok {
		s.handleMediaProxyWithURL(w, r, targetURL)
		return
	}

	if r.URL.Path == "/favicon.ico" || strings.HasPrefix(r.URL.Path, "/static/") {
		s.serveEmbeddedStatic(w, r)
		return
	}

	if r.URL.Path == "/search.php" {
		if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
			s.handleBinternetAPI(w, r)
			return
		}
		s.handleSearchPage(w, r, firstNonEmptyQuery(r.URL.Query().Get("scope"), "pins"))
		return
	}

	// If no HTML templates were configured (e.g. minimal API-only tests), fall back to static index.html.
	if s.tmpl == nil {
		s.handleStaticFallback(w, r)
		return
	}

	trimmed := strings.Trim(path.Clean(r.URL.Path), "/")
	if trimmed == "" || trimmed == "." || trimmed == "index.html" || trimmed == "index.php" {
		if q := strings.TrimSpace(firstNonEmptyQuery(r.URL.Query().Get("q"), r.URL.Query().Get("query"))); q != "" {
			s.handleSearchPage(w, r, firstNonEmptyQuery(r.URL.Query().Get("scope"), "pins"))
			return
		}
		s.handleIndexPage(w, r)
		return
	}

	parts := strings.Split(trimmed, "/")
	for i := range parts {
		if decoded, err := url.PathUnescape(parts[i]); err == nil {
			parts[i] = decoded
		}
	}

	switch strings.ToLower(parts[0]) {
	case "legal":
		s.handleLegalPage(w, r)
	case "search":
		scope := "pins"
		if len(parts) >= 2 {
			scope = mapWebScope(parts[1])
		} else if qScope := r.URL.Query().Get("scope"); qScope != "" {
			scope = mapWebScope(qScope)
		}
		s.handleSearchPage(w, r, scope)
	case "pin":
		if len(parts) >= 3 && strings.EqualFold(parts[2], "comments") {
			s.handlePinCommentsPage(w, r, parts[1])
			return
		}
		if len(parts) >= 2 {
			s.handlePinPage(w, r, parts[1])
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	case "ideas":
		if len(parts) >= 3 {
			s.handleIdeasTopicPage(w, r, parts[1], parts[2])
			return
		}
		if len(parts) == 2 {
			s.handleSearchPageWithQuery(w, r, strings.ReplaceAll(parts[1], "-", " "), "pins")
			return
		}
		s.handleIdeasIndexPage(w, r)
	default:
		username := strings.TrimPrefix(parts[0], "@")
		if len(parts) == 1 {
			tab := firstNonEmptyQuery(r.URL.Query().Get("tab"), "boards")
			s.handleUserPage(w, r, username, tab)
			return
		}
		switch strings.ToLower(parts[1]) {
		case "_created":
			s.handleUserPage(w, r, username, "created")
		case "_saved", "pins", "more_ideas":
			s.handleUserPage(w, r, username, "pins")
		case "boards":
			s.handleUserPage(w, r, username, "boards")
		default:
			s.handleBoardPage(w, r, username, parts[1])
		}
	}
}

func (s *Server) serveEmbeddedStatic(w http.ResponseWriter, r *http.Request) {
	if s.staticFS == nil {
		http.NotFound(w, r)
		return
	}
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	clean = strings.TrimPrefix(clean, "static/")
	if info, err := fs.Stat(s.staticFS, clean); err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFileFS(w, r, s.staticFS, clean)
}

func (s *Server) handleStaticFallback(w http.ResponseWriter, r *http.Request) {
	if s.staticFS == nil {
		http.NotFound(w, r)
		return
	}
	cleanPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if cleanPath == "" || cleanPath == "." {
		cleanPath = "index.html"
	}
	if info, err := fs.Stat(s.staticFS, cleanPath); err == nil && !info.IsDir() {
		http.ServeFileFS(w, r, s.staticFS, cleanPath)
		return
	}
	if _, err := fs.Stat(s.staticFS, "index.html"); err == nil {
		http.ServeFileFS(w, r, s.staticFS, "index.html")
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleIndexPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, r, http.StatusOK, "index.html", &pageBase{
		Version:       Version,
		Scope:         "pins",
		OgTitle:       "Flinternet",
		OgDescription: "Search public Pinterest pins, videos, boards, and creators without login walls.",
		TwitterCard:   "summary",
	})
}

func (s *Server) handleLegalPage(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, r, http.StatusOK, "legal.html", &pageBase{
		Version:       Version,
		Scope:         "pins",
		OgTitle:       "Legal Notice | Flinternet",
		OgDescription: "Privacy and attribution notice for Flinternet.",
		TwitterCard:   "summary",
	})
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request, scope string) {
	q := strings.TrimSpace(firstNonEmptyQuery(r.URL.Query().Get("q"), r.URL.Query().Get("query")))
	if q == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if target, ok := resolveOmnibarRedirect(q); ok {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	s.handleSearchPageWithQuery(w, r, q, scope)
}

func applyLessAITabs(tabs []pinterest.SearchTab, query, scope string, active bool) []pinterest.SearchTab {
	out := make([]pinterest.SearchTab, 0, len(tabs)+1)
	found := false
	for _, tab := range tabs {
		t := tab
		if t.IsGenAIFilter || strings.EqualFold(t.Label, "Less AI") {
			t.IsGenAIFilter = true
			t.IsSelected = active
			t.FilterGenAI = !active
			if t.Query == "" {
				t.Query = query
			}
			if t.Scope == "" {
				t.Scope = scope
			}
			found = true
		}
		out = append(out, t)
	}
	if !found {
		lessTab := pinterest.SearchTab{
			Label:         "Less AI",
			Query:         query,
			Scope:         scope,
			IsGenAIFilter: true,
			FilterGenAI:   !active,
			IsSelected:    active,
		}
		out = append([]pinterest.SearchTab{lessTab}, out...)
	}
	return out
}

func (s *Server) handleSearchPageWithQuery(w http.ResponseWriter, r *http.Request, query, scope string) {
	scope = mapWebScope(scope)
	bookmark := r.URL.Query().Get("bookmark")
	filterGenAI := false
	if scope == string(pinterest.ScopePins) {
		filterGenAI = resolveLessAIPref(w, r)
	}
	allowUnverified := true
	if rawAllow := r.URL.Query().Get("allow_unverified"); rawAllow != "" {
		allowUnverified = parseBoolParam(rawAllow)
	}

	params := pinterest.SearchParams{
		Query:           query,
		Scope:           pinterest.SearchScope(scope),
		Bookmark:        bookmark,
		FilterGenAI:     filterGenAI,
		AllowUnverified: &allowUnverified,
	}

	data := searchPageData{
		pageBase: pageBase{
			Version:     Version,
			Query:       query,
			Scope:       scope,
			FilterGenAI: filterGenAI,
			IsSafari:    isSafariRequest(r),
			OgTitle:     fmt.Sprintf("%s | Flinternet", query),
		},
	}

	switch pinterest.SearchScope(scope) {
	case pinterest.ScopeBoards:
		page, cached, err := s.cachedSearchBoards(r, params)
		if err != nil {
			s.renderErrorPage(w, r, err, "Search failed")
			return
		}
		_ = cached
		data.Boards = page.Results
		data.Tabs = page.Tabs
		data.Bookmark = page.Bookmark
		data.OgDescription = fmt.Sprintf("%d Pinterest boards matching %q.", len(page.Results), query)
		if len(page.Results) > 0 {
			data.OgImage = page.Results[0].CoverURL
		}
	case pinterest.ScopeUsers:
		page, _, err := s.cachedSearchUsers(r, params)
		if err != nil {
			s.renderErrorPage(w, r, err, "Search failed")
			return
		}
		data.Users = page.Results
		data.Tabs = page.Tabs
		data.Bookmark = page.Bookmark
		data.TwitterCard = "summary"
		data.OgDescription = fmt.Sprintf("%d Pinterest creators matching %q.", len(page.Results), query)
		if len(page.Results) > 0 {
			data.OgImage = page.Results[0].AvatarURL
		}
	default:
		page, _, err := s.cachedSearchPins(r, params)
		if err != nil {
			s.renderErrorPage(w, r, err, "Search failed")
			return
		}
		data.Pins = page.Results
		data.Tabs = applyLessAITabs(page.Tabs, query, scope, filterGenAI)
		data.Bookmark = page.Bookmark
		data.HasVideo = pinsHaveVideo(page.Results)
		data.PinTokens = s.buildPinTokens(page.Results)
		data.OgDescription = fmt.Sprintf("%d Pinterest %s matching %q.", len(page.Results), scope, query)
		if len(page.Results) > 0 {
			first := page.Results[0]
			data.OgImage = firstNonEmptyQuery(first.FallbackImageURL, first.ImageURL)
			data.OgImageWidth = first.Width
			data.OgImageHeight = first.Height
			data.OgImageAlt = firstNonEmptyQuery(first.AltText, first.Title, query)
		}
	}

	s.renderTemplate(w, r, http.StatusOK, "search.html", &data)
}

func (s *Server) handlePinPage(w http.ResponseWriter, r *http.Request, rawID string) {
	pin, err := s.loadPinByID(r, rawID)
	if err != nil {
		s.renderErrorPage(w, r, err, "Pin not found")
		return
	}

	query := r.URL.Query().Get("q")
	scope := firstNonEmptyQuery(r.URL.Query().Get("s"), "pins")

	var related []pinterest.Pin
	relQuery := relatedSearchQuery(pin)
	if relQuery != "" {
		if page, _, relErr := s.cachedSearchPins(r, pinterest.SearchParams{Query: relQuery, Scope: pinterest.ScopePins}); relErr == nil {
			for _, item := range page.Results {
				if item.ID != pin.ID {
					related = append(related, item)
				}
				if len(related) >= 16 {
					break
				}
			}
		}
	}

	isSafari := isSafariRequest(r)
	hasVideo := pin.IsVideo || pinsHaveVideo(related)
	ogTitle := firstNonEmptyQuery(pin.Title, pin.RichTitle, pin.SEOTitle, pin.AltText)
	if ogTitle == "" {
		if pin.Pinner.FullName != "" {
			ogTitle = "Pin by " + pin.Pinner.FullName
		} else {
			ogTitle = "Pinterest Pin"
		}
	}
	ogDesc := firstNonEmptyQuery(pin.Description, pin.RichDescription, pin.AltText, strings.Join(pin.VisualTags, " · "))
	if ogDesc == "" && pin.Pinner.FullName != "" {
		ogDesc = "Shared by " + pin.Pinner.FullName
	}
	ogType := "website"
	var ogVideo string
	var ogVideoW, ogVideoH int
	if pin.IsVideo {
		ogType = "video.other"
		if pin.MP4VideoURL != "" {
			ogVideo = pin.MP4VideoURL
			ogVideoW = pin.Width
			ogVideoH = pin.Height
		}
	}

	s.renderTemplate(w, r, http.StatusOK, "pin.html", &pinPageData{
		pageBase: pageBase{
			Version:       Version,
			Query:         query,
			Scope:         scope,
			HasVideo:      hasVideo,
			IsSafari:      isSafari,
			OgTitle:       ogTitle,
			OgDescription: ogDesc,
			OgImage:       firstNonEmptyQuery(pin.FallbackImageURL, pin.ImageURL),
			OgImageWidth:  pin.Width,
			OgImageHeight: pin.Height,
			OgImageAlt:    firstNonEmptyQuery(pin.AltText, ogTitle),
			OgType:        ogType,
			OgVideo:       ogVideo,
			OgVideoWidth:  ogVideoW,
			OgVideoHeight: ogVideoH,
			TwitterCard:   "summary_large_image",
		},
		Pin:         pin,
		RelatedPins: related,
		RelatedGrid: pinGridView{
			Query:     query,
			Scope:     scope,
			IsSafari:  isSafari,
			Pins:      related,
			PinTokens: s.buildPinTokens(related),
		},
	})
}

func (s *Server) handlePinCommentsPage(w http.ResponseWriter, r *http.Request, rawID string) {
	pinID := pinterest.ExtractPinID(rawID)
	q := r.URL.Query()
	aid := q.Get("aid")
	bookmark := q.Get("bookmark")
	repliesFor := strings.TrimSpace(q.Get("replies_for"))

	if aid == "" {
		if pin, err := s.loadPinByID(r, pinID); err == nil && pin.AggregatedPinID != "" {
			aid = pin.AggregatedPinID
		}
	}

	var (
		res *pinterest.CommentResult
		err error
	)
	if repliesFor != "" {
		res, err = s.client.PublicCommentReplies(r.Context(), repliesFor, bookmark)
	} else {
		res, err = s.client.PublicPinComments(r.Context(), pinID, aid, bookmark)
	}
	if err != nil {
		s.renderErrorPage(w, r, err, "Unable to load comments")
		return
	}
	if aid == "" {
		aid = res.AggregatedPinID
	}
	s.renderTemplate(w, r, http.StatusOK, "comments.html", &commentsPageData{
		Version:    Version,
		PinID:      pinID,
		Aid:        aid,
		RepliesFor: repliesFor,
		Result:     res,
	})
}

func (s *Server) handleUserPage(w http.ResponseWriter, r *http.Request, username, tab string) {
	profile, _, err := s.loadProfile(r.Context(), username)
	if err != nil {
		s.renderErrorPage(w, r, err, "Profile not found")
		return
	}
	bookmark := r.URL.Query().Get("bookmark")
	allowUnverified := true

	ogDesc := firstNonEmptyQuery(profile.About, fmt.Sprintf("%d followers · %d pins · %d boards on Flinternet", profile.FollowerCount, profile.PinCount, profile.BoardCount))
	data := userPageData{
		pageBase: pageBase{
			Version:       Version,
			Query:         r.URL.Query().Get("q"),
			Scope:         firstNonEmptyQuery(r.URL.Query().Get("s"), "users"),
			IsSafari:      isSafariRequest(r),
			OgTitle:       fmt.Sprintf("%s (@%s) | Flinternet", profile.FullName, profile.Username),
			OgDescription: ogDesc,
			OgImage:       profile.AvatarURL,
			OgImageAlt:    profile.FullName,
			OgType:        "profile",
			TwitterCard:   "summary",
		},
		Profile: profile,
		Tab:     tab,
	}

	switch tab {
	case "created":
		if profile.ID == "" {
			s.renderErrorPage(w, r, fmt.Errorf("profile missing user_id"), "Created pins unavailable")
			return
		}
		page, err := s.client.PublicCreatedPins(r.Context(), profile.Username, profile.ID, 25, bookmark, &allowUnverified)
		if err != nil {
			s.renderErrorPage(w, r, err, "Failed to load created pins")
			return
		}
		s.indexPins(page.Results)
		data.Pins = page.Results
		data.Bookmark = page.Bookmark
		data.HasVideo = pinsHaveVideo(page.Results)
		data.PinTokens = s.buildPinTokens(page.Results)
	case "pins":
		page, err := s.client.PublicProfilePins(r.Context(), profile.Username, 25, bookmark, &allowUnverified)
		if err != nil {
			s.renderErrorPage(w, r, err, "Failed to load saved pins")
			return
		}
		s.indexPins(page.Results)
		data.Pins = page.Results
		data.Bookmark = page.Bookmark
		data.HasVideo = pinsHaveVideo(page.Results)
		data.PinTokens = s.buildPinTokens(page.Results)
	default:
		data.Tab = "boards"
		page, err := s.client.PublicProfileBoards(r.Context(), profile.Username, bookmark)
		if err != nil {
			s.renderErrorPage(w, r, err, "Failed to load user boards")
			return
		}
		data.Boards = page.Results
		data.Bookmark = page.Bookmark
	}

	s.renderTemplate(w, r, http.StatusOK, "user.html", &data)
}

func (s *Server) handleBoardPage(w http.ResponseWriter, r *http.Request, username, slug string) {
	board, _, err := s.loadBoard(r.Context(), username, slug)
	if err != nil {
		s.renderErrorPage(w, r, err, "Board not found")
		return
	}
	bookmark := r.URL.Query().Get("bookmark")
	allowUnverified := true
	sourceURL := fmt.Sprintf("/%s/%s/", url.PathEscape(username), url.PathEscape(slug))

	page, err := s.client.PublicBoardPins(r.Context(), board.ID, sourceURL, 25, bookmark, &allowUnverified)
	if err != nil {
		s.renderErrorPage(w, r, err, "Failed to load board pins")
		return
	}
	s.indexPins(page.Results)

	ogImage := board.CoverURL
	if ogImage == "" && len(page.Results) > 0 {
		ogImage = firstNonEmptyQuery(page.Results[0].FallbackImageURL, page.Results[0].ImageURL)
	}

	s.renderTemplate(w, r, http.StatusOK, "board.html", &boardPageData{
		pageBase: pageBase{
			Version:       Version,
			Query:         r.URL.Query().Get("q"),
			Scope:         firstNonEmptyQuery(r.URL.Query().Get("s"), "boards"),
			IsSafari:      isSafariRequest(r),
			HasVideo:      pinsHaveVideo(page.Results),
			PinTokens:     s.buildPinTokens(page.Results),
			OgTitle:       fmt.Sprintf("%s (@%s) | Flinternet", board.Name, username),
			OgDescription: firstNonEmptyQuery(board.Description, fmt.Sprintf("%d pins on Flinternet", board.PinCount)),
			OgImage:       ogImage,
			OgImageAlt:    board.Name,
			TwitterCard:   "summary_large_image",
		},
		Username: username,
		Slug:     slug,
		Board:    board,
		Pins:     page.Results,
		Bookmark: page.Bookmark,
	})
}

func (s *Server) handleIdeasIndexPage(w http.ResponseWriter, r *http.Request) {
	page, err := s.client.PublicProfileBoards(r.Context(), "pinterest", r.URL.Query().Get("bookmark"))
	if err != nil {
		s.renderErrorPage(w, r, err, "Failed to load ideas")
		return
	}
	var ogImage string
	if len(page.Results) > 0 {
		ogImage = page.Results[0].CoverURL
	}
	s.renderTemplate(w, r, http.StatusOK, "ideas.html", &ideasPageData{
		pageBase: pageBase{
			Version:       Version,
			Scope:         "pins",
			OgTitle:       "Explore Ideas | Flinternet",
			OgDescription: "Browse curated Pinterest boards and visual ideas.",
			OgImage:       ogImage,
		},
		Title:    "Explore Ideas",
		Boards:   page.Results,
		Bookmark: page.Bookmark,
	})
}

func (s *Server) handleIdeasTopicPage(w http.ResponseWriter, r *http.Request, slug, interestID string) {
	bookmark := r.URL.Query().Get("bookmark")
	page, err := s.client.PublicIdeas(r.Context(), slug, interestID, bookmark)
	if err != nil {
		s.renderErrorPage(w, r, err, "Failed to load topic ideas")
		return
	}
	s.indexPins(page.Results)
	title := strings.ReplaceAll(slug, "-", " ")
	var (
		ogImage string
		ogW     int
		ogH     int
	)
	if len(page.Results) > 0 {
		first := page.Results[0]
		ogImage = firstNonEmptyQuery(first.FallbackImageURL, first.ImageURL)
		ogW = first.Width
		ogH = first.Height
	}
	s.renderTemplate(w, r, http.StatusOK, "ideas.html", &ideasPageData{
		pageBase: pageBase{
			Version:       Version,
			Query:         title,
			Scope:         "pins",
			IsSafari:      isSafariRequest(r),
			HasVideo:      pinsHaveVideo(page.Results),
			PinTokens:     s.buildPinTokens(page.Results),
			OgTitle:       fmt.Sprintf("%s | Flinternet", title),
			OgDescription: fmt.Sprintf("%d curated Pinterest ideas for %s.", len(page.Results), title),
			OgImage:       ogImage,
			OgImageWidth:  ogW,
			OgImageHeight: ogH,
			OgImageAlt:    title,
		},
		Title:      title,
		Slug:       slug,
		InterestID: interestID,
		Pins:       page.Results,
		Bookmark:   page.Bookmark,
	})
}

func (s *Server) renderTemplate(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	if m, ok := data.(interface{ populateMeta(*http.Request) }); ok && r != nil {
		m.populateMeta(r)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("template execution failed", "template", name, "error", err)
	}
}

func (s *Server) renderErrorPage(w http.ResponseWriter, r *http.Request, err error, heading string) {
	status := http.StatusBadGateway
	detail := err.Error()
	if pErr, ok := pinterest.AsError(err); ok {
		status = pErr.HTTPStatus()
		detail = fmt.Sprintf("[%s] %s", pErr.Code, pErr.Message)
		if pErr.RetryAfter != "" {
			w.Header().Set("Retry-After", pErr.RetryAfter)
			detail += " (Retry-After: " + pErr.RetryAfter + ")"
		}
	}
	if s.tmpl == nil {
		http.Error(w, heading+": "+detail, status)
		return
	}
	s.renderTemplate(w, r, status, "error.html", &errorPageData{
		pageBase: pageBase{
			Version:     Version,
			Scope:       "pins",
			IsSafari:    isSafariRequest(r),
			OgTitle:     fmt.Sprintf("Error %d | Flinternet", status),
			TwitterCard: "summary",
		},
		Status:  status,
		Message: heading,
		Detail:  detail,
	})
}

func mapWebScope(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "videos":
		return "videos"
	case "boards":
		return "boards"
	case "users", "people":
		return "users"
	default:
		return "pins"
	}
}

func resolveOmnibarRedirect(q string) (string, bool) {
	if strings.HasPrefix(q, "@") {
		user := strings.Trim(strings.TrimPrefix(q, "@"), "/")
		if user != "" && !strings.Contains(user, " ") {
			return "/" + url.PathEscape(user) + "/", true
		}
	}
	if strings.Contains(q, "pinterest.com/") {
		rawURL := q
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			rawURL = "https://" + rawURL
		}
		if u, err := url.Parse(rawURL); err == nil {
			return u.RequestURI(), true
		}
	}
	if !strings.ContainsAny(q, " ?#") && strings.Count(q, "/") == 1 {
		parts := strings.SplitN(q, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			return "/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/", true
		}
	}
	return "", false
}

func isSafariRequest(r *http.Request) bool {
	ua := r.Header.Get("User-Agent")
	return strings.Contains(ua, "Safari") &&
		!strings.Contains(ua, "Chrome") &&
		!strings.Contains(ua, "Chromium") &&
		!strings.Contains(ua, "Edg")
}

func pinsHaveVideo(pins []pinterest.Pin) bool {
	for _, p := range pins {
		if p.IsVideo {
			return true
		}
	}
	return false
}
