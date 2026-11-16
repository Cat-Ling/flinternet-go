// Package server exposes the Flinternet REST API, SSRF-safe media proxy,
// Binternet compatibility endpoints, and embedded multi-device web UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flinternet/flinternet/internal/cache"
	"github.com/flinternet/flinternet/internal/pinterest"
)

// Version can be overridden at link time via -ldflags "-X github.com/flinternet/flinternet/internal/server.Version=...".
var Version = "1.0.0"

const (
	defaultCacheItems = 256
	defaultPinCache   = 4096
)

// Config controls HTTP server behavior and caching.
type Config struct {
	Client      *pinterest.Client
	StaticFS    fs.FS
	TemplatesFS fs.FS
	CacheTTL    time.Duration
	ShareKey    string
	Logger      *slog.Logger
}

// Server serves the Flinternet API and embedded web application.
type Server struct {
	client     *pinterest.Client
	staticFS   fs.FS
	tmpl       *template.Template
	cacheTTL   time.Duration
	shareCodec *shareCodec
	logger     *slog.Logger
	mux        *http.ServeMux

	searchPinsCache    *cache.Cache[string, *pinterest.PageResult[pinterest.Pin]]
	searchBoardsCache  *cache.Cache[string, *pinterest.PageResult[pinterest.Board]]
	searchUsersCache   *cache.Cache[string, *pinterest.PageResult[pinterest.UserProfile]]
	suggestCache       *cache.Cache[string, []pinterest.Suggestion]
	tabsCache          *cache.Cache[string, []pinterest.SearchTab]
	profileCache       *cache.Cache[string, *pinterest.UserProfile]
	profileBoardsCache *cache.Cache[string, *pinterest.PageResult[pinterest.Board]]
	profilePinsCache   *cache.Cache[string, *pinterest.PageResult[pinterest.Pin]]
	createdPinsCache   *cache.Cache[string, *pinterest.PageResult[pinterest.Pin]]
	boardCache         *cache.Cache[string, *pinterest.Board]
	boardPinsCache     *cache.Cache[string, *pinterest.PageResult[pinterest.Pin]]
	ideasCache         *cache.Cache[string, *pinterest.PageResult[pinterest.Pin]]
	pinCache           *cache.Cache[string, pinterest.Pin]
	hlsCache           *cache.Cache[string, string]
	negCorrCache       *cache.Cache[string, struct{}]
	corrFlight         singleflightGroup[*pinterest.PageResult[pinterest.Pin]]
}

// New constructs a configured Server ready to handle HTTP traffic.
func New(cfg Config) (*Server, error) {
	if cfg.Client == nil {
		return nil, errors.New("pinterest client is required")
	}
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	tmpl, err := parseTemplates(cfg.TemplatesFS)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML templates: %w", err)
	}

	codec, err := newShareCodec(cfg.ShareKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize share key codec: %w", err)
	}

	s := &Server{
		client:             cfg.Client,
		staticFS:           cfg.StaticFS,
		tmpl:               tmpl,
		cacheTTL:           ttl,
		shareCodec:         codec,
		logger:             logger,
		mux:                http.NewServeMux(),
		searchPinsCache:    cache.New[string, *pinterest.PageResult[pinterest.Pin]](defaultCacheItems, ttl),
		searchBoardsCache:  cache.New[string, *pinterest.PageResult[pinterest.Board]](defaultCacheItems, ttl),
		searchUsersCache:   cache.New[string, *pinterest.PageResult[pinterest.UserProfile]](defaultCacheItems, ttl),
		suggestCache:       cache.New[string, []pinterest.Suggestion](defaultCacheItems, ttl),
		tabsCache:          cache.New[string, []pinterest.SearchTab](defaultCacheItems, ttl),
		profileCache:       cache.New[string, *pinterest.UserProfile](defaultCacheItems, ttl),
		profileBoardsCache: cache.New[string, *pinterest.PageResult[pinterest.Board]](defaultCacheItems, ttl),
		profilePinsCache:   cache.New[string, *pinterest.PageResult[pinterest.Pin]](defaultCacheItems, ttl),
		createdPinsCache:   cache.New[string, *pinterest.PageResult[pinterest.Pin]](defaultCacheItems, ttl),
		boardCache:         cache.New[string, *pinterest.Board](defaultCacheItems, ttl),
		boardPinsCache:     cache.New[string, *pinterest.PageResult[pinterest.Pin]](defaultCacheItems, ttl),
		ideasCache:         cache.New[string, *pinterest.PageResult[pinterest.Pin]](defaultCacheItems, ttl),
		pinCache:           cache.New[string, pinterest.Pin](defaultPinCache, 30*time.Minute),
		hlsCache:           cache.New[string, string](defaultCacheItems, ttl),
		negCorrCache:       cache.New[string, struct{}](defaultCacheItems, 2*time.Minute),
	}
	s.registerRoutes()
	return s, nil
}

// Handler returns the root HTTP handler with security headers.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/endpoints", s.handleEndpoints)
	s.mux.HandleFunc("GET /api/v1/search", s.handleSearch)
	s.mux.HandleFunc("POST /api/v1/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/v1/suggest", s.handleSuggest)
	s.mux.HandleFunc("GET /api/v1/search/tabs", s.handleSearchTabs)

	s.mux.HandleFunc("GET /api/v1/users/{username}", s.handleUserProfile)
	s.mux.HandleFunc("GET /api/v1/users/{username}/boards", s.handleUserBoards)
	s.mux.HandleFunc("GET /api/v1/users/{username}/pins", s.handleUserPins)
	s.mux.HandleFunc("GET /api/v1/users/{username}/created", s.handleUserCreatedPins)

	s.mux.HandleFunc("GET /api/v1/boards/{username}/{slug}", s.handleBoard)
	s.mux.HandleFunc("GET /api/v1/boards/{username}/{slug}/pins", s.handleBoardPins)
	s.mux.HandleFunc("GET /api/v1/board-feed", s.handleBoardFeed)

	s.mux.HandleFunc("GET /api/v1/pins/{id}", s.handlePinDetail)
	s.mux.HandleFunc("GET /api/v1/pins/{id}/comments", s.handlePinCommentsAPI)
	s.mux.HandleFunc("GET /api/v1/comments/{id}/replies", s.handleCommentRepliesAPI)
	s.mux.HandleFunc("GET /api/v1/ideas/{slug}/{id}", s.handleIdeasAPI)
	s.mux.HandleFunc("GET /api/v1/media/proxy", s.handleMediaProxy)

	// Binternet compatibility routes.
	s.mux.HandleFunc("GET /api.php", s.handleBinternetAPI)
	s.mux.HandleFunc("GET /image_proxy.php", s.handleMediaProxy)

	s.mux.HandleFunc("/api/", s.handleAPINotFound)
	s.mux.HandleFunc("/", s.handleWeb)
}

type apiErrorBody struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	RetryAfter  string `json:"retry_after,omitempty"`
}

type apiErrorEnvelope struct {
	Error apiErrorBody `json:"error"`
}

type healthResponse struct {
	Status                    string `json:"status"`
	Version                   string `json:"version"`
	BaseURL                   string `json:"base_url"`
	AllowUnverifiedPagination bool   `json:"allow_unverified_pagination"`
	CacheTTLSeconds           int64  `json:"cache_ttl_seconds"`
	AuthMode                  string `json:"auth_mode"`
}

type endpointsResponse struct {
	Count     int                      `json:"count"`
	Endpoints []pinterest.EndpointSpec `json:"endpoints"`
}

type searchResponse struct {
	Query         string                  `json:"query"`
	Scope         pinterest.SearchScope   `json:"scope"`
	Endpoint      string                  `json:"endpoint"`
	EvidenceGrade string                  `json:"evidence_grade,omitempty"`
	Bookmark      string                  `json:"bookmark,omitempty"`
	Cached        bool                    `json:"cached"`
	Count         int                     `json:"count"`
	Pins          []pinterest.Pin         `json:"pins,omitempty"`
	Boards        []pinterest.Board       `json:"boards,omitempty"`
	Users         []pinterest.UserProfile `json:"users,omitempty"`
	Tabs          []pinterest.SearchTab   `json:"tabs,omitempty"`
	Raw           json.RawMessage         `json:"raw,omitempty"`
}

type suggestResponse struct {
	Term        string                 `json:"term"`
	Scope       string                 `json:"scope"`
	Cached      bool                   `json:"cached"`
	Suggestions []pinterest.Suggestion `json:"suggestions"`
}

type searchTabsResponse struct {
	Query  string                `json:"query"`
	Scope  string                `json:"scope"`
	Cached bool                  `json:"cached"`
	Tabs   []pinterest.SearchTab `json:"tabs"`
}

type profileResponse struct {
	Profile *pinterest.UserProfile `json:"profile"`
	Cached  bool                   `json:"cached"`
}

type boardsResponse struct {
	Username      string            `json:"username"`
	Endpoint      string            `json:"endpoint"`
	EvidenceGrade string            `json:"evidence_grade,omitempty"`
	Bookmark      string            `json:"bookmark,omitempty"`
	Cached        bool              `json:"cached"`
	Count         int               `json:"count"`
	Boards        []pinterest.Board `json:"boards"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
}

type pinsResponse struct {
	Subject       string          `json:"subject"`
	Endpoint      string          `json:"endpoint"`
	EvidenceGrade string          `json:"evidence_grade,omitempty"`
	Bookmark      string          `json:"bookmark,omitempty"`
	Cached        bool            `json:"cached"`
	Count         int             `json:"count"`
	Pins          []pinterest.Pin `json:"pins"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type boardResponse struct {
	Board  *pinterest.Board `json:"board"`
	Cached bool             `json:"cached"`
}

type pinDetailResponse struct {
	Pin     pinterest.Pin   `json:"pin"`
	Related []pinterest.Pin `json:"related,omitempty"`
}

type searchRequestBody struct {
	Query           string                `json:"q"`
	Scope           pinterest.SearchScope `json:"scope"`
	PageSize        *int                  `json:"page_size"`
	RS              string                `json:"rs"`
	Bookmark        string                `json:"bookmark"`
	AllowUnverified *bool                 `json:"allow_unverified"`
	Raw             bool                  `json:"raw"`
}

type binternetResponse struct {
	Status   string          `json:"status"`
	Query    string          `json:"query"`
	Scope    string          `json:"scope"`
	Bookmark string          `json:"bookmark,omitempty"`
	Count    int             `json:"count"`
	Results  []pinterest.Pin `json:"results"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:                    "ok",
		Version:                   Version,
		BaseURL:                   s.client.BaseURL(),
		AllowUnverifiedPagination: s.client.AllowUnverifiedPagination(),
		CacheTTLSeconds:           int64(s.cacheTTL.Seconds()),
		AuthMode:                  "guest-anonymous-only",
	})
}

func (s *Server) handleEndpoints(w http.ResponseWriter, _ *http.Request) {
	catalog := pinterest.EndpointCatalog()
	writeJSON(w, http.StatusOK, endpointsResponse{
		Count:     len(catalog),
		Endpoints: catalog,
	})
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "NOT_FOUND", "API endpoint does not exist", 0, "", "")
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	reqParams, includeRaw, err := parseSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), err.Error(), 0, "", "")
		return
	}
	if reqParams.Scope == "" {
		reqParams.Scope = pinterest.ScopePins
	}
	if !pinterest.ValidSearchScope(reqParams.Scope) {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "scope must be pins, videos, boards, or users", 0, "", "")
		return
	}
	if strings.TrimSpace(reqParams.Query) == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "query parameter q is required", 0, "", "")
		return
	}

	switch reqParams.Scope {
	case pinterest.ScopePins, pinterest.ScopeVideos:
		page, cached, err := s.cachedSearchPins(r, reqParams)
		if err != nil {
			writePinterestError(w, err)
			return
		}
		s.writeSearchPinsResponse(w, reqParams.Query, reqParams.Scope, page, cached, includeRaw)

	case pinterest.ScopeBoards:
		page, cached, err := s.cachedSearchBoards(r, reqParams)
		if err != nil {
			writePinterestError(w, err)
			return
		}
		s.writeSearchBoardsResponse(w, reqParams.Query, page, cached, includeRaw)

	case pinterest.ScopeUsers:
		page, cached, err := s.cachedSearchUsers(r, reqParams)
		if err != nil {
			writePinterestError(w, err)
			return
		}
		s.writeSearchUsersResponse(w, reqParams.Query, page, cached, includeRaw)
	}
}

func (s *Server) writeSearchPinsResponse(w http.ResponseWriter, query string, scope pinterest.SearchScope, page *pinterest.PageResult[pinterest.Pin], cached, includeRaw bool) {
	pins := stripPinRawSlice(page.Results, includeRaw)
	var raw json.RawMessage
	if includeRaw {
		raw = page.Raw
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Query:         query,
		Scope:         scope,
		Endpoint:      page.Endpoint,
		EvidenceGrade: page.EvidenceGrade,
		Bookmark:      page.Bookmark,
		Cached:        cached,
		Count:         len(pins),
		Pins:          pins,
		Tabs:          stripTabRawSlice(page.Tabs, includeRaw),
		Raw:           raw,
	})
}

func (s *Server) writeSearchBoardsResponse(w http.ResponseWriter, query string, page *pinterest.PageResult[pinterest.Board], cached, includeRaw bool) {
	boards := stripBoardRawSlice(page.Results, includeRaw)
	var raw json.RawMessage
	if includeRaw {
		raw = page.Raw
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Query:         query,
		Scope:         pinterest.ScopeBoards,
		Endpoint:      page.Endpoint,
		EvidenceGrade: page.EvidenceGrade,
		Bookmark:      page.Bookmark,
		Cached:        cached,
		Count:         len(boards),
		Boards:        boards,
		Tabs:          stripTabRawSlice(page.Tabs, includeRaw),
		Raw:           raw,
	})
}

func (s *Server) writeSearchUsersResponse(w http.ResponseWriter, query string, page *pinterest.PageResult[pinterest.UserProfile], cached, includeRaw bool) {
	users := stripUserRawSlice(page.Results, includeRaw)
	var raw json.RawMessage
	if includeRaw {
		raw = page.Raw
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Query:         query,
		Scope:         pinterest.ScopeUsers,
		Endpoint:      page.Endpoint,
		EvidenceGrade: page.EvidenceGrade,
		Bookmark:      page.Bookmark,
		Cached:        cached,
		Count:         len(users),
		Users:         users,
		Tabs:          stripTabRawSlice(page.Tabs, includeRaw),
		Raw:           raw,
	})
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	term := strings.TrimSpace(firstNonEmptyQuery(q.Get("term"), q.Get("q")))
	scope := firstNonEmptyQuery(q.Get("scope"), "pins")
	count := parsePositiveInt(q.Get("count"), 6, 25)
	includeRaw := parseBoolParam(q.Get("raw"))

	if term == "" {
		writeJSON(w, http.StatusOK, suggestResponse{
			Term:        "",
			Scope:       scope,
			Suggestions: []pinterest.Suggestion{},
		})
		return
	}

	cacheKey := fmt.Sprintf("%s|%s|%d", term, scope, count)
	if cached, ok := s.suggestCache.Get(cacheKey); ok {
		writeJSON(w, http.StatusOK, suggestResponse{
			Term:        term,
			Scope:       scope,
			Cached:      true,
			Suggestions: stripSuggestionRawSlice(cached, includeRaw),
		})
		return
	}

	suggestions, err := s.client.Suggest(r.Context(), term, scope, count)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.suggestCache.Set(cacheKey, suggestions)
	writeJSON(w, http.StatusOK, suggestResponse{
		Term:        term,
		Scope:       scope,
		Cached:      false,
		Suggestions: stripSuggestionRawSlice(suggestions, includeRaw),
	})
}

func (s *Server) handleSearchTabs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(firstNonEmptyQuery(q.Get("q"), q.Get("query")))
	scope := firstNonEmptyQuery(q.Get("scope"), "pins")
	includeRaw := parseBoolParam(q.Get("raw"))

	if query == "" {
		writeJSON(w, http.StatusOK, searchTabsResponse{
			Query: "",
			Scope: scope,
			Tabs:  []pinterest.SearchTab{},
		})
		return
	}

	cacheKey := query + "|" + scope
	if cached, ok := s.tabsCache.Get(cacheKey); ok {
		writeJSON(w, http.StatusOK, searchTabsResponse{
			Query:  query,
			Scope:  scope,
			Cached: true,
			Tabs:   stripTabRawSlice(cached, includeRaw),
		})
		return
	}

	tabs, err := s.client.SearchTabs(r.Context(), query, scope)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.tabsCache.Set(cacheKey, tabs)
	writeJSON(w, http.StatusOK, searchTabsResponse{
		Query:  query,
		Scope:  scope,
		Cached: false,
		Tabs:   stripTabRawSlice(tabs, includeRaw),
	})
}

func (s *Server) handleUserProfile(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	includeRaw := parseBoolParam(r.URL.Query().Get("raw"))
	profile, cached, err := s.loadProfile(r.Context(), username)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	copyProfile := *profile
	if !includeRaw {
		copyProfile.Raw = nil
	}
	writeJSON(w, http.StatusOK, profileResponse{
		Profile: &copyProfile,
		Cached:  cached,
	})
}

func (s *Server) handleUserBoards(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	bookmark := r.URL.Query().Get("bookmark")
	includeRaw := parseBoolParam(r.URL.Query().Get("raw"))

	cacheKey := strings.ToLower(strings.TrimPrefix(username, "@")) + "|" + bookmark
	if page, ok := s.profileBoardsCache.Get(cacheKey); ok {
		s.writeBoardsResponse(w, username, page, true, includeRaw)
		return
	}

	page, err := s.client.PublicProfileBoards(r.Context(), username, bookmark)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.profileBoardsCache.Set(cacheKey, page)
	s.writeBoardsResponse(w, username, page, false, includeRaw)
}

func (s *Server) writeBoardsResponse(w http.ResponseWriter, username string, page *pinterest.PageResult[pinterest.Board], cached, includeRaw bool) {
	boards := stripBoardRawSlice(page.Results, includeRaw)
	var raw json.RawMessage
	if includeRaw {
		raw = page.Raw
	}
	writeJSON(w, http.StatusOK, boardsResponse{
		Username:      strings.TrimPrefix(username, "@"),
		Endpoint:      page.Endpoint,
		EvidenceGrade: page.EvidenceGrade,
		Bookmark:      page.Bookmark,
		Cached:        cached,
		Count:         len(boards),
		Boards:        boards,
		Raw:           raw,
	})
}

func (s *Server) handleUserPins(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	q := r.URL.Query()
	pageSize := parsePositiveInt(q.Get("page_size"), 25, 100)
	bookmark := q.Get("bookmark")
	allowUnverified := parseOptionalBoolParam(q.Get("allow_unverified"))
	includeRaw := parseBoolParam(q.Get("raw"))

	cacheKey := fmt.Sprintf("%s|%d|%s", strings.ToLower(strings.TrimPrefix(username, "@")), pageSize, bookmark)
	if page, ok := s.profilePinsCache.Get(cacheKey); ok {
		s.writePinsResponse(w, username, page, true, includeRaw)
		return
	}

	page, err := s.client.PublicProfilePins(r.Context(), username, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.indexPins(page.Results)
	s.profilePinsCache.Set(cacheKey, page)
	s.writePinsResponse(w, username, page, false, includeRaw)
}

func (s *Server) handleUserCreatedPins(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	q := r.URL.Query()
	userID := strings.TrimSpace(q.Get("user_id"))
	pageSize := parsePositiveInt(q.Get("page_size"), 25, 100)
	bookmark := q.Get("bookmark")
	allowUnverified := parseOptionalBoolParam(q.Get("allow_unverified"))
	includeRaw := parseBoolParam(q.Get("raw"))

	if userID == "" {
		profile, _, err := s.loadProfile(r.Context(), username)
		if err != nil {
			writePinterestError(w, err)
			return
		}
		if profile.ID == "" {
			writeError(w, http.StatusBadGateway, string(pinterest.CodeProtocolError), "public profile did not include a user_id for UserActivityPinsResource", 0, "", "")
			return
		}
		userID = profile.ID
	}

	cacheKey := fmt.Sprintf("%s|%s|%d|%s", strings.ToLower(strings.TrimPrefix(username, "@")), userID, pageSize, bookmark)
	if page, ok := s.createdPinsCache.Get(cacheKey); ok {
		s.writePinsResponse(w, username, page, true, includeRaw)
		return
	}

	page, err := s.client.PublicCreatedPins(r.Context(), username, userID, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.indexPins(page.Results)
	s.createdPinsCache.Set(cacheKey, page)
	s.writePinsResponse(w, username, page, false, includeRaw)
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	slug := r.PathValue("slug")
	includeRaw := parseBoolParam(r.URL.Query().Get("raw"))

	board, cached, err := s.loadBoard(r.Context(), username, slug)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	copyBoard := *board
	if !includeRaw {
		copyBoard.Raw = nil
	}
	writeJSON(w, http.StatusOK, boardResponse{
		Board:  &copyBoard,
		Cached: cached,
	})
}

func (s *Server) handleBoardPins(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	slug := r.PathValue("slug")
	q := r.URL.Query()
	boardID := strings.TrimSpace(q.Get("board_id"))
	pageSize := parsePositiveInt(q.Get("page_size"), 25, 100)
	bookmark := q.Get("bookmark")
	allowUnverified := parseOptionalBoolParam(q.Get("allow_unverified"))
	includeRaw := parseBoolParam(q.Get("raw"))

	if boardID == "" {
		board, _, err := s.loadBoard(r.Context(), username, slug)
		if err != nil {
			writePinterestError(w, err)
			return
		}
		if board.ID == "" {
			writeError(w, http.StatusBadGateway, string(pinterest.CodeProtocolError), "board metadata did not include a board_id", 0, "", "")
			return
		}
		boardID = board.ID
	}

	sourceURL := fmt.Sprintf("/%s/%s/", url.PathEscape(strings.TrimPrefix(username, "@")), url.PathEscape(slug))
	s.serveBoardFeed(w, r.Context(), boardID, sourceURL, pageSize, bookmark, allowUnverified, includeRaw)
}

func (s *Server) handleBoardFeed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	boardID := strings.TrimSpace(q.Get("board_id"))
	if boardID == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "board_id query parameter is required", 0, "", "")
		return
	}
	sourceURL := firstNonEmptyQuery(q.Get("source_url"), "/")
	pageSize := parsePositiveInt(q.Get("page_size"), 25, 100)
	bookmark := q.Get("bookmark")
	allowUnverified := parseOptionalBoolParam(q.Get("allow_unverified"))
	includeRaw := parseBoolParam(q.Get("raw"))

	s.serveBoardFeed(w, r.Context(), boardID, sourceURL, pageSize, bookmark, allowUnverified, includeRaw)
}

func (s *Server) serveBoardFeed(w http.ResponseWriter, ctx context.Context, boardID, sourceURL string, pageSize int, bookmark string, allowUnverified *bool, includeRaw bool) {
	cacheKey := fmt.Sprintf("%s|%d|%s", boardID, pageSize, bookmark)
	if page, ok := s.boardPinsCache.Get(cacheKey); ok {
		s.writePinsResponse(w, boardID, page, true, includeRaw)
		return
	}

	page, err := s.client.PublicBoardPins(ctx, boardID, sourceURL, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.indexPins(page.Results)
	s.boardPinsCache.Set(cacheKey, page)
	s.writePinsResponse(w, boardID, page, false, includeRaw)
}

func (s *Server) writePinsResponse(w http.ResponseWriter, subject string, page *pinterest.PageResult[pinterest.Pin], cached, includeRaw bool) {
	pins := stripPinRawSlice(page.Results, includeRaw)
	var raw json.RawMessage
	if includeRaw {
		raw = page.Raw
	}
	writeJSON(w, http.StatusOK, pinsResponse{
		Subject:       subject,
		Endpoint:      page.Endpoint,
		EvidenceGrade: page.EvidenceGrade,
		Bookmark:      page.Bookmark,
		Cached:        cached,
		Count:         len(pins),
		Pins:          pins,
		Raw:           raw,
	})
}

func (s *Server) cachedSearchPins(r *http.Request, params pinterest.SearchParams) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	key := fmt.Sprintf("%s|%s|%d|%s|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.Bookmark)
	if page, ok := s.searchPinsCache.Get(key); ok {
		return page, true, nil
	}
	page, err := s.client.SearchPins(r.Context(), params)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.searchPinsCache.Set(key, page)
	return page, false, nil
}

func (s *Server) cachedSearchBoards(r *http.Request, params pinterest.SearchParams) (*pinterest.PageResult[pinterest.Board], bool, error) {
	key := fmt.Sprintf("%s|%s|%d|%s|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.Bookmark)
	if page, ok := s.searchBoardsCache.Get(key); ok {
		return page, true, nil
	}
	page, err := s.client.SearchBoards(r.Context(), params)
	if err != nil {
		return nil, false, err
	}
	s.searchBoardsCache.Set(key, page)
	return page, false, nil
}

func (s *Server) cachedSearchUsers(r *http.Request, params pinterest.SearchParams) (*pinterest.PageResult[pinterest.UserProfile], bool, error) {
	key := fmt.Sprintf("%s|%s|%d|%s|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.Bookmark)
	if page, ok := s.searchUsersCache.Get(key); ok {
		return page, true, nil
	}
	page, err := s.client.SearchUsers(r.Context(), params)
	if err != nil {
		return nil, false, err
	}
	s.searchUsersCache.Set(key, page)
	return page, false, nil
}

func (s *Server) buildGridTokens(pins []pinterest.Pin, kind, target, sub, bookmark string) map[string]string {
	if s.shareCodec == nil || len(pins) == 0 || kind == "" || target == "" {
		return nil
	}
	tokens := make(map[string]string, len(pins))
	for _, p := range pins {
		if !strings.HasPrefix(p.ID, "e1_") {
			continue
		}
		tok, err := s.shareCodec.seal(p.ID, pinSource{
			Kind:     kind,
			Target:   target,
			Sub:      sub,
			Bookmark: bookmark,
			AggID:    p.AggregatedPinID,
			MediaKey: pinMediaKey(p),
		})
		if err == nil && tok != "" {
			tokens[p.ID] = tok
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

func (s *Server) fetchCorrelatedSource(r *http.Request, src pinSource) (*pinterest.PageResult[pinterest.Pin], error) {
	key := src.cacheKey()
	if src.Kind == sourceSearch {
		scope := pinterest.SearchScope(src.Sub)
		if scope != pinterest.ScopePins && scope != pinterest.ScopeVideos {
			scope = pinterest.ScopePins
		}
		searchKey := string(scope) + "|" + strings.ToLower(src.Target) + "|" + src.Bookmark
		if page, ok := s.searchPinsCache.Get(searchKey); ok {
			return page, nil
		}
	} else if page, ok := s.ideasCache.Get(key); ok {
		return page, nil
	}

	return s.corrFlight.do(key, func() (*pinterest.PageResult[pinterest.Pin], error) {
		ctx := r.Context()
		allow := true
		var (
			page *pinterest.PageResult[pinterest.Pin]
			err  error
		)
		switch src.Kind {
		case sourceSearch:
			scope := pinterest.SearchScope(src.Sub)
			if scope != pinterest.ScopePins && scope != pinterest.ScopeVideos {
				scope = pinterest.ScopePins
			}
			page, _, err = s.cachedSearchPins(r, pinterest.SearchParams{
				Query:           src.Target,
				Scope:           scope,
				Bookmark:        src.Bookmark,
				AllowUnverified: &allow,
			})
			return page, err
		case sourceBoard:
			sourceURL := firstNonEmptyQuery(src.Sub, "/")
			page, err = s.client.PublicBoardPins(ctx, src.Target, sourceURL, 25, src.Bookmark, &allow)
		case sourceUserPins:
			page, err = s.client.PublicProfilePins(ctx, src.Target, 25, src.Bookmark, &allow)
		case sourceUserCreated:
			page, err = s.client.PublicCreatedPins(ctx, src.Target, src.Sub, 25, src.Bookmark, &allow)
		case sourceIdeas:
			page, err = s.client.PublicIdeas(ctx, src.Target, src.Sub, src.Bookmark)
		default:
			return nil, fmt.Errorf("unsupported pin correlation source %q", src.Kind)
		}
		if err != nil {
			return nil, err
		}
		s.indexPins(page.Results)
		s.ideasCache.Set(key, page)
		return page, nil
	})
}

func (s *Server) resolveCorrelatedPin(r *http.Request, pinID string) (pinterest.Pin, bool) {
	if s.shareCodec == nil || !strings.HasPrefix(pinID, "e1_") {
		return pinterest.Pin{}, false
	}
	tok := strings.TrimSpace(r.URL.Query().Get("c"))
	src, ok := s.shareCodec.open(pinID, tok)
	if !ok {
		return pinterest.Pin{}, false
	}
	negKey := pinID + "|" + src.cacheKey()
	if _, missed := s.negCorrCache.Get(negKey); missed {
		return pinterest.Pin{}, false
	}
	page, err := s.fetchCorrelatedSource(r, src)
	if err != nil || page == nil {
		s.negCorrCache.Set(negKey, struct{}{})
		return pinterest.Pin{}, false
	}
	matched, found := matchCorrelatedPin(page.Results, pinID, src)
	if !found {
		s.negCorrCache.Set(negKey, struct{}{})
		return pinterest.Pin{}, false
	}
	s.pinCache.Set(pinID, matched)
	return matched, true
}

func (s *Server) loadPinByID(r *http.Request, rawID string) (pinterest.Pin, error) {
	pinID := pinterest.ExtractPinID(rawID)
	if pinID == "" {
		return pinterest.Pin{}, fmt.Errorf("pin id is required")
	}
	cached, ok := s.pinCache.Get(pinID)
	needsEnrich := !ok || (!strings.HasPrefix(pinID, "e1_") && cached.Saves == 0 && cached.Repins == 0)
	if needsEnrich {
		if !ok && strings.HasPrefix(pinID, "e1_") {
			if rehydrated, found := s.resolveCorrelatedPin(r, pinID); found {
				return rehydrated, nil
			}
			// Without an active share correlation key+token, do not query Pinterest or leak searches.
			return pinterest.Pin{}, &pinterest.Error{
				Code:    pinterest.CodeHTTPError,
				Message: "pin not found in cache",
				Status:  http.StatusNotFound,
			}
		}
		fetched, err := s.client.PublicPin(r.Context(), pinID)
		if err == nil {
			s.pinCache.Set(pinID, *fetched)
			return *fetched, nil
		}
		if ok {
			return cached, nil
		}
		return pinterest.Pin{}, err
	}
	return cached, nil
}

func relatedSearchQuery(pin pinterest.Pin) string {
	query := strings.TrimSpace(firstNonEmptyQuery(pin.Title, pin.RichTitle, pin.Description, pin.Pinner.FullName, pin.Pinner.Username))
	if query == "" && pin.SEOTitle != "" {
		query = strings.TrimSpace(strings.SplitN(pin.SEOTitle, "|", 2)[0])
	}
	if query == "" && len(pin.VisualTags) > 0 {
		limit := min(3, len(pin.VisualTags))
		query = strings.Join(pin.VisualTags[:limit], " ")
	}
	if query == "" && pin.Domain != "" && !strings.EqualFold(pin.Domain, "Uploaded by user") {
		query = pin.Domain
	}
	if len(query) > 64 {
		query = query[:64]
	}
	return strings.TrimSpace(query)
}

func (s *Server) handlePinDetail(w http.ResponseWriter, r *http.Request) {
	pinID := pinterest.ExtractPinID(r.PathValue("id"))
	if pinID == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "pin id is required", 0, "", "")
		return
	}
	pin, err := s.loadPinByID(r, pinID)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	includeRaw := parseBoolParam(r.URL.Query().Get("raw"))
	if !includeRaw {
		pin.Raw = nil
	}

	var related []pinterest.Pin
	if parseBoolParam(r.URL.Query().Get("related")) {
		if query := relatedSearchQuery(pin); query != "" {
			if page, _, relErr := s.cachedSearchPins(r, pinterest.SearchParams{Query: query, Scope: pinterest.ScopePins}); relErr == nil {
				for _, item := range page.Results {
					if item.ID != pin.ID {
						related = append(related, item)
					}
					if len(related) >= 12 {
						break
					}
				}
				related = stripPinRawSlice(related, includeRaw)
			}
		}
	}

	writeJSON(w, http.StatusOK, pinDetailResponse{
		Pin:     pin,
		Related: related,
	})
}

func (s *Server) handlePinCommentsAPI(w http.ResponseWriter, r *http.Request) {
	pinID := pinterest.ExtractPinID(r.PathValue("id"))
	if pinID == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "pin id is required", 0, "", "")
		return
	}
	q := r.URL.Query()
	aid := strings.TrimSpace(q.Get("aid"))
	if aid == "" {
		if pin, err := s.loadPinByID(r, pinID); err == nil && pin.AggregatedPinID != "" {
			aid = pin.AggregatedPinID
		}
	}
	res, err := s.client.PublicPinComments(r.Context(), pinID, aid, q.Get("bookmark"))
	if err != nil {
		writePinterestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCommentRepliesAPI(w http.ResponseWriter, r *http.Request) {
	commentID := strings.TrimSpace(r.PathValue("id"))
	if commentID == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "comment id is required", 0, "", "")
		return
	}
	res, err := s.client.PublicCommentReplies(r.Context(), commentID, r.URL.Query().Get("bookmark"))
	if err != nil {
		writePinterestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleIdeasAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	interestID := r.PathValue("id")
	q := r.URL.Query()
	includeRaw := parseBoolParam(q.Get("raw"))

	page, err := s.client.PublicIdeas(r.Context(), slug, interestID, q.Get("bookmark"))
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.indexPins(page.Results)
	s.writePinsResponse(w, interestID, page, false, includeRaw)
}

func (s *Server) handleMediaProxy(w http.ResponseWriter, r *http.Request) {
	s.handleMediaProxyWithURL(w, r, r.URL.Query().Get("url"))
}

func (s *Server) handleMediaProxyWithURL(w http.ResponseWriter, r *http.Request, rawURL string) {
	if s.hlsCache != nil {
		if cachedManifest, ok := s.hlsCache.Get(rawURL); ok {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, cachedManifest)
			return
		}
	}

	resp, err := s.client.Media().Fetch(r.Context(), rawURL, r.Header.Get("Range"))
	if err != nil {
		writePinterestError(w, err)
		return
	}
	defer resp.Body.Close()

	if resp.IsHLSManifest {
		manifestBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			writePinterestError(w, err)
			return
		}
		rewritten := pinterest.RewriteHLSManifest(string(manifestBytes), resp.SourceURL, "/api/v1/media/proxy?url=")
		if s.hlsCache != nil {
			s.hlsCache.Set(rawURL, rewritten)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, rewritten)
		return
	}

	w.Header().Set("Content-Type", resp.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if resp.AcceptRanges != "" {
		w.Header().Set("Accept-Ranges", resp.AcceptRanges)
	} else {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if resp.ContentRange != "" {
		w.Header().Set("Content-Range", resp.ContentRange)
	}
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.Debug("media proxy stream interrupted", "error", err)
	}
}

func (s *Server) handleBinternetAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(firstNonEmptyQuery(q.Get("q"), q.Get("query")))
	if query == "" {
		writeError(w, http.StatusBadRequest, string(pinterest.CodeValidationError), "query parameter q is required", 0, "", "")
		return
	}
	scope := pinterest.SearchScope(firstNonEmptyQuery(q.Get("scope"), string(pinterest.ScopePins)))
	if scope != pinterest.ScopePins && scope != pinterest.ScopeVideos {
		scope = pinterest.ScopePins
	}
	bookmark := q.Get("bookmark")
	allow := parseOptionalBoolParam(q.Get("allow_unverified"))

	page, err := s.client.SearchPins(r.Context(), pinterest.SearchParams{
		Query:           query,
		Scope:           scope,
		Bookmark:        bookmark,
		AllowUnverified: allow,
	})
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.indexPins(page.Results)
	writeJSON(w, http.StatusOK, binternetResponse{
		Status:   "success",
		Query:    query,
		Scope:    string(scope),
		Bookmark: page.Bookmark,
		Count:    len(page.Results),
		Results:  stripPinRawSlice(page.Results, false),
	})
}

var iPinimgDirectPrefixes = []string{
	"/originals/",
	"/736x/",
	"/564x/",
	"/474x/",
	"/236x/",
	"/170x/",
	"/60x60/",
	"/136x136/",
	"/140x140_RS/",
	"/280x280_RS/",
	"/75x75_RS/",
	"/30x30_RS/",
	"/upload/",
	"/videos/thumbnails/",
}

func resolveDirectCDNRedirect(urlPath, rawQuery string) (string, bool) {
	var host string
	for _, prefix := range iPinimgDirectPrefixes {
		if strings.HasPrefix(urlPath, prefix) {
			host = "https://i.pinimg.com"
			break
		}
	}
	if host == "" && strings.HasPrefix(urlPath, "/videos/iht/") {
		host = "https://v1.pinimg.com"
	}
	if host == "" {
		return "", false
	}
	target := host + urlPath
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target, true
}

func (s *Server) loadProfile(ctx context.Context, username string) (*pinterest.UserProfile, bool, error) {
	key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	if p, ok := s.profileCache.Get(key); ok {
		return p, true, nil
	}
	p, err := s.client.PublicProfile(ctx, username)
	if err != nil {
		return nil, false, err
	}
	s.profileCache.Set(key, p)
	return p, false, nil
}

func (s *Server) loadBoard(ctx context.Context, username, slug string) (*pinterest.Board, bool, error) {
	key := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@")) + "/" + strings.ToLower(strings.Trim(slug, "/"))
	if b, ok := s.boardCache.Get(key); ok {
		return b, true, nil
	}
	b, err := s.client.PublicBoard(ctx, username, slug)
	if err != nil {
		return nil, false, err
	}
	s.boardCache.Set(key, b)
	return b, false, nil
}

func (s *Server) indexPins(pins []pinterest.Pin) {
	for _, pin := range pins {
		if pin.ID != "" {
			s.pinCache.Set(pin.ID, pin)
		}
	}
}

func parseSearchRequest(r *http.Request) (pinterest.SearchParams, bool, error) {
	if r.Method == http.MethodPost {
		var body searchRequestBody
		dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
		if err := dec.Decode(&body); err != nil {
			return pinterest.SearchParams{}, false, errors.New("invalid JSON request body")
		}
		return pinterest.SearchParams{
			Query:           body.Query,
			Scope:           body.Scope,
			PageSize:        body.PageSize,
			RS:              body.RS,
			Bookmark:        body.Bookmark,
			AllowUnverified: body.AllowUnverified,
		}, body.Raw, nil
	}

	q := r.URL.Query()
	var pageSizePtr *int
	if rawSize := strings.TrimSpace(q.Get("page_size")); rawSize != "" {
		n, err := strconv.Atoi(rawSize)
		if err != nil {
			return pinterest.SearchParams{}, false, errors.New("page_size must be an integer")
		}
		pageSizePtr = &n
	}
	return pinterest.SearchParams{
		Query:           firstNonEmptyQuery(q.Get("q"), q.Get("query")),
		Scope:           pinterest.SearchScope(strings.TrimSpace(q.Get("scope"))),
		PageSize:        pageSizePtr,
		RS:              strings.TrimSpace(q.Get("rs")),
		Bookmark:        q.Get("bookmark"),
		AllowUnverified: parseOptionalBoolParam(q.Get("allow_unverified")),
	}, parseBoolParam(q.Get("raw")), nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string, upstreamStatus int, ctype, retryAfter string) {
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	writeJSON(w, status, apiErrorEnvelope{
		Error: apiErrorBody{
			Code:        code,
			Message:     message,
			Status:      upstreamStatus,
			ContentType: ctype,
			RetryAfter:  retryAfter,
		},
	})
}

func writePinterestError(w http.ResponseWriter, err error) {
	if pErr, ok := pinterest.AsError(err); ok {
		writeError(w, pErr.HTTPStatus(), string(pErr.Code), pErr.Message, pErr.Status, pErr.ContentType, pErr.RetryAfter)
		return
	}
	writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR", err.Error(), 0, "", "")
}

func stripPinRawSlice(pins []pinterest.Pin, includeRaw bool) []pinterest.Pin {
	out := make([]pinterest.Pin, len(pins))
	copy(out, pins)
	if !includeRaw {
		for i := range out {
			out[i].Raw = nil
		}
	}
	return out
}

func stripBoardRawSlice(boards []pinterest.Board, includeRaw bool) []pinterest.Board {
	out := make([]pinterest.Board, len(boards))
	copy(out, boards)
	if !includeRaw {
		for i := range out {
			out[i].Raw = nil
		}
	}
	return out
}

func stripUserRawSlice(users []pinterest.UserProfile, includeRaw bool) []pinterest.UserProfile {
	out := make([]pinterest.UserProfile, len(users))
	copy(out, users)
	if !includeRaw {
		for i := range out {
			out[i].Raw = nil
		}
	}
	return out
}

func stripSuggestionRawSlice(items []pinterest.Suggestion, includeRaw bool) []pinterest.Suggestion {
	out := make([]pinterest.Suggestion, len(items))
	copy(out, items)
	if !includeRaw {
		for i := range out {
			out[i].Raw = nil
		}
	}
	return out
}

func stripTabRawSlice(tabs []pinterest.SearchTab, includeRaw bool) []pinterest.SearchTab {
	if len(tabs) == 0 {
		return nil
	}
	out := make([]pinterest.SearchTab, len(tabs))
	copy(out, tabs)
	if !includeRaw {
		for i := range out {
			out[i].Raw = nil
		}
	}
	return out
}

func firstNonEmptyQuery(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parsePositiveInt(raw string, def, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func parseBoolParam(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseOptionalBoolParam(raw string) *bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return nil
	}
	val := trimmed == "1" || trimmed == "true" || trimmed == "yes" || trimmed == "on"
	return &val
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
