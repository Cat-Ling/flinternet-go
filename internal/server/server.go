// Package server exposes the Flinternet REST API, SSRF-safe media proxy,
// Binternet compatibility endpoints, and embedded multi-device web UI.
package server

import (
	"bytes"
	"compress/gzip"
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
	enrichedPins       *cache.Cache[string, struct{}]
	hlsCache           *cache.Cache[string, string]
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
		enrichedPins:       cache.New[string, struct{}](defaultPinCache, 30*time.Minute),
		hlsCache:           cache.New[string, string](defaultCacheItems, ttl),
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
	FilterGenAI     *bool                 `json:"filter_genai"`
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
		page, cached, err := s.cachedSearchPins(r.Context(), reqParams)
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

	page, cached, err := s.cachedUserBoards(r.Context(), username, bookmark)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.writeBoardsResponse(w, username, page, cached, includeRaw)
}

func (s *Server) cachedUserBoards(ctx context.Context, username, bookmark string) (*pinterest.PageResult[pinterest.Board], bool, error) {
	cacheKey := strings.ToLower(strings.TrimPrefix(username, "@")) + "|" + bookmark
	if page, ok := s.profileBoardsCache.Get(cacheKey); ok {
		return page, true, nil
	}
	page, err := s.client.PublicProfileBoards(ctx, username, bookmark)
	if err != nil {
		return nil, false, err
	}
	s.profileBoardsCache.Set(cacheKey, page)
	return page, false, nil
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

	page, cached, err := s.cachedUserPins(r.Context(), username, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.writePinsResponse(w, username, page, cached, includeRaw)
}

func (s *Server) cachedUserPins(ctx context.Context, username string, pageSize int, bookmark string, allowUnverified *bool) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	cacheKey := fmt.Sprintf("%s|%d|%s", strings.ToLower(strings.TrimPrefix(username, "@")), pageSize, bookmark)
	if page, ok := s.profilePinsCache.Get(cacheKey); ok {
		return page, true, nil
	}
	page, err := s.client.PublicProfilePins(ctx, username, pageSize, bookmark, allowUnverified)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.profilePinsCache.Set(cacheKey, page)
	return page, false, nil
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

	page, cached, err := s.cachedUserCreatedPins(r.Context(), username, userID, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.writePinsResponse(w, username, page, cached, includeRaw)
}

func (s *Server) cachedUserCreatedPins(ctx context.Context, username, userID string, pageSize int, bookmark string, allowUnverified *bool) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	cacheKey := fmt.Sprintf("%s|%s|%d|%s", strings.ToLower(strings.TrimPrefix(username, "@")), userID, pageSize, bookmark)
	if page, ok := s.createdPinsCache.Get(cacheKey); ok {
		return page, true, nil
	}
	page, err := s.client.PublicCreatedPins(ctx, username, userID, pageSize, bookmark, allowUnverified)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.createdPinsCache.Set(cacheKey, page)
	return page, false, nil
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
	page, cached, err := s.cachedBoardPins(ctx, boardID, sourceURL, pageSize, bookmark, allowUnverified)
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.writePinsResponse(w, boardID, page, cached, includeRaw)
}

func (s *Server) cachedBoardPins(ctx context.Context, boardID, sourceURL string, pageSize int, bookmark string, allowUnverified *bool) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	cacheKey := fmt.Sprintf("%s|%d|%s", boardID, pageSize, bookmark)
	if page, ok := s.boardPinsCache.Get(cacheKey); ok {
		return page, true, nil
	}
	page, err := s.client.PublicBoardPins(ctx, boardID, sourceURL, pageSize, bookmark, allowUnverified)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.boardPinsCache.Set(cacheKey, page)
	return page, false, nil
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

func (s *Server) cachedSearchPins(ctx context.Context, params pinterest.SearchParams) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	key := fmt.Sprintf("%s|%s|%d|%s|%t|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.FilterGenAI, params.Bookmark)
	if page, ok := s.searchPinsCache.Get(key); ok {
		return page, true, nil
	}
	page, err := s.client.SearchPins(ctx, params)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.searchPinsCache.Set(key, page)
	return page, false, nil
}

func (s *Server) cachedSearchBoards(r *http.Request, params pinterest.SearchParams) (*pinterest.PageResult[pinterest.Board], bool, error) {
	key := fmt.Sprintf("%s|%s|%d|%s|%t|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.FilterGenAI, params.Bookmark)
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
	key := fmt.Sprintf("%s|%s|%d|%s|%t|%s", params.Query, params.Scope, derefInt(params.PageSize), params.RS, params.FilterGenAI, params.Bookmark)
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

func (s *Server) buildPinTokens(pins []pinterest.Pin) map[string]string {
	if len(pins) == 0 {
		return nil
	}
	tokens := make(map[string]string, len(pins))
	for _, p := range pins {
		if !strings.HasPrefix(p.ID, "e1_") {
			continue
		}
		tok, err := encodeBoundedPinToken(s.shareCodec, p)
		if err == nil && tok != "" {
			tokens[p.ID] = tok
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

func (s *Server) loadPinByID(r *http.Request, rawID string) (pinterest.Pin, error) {
	pinID := pinterest.ExtractPinID(rawID)
	if pinID == "" {
		return pinterest.Pin{}, fmt.Errorf("pin id is required")
	}
	cached, ok := s.pinCache.Get(pinID)
	_, alreadyEnriched := s.enrichedPins.Get(pinID)
	needsEnrich := !ok || (!strings.HasPrefix(pinID, "e1_") && !alreadyEnriched && cached.Saves == 0 && cached.Repins == 0)
	if needsEnrich {
		if !ok && strings.HasPrefix(pinID, "e1_") {
			if tok := strings.TrimSpace(r.URL.Query().Get("p")); tok != "" {
				if restored, authenticated, valid := decodePinSnapshot(s.shareCodec, pinID, tok); valid {
					// Only populate shared pinCache when cryptographically authenticated by ShareKey.
					if authenticated {
						s.pinCache.Set(pinID, restored)
					}
					return restored, nil
				}
			}
			return pinterest.Pin{}, &pinterest.Error{
				Code:    pinterest.CodeHTTPError,
				Message: "pin not found",
				Status:  http.StatusNotFound,
			}
		}
		fetched, err := s.client.PublicPin(r.Context(), pinID)
		if err == nil {
			s.enrichedPins.Set(pinID, struct{}{})
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
			if page, _, relErr := s.cachedSearchPins(r.Context(), pinterest.SearchParams{Query: query, Scope: pinterest.ScopePins}); relErr == nil {
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

	page, cached, err := s.cachedIdeas(r.Context(), slug, interestID, q.Get("bookmark"))
	if err != nil {
		writePinterestError(w, err)
		return
	}
	s.writePinsResponse(w, interestID, page, cached, includeRaw)
}

func (s *Server) cachedIdeas(ctx context.Context, slug, interestID, bookmark string) (*pinterest.PageResult[pinterest.Pin], bool, error) {
	cacheKey := strings.ToLower(strings.TrimSpace(slug)) + "|" + strings.TrimSpace(interestID) + "|" + bookmark
	if page, ok := s.ideasCache.Get(cacheKey); ok {
		return page, true, nil
	}
	page, err := s.client.PublicIdeas(ctx, slug, interestID, bookmark)
	if err != nil {
		return nil, false, err
	}
	s.indexPins(page.Results)
	s.ideasCache.Set(cacheKey, page)
	return page, false, nil
}

func (s *Server) handleMediaProxy(w http.ResponseWriter, r *http.Request) {
	s.handleMediaProxyWithURL(w, r, r.URL.Query().Get("url"))
}

func writeHLSManifestResponse(w http.ResponseWriter, method, manifest string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(len(manifest)))
	w.WriteHeader(http.StatusOK)
	if method != http.MethodHead {
		_, _ = io.WriteString(w, manifest)
	}
}

func (s *Server) handleMediaProxyWithURL(w http.ResponseWriter, r *http.Request, rawURL string) {
	if s.hlsCache != nil {
		if cachedManifest, ok := s.hlsCache.Get(rawURL); ok {
			writeHLSManifestResponse(w, r.Method, cachedManifest)
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
		if len(manifestBytes) >= 2 && manifestBytes[0] == 0x1f && manifestBytes[1] == 0x8b {
			gz, gzErr := gzip.NewReader(bytes.NewReader(manifestBytes))
			if gzErr != nil {
				writeError(w, http.StatusBadGateway, string(pinterest.CodeProtocolError), "invalid gzip upstream HLS manifest", resp.StatusCode, resp.ContentType, "")
				return
			}
			uncompressed, readErr := io.ReadAll(io.LimitReader(gz, 2*1024*1024))
			_ = gz.Close()
			if readErr != nil {
				writeError(w, http.StatusBadGateway, string(pinterest.CodeProtocolError), "failed decompressing upstream HLS manifest", resp.StatusCode, resp.ContentType, "")
				return
			}
			manifestBytes = uncompressed
		}
		manifestText := strings.TrimPrefix(string(manifestBytes), "\ufeff")
		if !strings.HasPrefix(strings.TrimSpace(manifestText), "#EXTM3U") {
			writeError(w, http.StatusBadGateway, string(pinterest.CodeProtocolError), "invalid upstream HLS manifest", resp.StatusCode, resp.ContentType, "")
			return
		}
		rewritten := pinterest.RewriteHLSManifest(manifestText, resp.SourceURL, "/api/v1/media/proxy?url=")
		if s.hlsCache != nil {
			s.hlsCache.Set(rawURL, rewritten)
		}
		writeHLSManifestResponse(w, r.Method, rewritten)
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
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, resp.Body); err != nil {
			s.logger.Debug("media proxy stream interrupted", "error", err)
		}
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

const lessAICookieName = "flinternet_less_ai"

func lessAICookiePref(r *http.Request) (bool, bool) {
	if r == nil {
		return false, false
	}
	c, err := r.Cookie(lessAICookieName)
	if err != nil {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(c.Value)) {
	case "0", "false", "no", "off":
		return false, true
	case "1", "true", "yes", "on":
		return true, true
	default:
		return false, false
	}
}

func resolveLessAIPref(w http.ResponseWriter, r *http.Request) bool {
	if r != nil {
		if raw := strings.TrimSpace(r.URL.Query().Get("filter_genai")); raw != "" {
			if parsed := parseOptionalBoolParam(raw); parsed != nil {
				if w != nil {
					val := "0"
					if *parsed {
						val = "1"
					}
					http.SetCookie(w, &http.Cookie{
						Name:     lessAICookieName,
						Value:    val,
						Path:     "/",
						MaxAge:   365 * 24 * 3600,
						SameSite: http.SameSiteLaxMode,
					})
				}
				return *parsed
			}
		}
		if pref, ok := lessAICookiePref(r); ok {
			return pref
		}
	}
	return true
}

func parseSearchRequest(r *http.Request) (pinterest.SearchParams, bool, error) {
	if r.Method == http.MethodPost {
		var body searchRequestBody
		dec := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
		if err := dec.Decode(&body); err != nil {
			return pinterest.SearchParams{}, false, errors.New("invalid JSON request body")
		}
		filterGenAI := false
		if body.Scope == "" || body.Scope == pinterest.ScopePins {
			if body.FilterGenAI != nil {
				filterGenAI = *body.FilterGenAI
			} else if pref, ok := lessAICookiePref(r); ok {
				filterGenAI = pref
			} else {
				filterGenAI = true
			}
		}
		return pinterest.SearchParams{
			Query:           body.Query,
			Scope:           body.Scope,
			PageSize:        body.PageSize,
			RS:              body.RS,
			Bookmark:        body.Bookmark,
			FilterGenAI:     filterGenAI,
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
	scope := pinterest.SearchScope(strings.TrimSpace(q.Get("scope")))
	filterGenAI := false
	if scope == "" || scope == pinterest.ScopePins {
		if explicit := parseOptionalBoolParam(q.Get("filter_genai")); explicit != nil {
			filterGenAI = *explicit
		} else if pref, ok := lessAICookiePref(r); ok {
			filterGenAI = pref
		} else {
			filterGenAI = true
		}
	}
	return pinterest.SearchParams{
		Query:           firstNonEmptyQuery(q.Get("q"), q.Get("query")),
		Scope:           scope,
		PageSize:        pageSizePtr,
		RS:              strings.TrimSpace(q.Get("rs")),
		Bookmark:        q.Get("bookmark"),
		FilterGenAI:     filterGenAI,
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
