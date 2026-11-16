package pinterest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SearchScope constrains BaseSearchResource queries to documented scopes.
type SearchScope string

const (
	ScopePins   SearchScope = "pins"
	ScopeVideos SearchScope = "videos"
	ScopeBoards SearchScope = "boards"
	ScopeUsers  SearchScope = "users"
)

// ValidSearchScope checks whether s is one of pins, videos, boards, or users.
func ValidSearchScope(s SearchScope) bool {
	switch s {
	case ScopePins, ScopeVideos, ScopeBoards, ScopeUsers:
		return true
	default:
		return false
	}
}

// SearchParams controls a BaseSearchResource/get request.
type SearchParams struct {
	Query           string
	Scope           SearchScope
	PageSize        *int
	RS              string
	Bookmark        string
	AllowUnverified *bool
}

// EndpointSpec documents each Pinterest web resource supported by Flinternet.
type EndpointSpec struct {
	Name          string `json:"name"`
	Resource      string `json:"resource"`
	HTTPMethod    string `json:"http_method"`
	EvidenceGrade string `json:"evidence_grade"`
	LocalRoute    string `json:"local_route"`
	Pagination    string `json:"pagination"`
	Description   string `json:"description"`
}

// EndpointCatalog returns metadata for every unauthenticated resource covered by the client.
func EndpointCatalog() []EndpointSpec {
	return []EndpointSpec{
		{
			Name:          "Search",
			Resource:      "BaseSearchResource/get",
			HTTPMethod:    "GET / POST continuation",
			EvidenceGrade: "A/B (first page), C (POST continuation)",
			LocalRoute:    "/api/v1/search?q={query}&scope={pins|videos|boards|users}&bookmark={cursor}",
			Pagination:    "Opaque cursor via POST bookmarks when unverified pagination is enabled",
			Description:   "Search public pins, video pins, boards, and user accounts.",
		},
		{
			Name:          "Typeahead Suggestions",
			Resource:      "AdvancedTypeaheadResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "A/B",
			LocalRoute:    "/api/v1/suggest?term={prefix}&scope={pins|videos|boards|users}&count={n}",
			Pagination:    "Not applicable",
			Description:   "Autocomplete search terms and quick profile suggestions.",
		},
		{
			Name:          "Search Tabs & Guides",
			Resource:      "SearchTabResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/search/tabs?q={query}&scope={scope}",
			Pagination:    "Not applicable",
			Description:   "Search scope/topic tabs and visual guide pills.",
		},
		{
			Name:          "Public Profile",
			Resource:      "UserResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "A/B",
			LocalRoute:    "/api/v1/users/{username}",
			Pagination:    "Not applicable",
			Description:   "Public profile metadata with field_set_key=unauth_profile.",
		},
		{
			Name:          "Public Profile Boards",
			Resource:      "BoardsFeedResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "A-P",
			LocalRoute:    "/api/v1/users/{username}/boards?bookmark={cursor}",
			Pagination:    "Verified guest cursor pagination via bookmarks=[cursor]",
			Description:   "Paginated public boards owned by a user.",
		},
		{
			Name:          "Public Profile Pins",
			Resource:      "UserPinsResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/users/{username}/pins?page_size={n}",
			Pagination:    "First page verified; continuation gated",
			Description:   "Public saved pins grid for a user profile.",
		},
		{
			Name:          "Public Created Pins",
			Resource:      "UserActivityPinsResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/users/{username}/created?user_id={id}&page_size={n}",
			Pagination:    "First page verified; continuation gated",
			Description:   "Pins created by a public user (auto-resolves user_id if omitted).",
		},
		{
			Name:          "Public Board Metadata",
			Resource:      "BoardResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/boards/{username}/{slug}",
			Pagination:    "Not applicable",
			Description:   "Public board metadata, cover, pin count, and owner summary.",
		},
		{
			Name:          "Public Board Pins",
			Resource:      "BoardFeedResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/boards/{username}/{slug}/pins?page_size={n}",
			Pagination:    "First page verified; continuation gated",
			Description:   "Pins inside a public board (auto-resolves board_id if omitted).",
		},
		{
			Name:          "Public Pin Detail",
			Resource:      "PinResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/pins/{id}?related={0|1}",
			Pagination:    "Not applicable",
			Description:   "Direct unauthenticated pin lookup via field_set_key=detailed.",
		},
		{
			Name:          "Pin Comments",
			Resource:      "UnifiedCommentsResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/pins/{id}/comments?aid={aggregated_pin_id}&bookmark={cursor}",
			Pagination:    "Opaque cursor via bookmarks=[cursor]",
			Description:   "Public comments and Tried It notes for a pin.",
		},
		{
			Name:          "Comment Replies",
			Resource:      "AggregatedCommentReplyFeedResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/comments/{id}/replies?bookmark={cursor}",
			Pagination:    "Opaque cursor via bookmarks=[cursor]",
			Description:   "Public replies for a unified comment thread.",
		},
		{
			Name:          "Ideas Interest Feed",
			Resource:      "BestPinsFeedAltResource/get",
			HTTPMethod:    "GET",
			EvidenceGrade: "B",
			LocalRoute:    "/api/v1/ideas/{slug}/{id}?bookmark={cursor}",
			Pagination:    "Opaque cursor via bookmarks=[cursor]",
			Description:   "Pins for a Pinterest /ideas/{slug}/{id}/ topic page.",
		},
		{
			Name:          "Media Proxy",
			Resource:      "i.pinimg.com / v1.pinimg.com",
			HTTPMethod:    "GET",
			EvidenceGrade: "Hardened CDN Proxy",
			LocalRoute:    "/api/v1/media/proxy?url={https_pinimg_url}",
			Pagination:    "Range requests supported",
			Description:   "SSRF-hardened proxy for images, MP4 videos, and rewritten HLS .m3u8 manifests.",
		},
	}
}

type baseSearchOptions struct {
	Query                  string   `json:"query"`
	Scope                  string   `json:"scope"`
	AppliedProductFilters  string   `json:"appliedProductFilters"`
	SEODrawerEnabled       bool     `json:"seoDrawerEnabled"`
	AutoCorrectionDisabled bool     `json:"auto_correction_disabled"`
	FilterGenAI            bool     `json:"filter_genai"`
	SourceURL              string   `json:"source_url"`
	StaticFeed             bool     `json:"static_feed"`
	PageSize               *int     `json:"page_size"`
	Gated                  bool     `json:"gated"`
	NoFetchContextOnResource bool   `json:"no_fetch_context_on_resource"`
	RS                     string   `json:"rs"`
	Bookmarks              []string `json:"bookmarks,omitempty"`
}

func (c *Client) canPaginateUnverified(override *bool) bool {
	if override != nil {
		return *override
	}
	return c.allowUnverifiedPagination
}

func defaultPageSizeForScope(scope SearchScope, req *int) (*int, error) {
	if req != nil {
		if *req < 1 || *req > 100 {
			return nil, newError(CodeValidationError, "page_size must be between 1 and 100", 0, "", "", nil)
		}
		v := *req
		return &v, nil
	}
	if scope == ScopePins || scope == ScopeVideos {
		v := 25
		return &v, nil
	}
	return nil, nil
}

// Search executes BaseSearchResource/get (first page GET, optional POST continuation).
func (c *Client) Search(ctx context.Context, params SearchParams) (*PageResult[json.RawMessage], error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, newError(CodeValidationError, "query must not be empty", 0, "", "", nil)
	}
	scope := params.Scope
	if scope == "" {
		scope = ScopePins
	}
	if !ValidSearchScope(scope) {
		return nil, newError(CodeValidationError, "scope must be pins, videos, boards, or users", 0, "", "", nil)
	}
	pageSize, err := defaultPageSizeForScope(scope, params.PageSize)
	if err != nil {
		return nil, err
	}

	rs := strings.TrimSpace(params.RS)
	if rs == "" {
		if scope == ScopePins {
			rs = "typed"
		} else {
			rs = "content_type_filter"
		}
	}

	q := url.Values{}
	q.Set("q", query)
	q.Set("rs", rs)
	sourceURL := fmt.Sprintf("/search/%s/?%s", scope, q.Encode())

	method := http.MethodGet
	grade := "B"
	options := baseSearchOptions{
		Query:                    query,
		Scope:                    string(scope),
		AppliedProductFilters:    "---",
		SEODrawerEnabled:         false,
		AutoCorrectionDisabled:   false,
		FilterGenAI:              false,
		SourceURL:                sourceURL,
		StaticFeed:               false,
		PageSize:                 pageSize,
		Gated:                    false,
		NoFetchContextOnResource: false,
		RS:                       rs,
	}

	if params.Bookmark != "" {
		if !c.canPaginateUnverified(params.AllowUnverified) {
			return nil, newError(CodePaginationNotVerified, "search continuation is not verified as unauthenticated in capture; pass allow_unverified=true to test in a fresh guest session", 0, "", "", nil)
		}
		options.Bookmarks = []string{params.Bookmark}
		method = http.MethodPost
		grade = "C"
	}

	env, rawBody, err := doResourceRequest(ctx, c, "BaseSearchResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    method,
		handler:   fmt.Sprintf("www/search/%s.js", scope),
	})
	if err != nil {
		return nil, err
	}

	items, tabs, err := extractSearchEnvelope(env.ResourceResponse.Data, string(scope))
	if err != nil {
		return nil, newError(CodeProtocolError, "unexpected BaseSearchResource data shape", 200, "application/json", "", err)
	}
	bookmark := normalizeBookmark(env.ResourceResponse.Bookmark)

	return &PageResult[json.RawMessage]{
		Results:       items,
		Bookmark:      bookmark,
		Endpoint:      "BaseSearchResource/get",
		EvidenceGrade: grade,
		Tabs:          tabs,
		Raw:           rawBody,
	}, nil
}

// SearchPins runs a search for pins and returns normalized Pin models.
func (c *Client) SearchPins(ctx context.Context, params SearchParams) (*PageResult[Pin], error) {
	if params.Scope == "" {
		params.Scope = ScopePins
	}
	page, err := c.Search(ctx, params)
	if err != nil {
		return nil, err
	}
	return filterValidPins(mapPageResults(page, NormalizePin)), nil
}

// SearchBoards runs a search for boards and returns normalized Board models.
func (c *Client) SearchBoards(ctx context.Context, params SearchParams) (*PageResult[Board], error) {
	params.Scope = ScopeBoards
	page, err := c.Search(ctx, params)
	if err != nil {
		return nil, err
	}
	return filterValidBoards(mapPageResults(page, NormalizeBoard)), nil
}

// SearchUsers runs a search for public user accounts and returns normalized UserProfile models.
func (c *Client) SearchUsers(ctx context.Context, params SearchParams) (*PageResult[UserProfile], error) {
	params.Scope = ScopeUsers
	page, err := c.Search(ctx, params)
	if err != nil {
		return nil, err
	}
	return mapPageResults(page, NormalizeUserProfile), nil
}

type typeaheadOptions struct {
	Term                       string `json:"term"`
	PinScope                   string `json:"pin_scope"`
	AutocompleteRequestSurface int    `json:"autocomplete_request_surface"`
	Count                      int    `json:"count"`
}

// Suggest calls AdvancedTypeaheadResource/get for search autocomplete suggestions.
func (c *Client) Suggest(ctx context.Context, term, pinScope string, count int) ([]Suggestion, error) {
	trimmed := strings.TrimSpace(term)
	if trimmed == "" {
		return []Suggestion{}, nil
	}
	if pinScope == "" {
		pinScope = "pins"
	}
	if count <= 0 || count > 25 {
		count = 5
	}

	options := typeaheadOptions{
		Term:                       trimmed,
		PinScope:                   pinScope,
		AutocompleteRequestSurface: 0,
		Count:                      count,
	}
	env, _, err := doResourceRequest(ctx, c, "AdvancedTypeaheadResource", options, requestOpts{
		sourceURL: "/",
		method:    http.MethodGet,
		handler:   "www/search/[scope].js",
	})
	if err != nil {
		return nil, err
	}

	items, _, err := extractSearchEnvelope(env.ResourceResponse.Data, pinScope)
	if err != nil {
		return []Suggestion{}, nil
	}
	suggestions := make([]Suggestion, 0, len(items))
	for _, rawItem := range items {
		s, err := NormalizeSuggestion(rawItem)
		if err != nil || s.Label == "" {
			continue
		}
		suggestions = append(suggestions, s)
	}
	return suggestions, nil
}

type searchTabOptions struct {
	Query     string `json:"query"`
	Scope     string `json:"scope"`
	SourceURL string `json:"source_url"`
}

// SearchTabs queries SearchTabResource/get for search navigation and scope tabs.
func (c *Client) SearchTabs(ctx context.Context, query, scope string) ([]SearchTab, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []SearchTab{}, nil
	}
	if scope == "" {
		scope = "pins"
	}
	vals := url.Values{}
	vals.Set("q", q)
	vals.Set("rs", "typed")
	sourceURL := fmt.Sprintf("/search/%s/?%s", scope, vals.Encode())

	options := searchTabOptions{
		Query:     q,
		Scope:     scope,
		SourceURL: sourceURL,
	}
	env, _, err := doResourceRequest(ctx, c, "SearchTabResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   fmt.Sprintf("www/search/%s.js", scope),
	})
	if err != nil {
		return nil, err
	}

	rawItems, tabs, err := extractSearchEnvelope(env.ResourceResponse.Data, scope)
	if err == nil && len(tabs) > 0 {
		return tabs, nil
	}

	out := make([]SearchTab, 0, len(rawItems))
	for _, rawItem := range rawItems {
		var g rawGuideWire
		if err := json.Unmarshal(rawItem, &g); err != nil {
			continue
		}
		if tab, ok := normalizeGuideTab(rawItem, g, scope); ok {
			out = append(out, tab)
		}
	}
	return out, nil
}

type userOptions struct {
	Username     string `json:"username"`
	FieldSetKey  string `json:"field_set_key"`
	IsMobileFork bool   `json:"is_mobile_fork"`
}

// PublicProfile calls UserResource/get with field_set_key="unauth_profile".
func (c *Client) PublicProfile(ctx context.Context, username string) (*UserProfile, error) {
	cleanUser, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}
	sourceURL := "/" + url.PathEscape(cleanUser) + "/"
	options := userOptions{
		Username:     cleanUser,
		FieldSetKey:  "unauth_profile",
		IsMobileFork: true,
	}
	env, _, err := doResourceRequest(ctx, c, "UserResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username].js",
	})
	if err != nil {
		return nil, err
	}
	profile, err := NormalizeUserProfile(env.ResourceResponse.Data)
	if err != nil {
		return nil, newError(CodeProtocolError, "UserResource data was not a valid profile object", 200, "application/json", "", err)
	}
	return &profile, nil
}

type boardsFeedOptions struct {
	Username      string   `json:"username"`
	FieldSetKey   string   `json:"field_set_key"`
	FilterStories bool     `json:"filter_stories"`
	Sort          string   `json:"sort"`
	Bookmarks     []string `json:"bookmarks,omitempty"`
}

// PublicProfileBoards calls BoardsFeedResource/get with verified guest cursor pagination.
func (c *Client) PublicProfileBoards(ctx context.Context, username, bookmark string) (*PageResult[Board], error) {
	cleanUser, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}
	sourceURL := "/" + url.PathEscape(cleanUser) + "/"
	options := boardsFeedOptions{
		Username:      cleanUser,
		FieldSetKey:   "profile_grid_item",
		FilterStories: false,
		Sort:          "last_pinned_to",
	}
	if bookmark != "" {
		options.Bookmarks = []string{bookmark}
	}
	env, rawBody, err := doResourceRequest(ctx, c, "BoardsFeedResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username].js",
	})
	if err != nil {
		return nil, err
	}
	page, err := decodePinOrBoardPage(env, rawBody, "BoardsFeedResource/get", "A-P", NormalizeBoard)
	if err != nil {
		return nil, err
	}
	return filterValidBoards(page), nil
}

type userPinsOptions struct {
	Username         string   `json:"username"`
	IsOwnProfilePins bool     `json:"is_own_profile_pins"`
	FieldSetKey      string   `json:"field_set_key"`
	AddVase          bool     `json:"add_vase"`
	PageSize         int      `json:"page_size"`
	Gated            bool     `json:"gated"`
	Bookmarks        []string `json:"bookmarks,omitempty"`
}

// PublicProfilePins calls UserPinsResource/get for a user's saved pins grid.
func (c *Client) PublicProfilePins(ctx context.Context, username string, pageSize int, bookmark string, allowUnverified *bool) (*PageResult[Pin], error) {
	cleanUser, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}
	grade := "B"
	if bookmark != "" {
		if !c.canPaginateUnverified(allowUnverified) {
			return nil, newError(CodePaginationNotVerified, "UserPinsResource continuation is not verified as unauthenticated; pass allow_unverified=true to test", 0, "", "", nil)
		}
		grade = "C"
	}
	sourceURL := "/" + url.PathEscape(cleanUser) + "/"
	options := userPinsOptions{
		Username:         cleanUser,
		IsOwnProfilePins: false,
		FieldSetKey:      "mobile_grid_item",
		AddVase:          false,
		PageSize:         pageSize,
		Gated:            false,
	}
	if bookmark != "" {
		options.Bookmarks = []string{bookmark}
	}
	env, rawBody, err := doResourceRequest(ctx, c, "UserPinsResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username].js",
	})
	if err != nil {
		return nil, err
	}
	page, err := decodePinOrBoardPage(env, rawBody, "UserPinsResource/get", grade, NormalizePin)
	if err != nil {
		return nil, err
	}
	return filterValidPins(page), nil
}

type userActivityDataOptions struct {
	PageSize int `json:"page_size"`
}

type userActivityPinsOptions struct {
	Username         string                  `json:"username"`
	UserID           string                  `json:"user_id"`
	IsOwnProfilePins bool                    `json:"is_own_profile_pins"`
	FieldSetKey      string                  `json:"field_set_key"`
	ExcludeAddPinRep bool                    `json:"exclude_add_pin_rep"`
	Gated            bool                    `json:"gated"`
	Data             userActivityDataOptions `json:"data"`
	Bookmarks        []string                `json:"bookmarks,omitempty"`
}

// PublicCreatedPins calls UserActivityPinsResource/get for pins created by username.
func (c *Client) PublicCreatedPins(ctx context.Context, username, userID string, pageSize int, bookmark string, allowUnverified *bool) (*PageResult[Pin], error) {
	cleanUser, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}
	cleanUserID := strings.TrimSpace(userID)
	if cleanUserID == "" {
		return nil, newError(CodeValidationError, "user_id is required for UserActivityPinsResource", 0, "", "", nil)
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}
	grade := "B"
	if bookmark != "" {
		if !c.canPaginateUnverified(allowUnverified) {
			return nil, newError(CodePaginationNotVerified, "UserActivityPinsResource continuation is not verified as unauthenticated; pass allow_unverified=true to test", 0, "", "", nil)
		}
		grade = "C"
	}

	sourceURL := "/" + url.PathEscape(cleanUser) + "/_created/"
	options := userActivityPinsOptions{
		Username:         cleanUser,
		UserID:           cleanUserID,
		IsOwnProfilePins: false,
		FieldSetKey:      "profile_created_grid_item",
		ExcludeAddPinRep: true,
		Gated:            false,
		Data:             userActivityDataOptions{PageSize: pageSize},
	}
	if bookmark != "" {
		options.Bookmarks = []string{bookmark}
	}
	env, rawBody, err := doResourceRequest(ctx, c, "UserActivityPinsResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username]/_created.js",
	})
	if err != nil {
		return nil, err
	}
	page, err := decodePinOrBoardPage(env, rawBody, "UserActivityPinsResource/get", grade, NormalizePin)
	if err != nil {
		return nil, err
	}
	return filterValidPins(page), nil
}

type boardOptions struct {
	Username     string `json:"username"`
	Slug         string `json:"slug"`
	FieldSetKey  string `json:"field_set_key"`
	IsMobileFork bool   `json:"is_mobile_fork"`
	AddFields    string `json:"add_fields"`
}

const defaultBoardAddFields = "board.meal_plan(),mealplan.{id,meal_groups,shopping_lists},meal.{pins()},pin.{id,images[236x,474x,orig],domain,dominant_color,rich_summary(),tracking_params,type}"

// PublicBoard calls BoardResource/get for public board metadata.
func (c *Client) PublicBoard(ctx context.Context, username, slug string) (*Board, error) {
	cleanUser, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}
	cleanSlug := strings.Trim(strings.TrimSpace(slug), "/")
	if cleanSlug == "" {
		return nil, newError(CodeValidationError, "board slug must not be empty", 0, "", "", nil)
	}
	sourceURL := fmt.Sprintf("/%s/%s/", url.PathEscape(cleanUser), url.PathEscape(cleanSlug))
	options := boardOptions{
		Username:     cleanUser,
		Slug:         cleanSlug,
		FieldSetKey:  "profile_grid_item",
		IsMobileFork: true,
		AddFields:    defaultBoardAddFields,
	}
	env, _, err := doResourceRequest(ctx, c, "BoardResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username]/[slug].js",
	})
	if err != nil {
		return nil, err
	}
	board, err := NormalizeBoard(env.ResourceResponse.Data)
	if err != nil {
		return nil, newError(CodeProtocolError, "BoardResource data was not a valid board object", 200, "application/json", "", err)
	}
	if board.Username == "" {
		board.Username = cleanUser
	}
	if board.Slug == "" {
		board.Slug = cleanSlug
	}
	return &board, nil
}

type boardFeedOptions struct {
	BoardID             string    `json:"board_id"`
	FieldSetKey         string    `json:"field_set_key"`
	FilterSectionPins   bool      `json:"filter_section_pins"`
	IsReact             bool      `json:"is_react"`
	Prepend             bool      `json:"prepend"`
	PageSize            int       `json:"page_size"`
	Bookmarks           *[]string `json:"bookmarks"`
	ReduxNormalizeFeed  bool      `json:"redux_normalize_feed"`
	Gated               bool      `json:"gated"`
	AddVase             bool      `json:"add_vase"`
}

// PublicBoardPins calls BoardFeedResource/get for pins inside a public board.
func (c *Client) PublicBoardPins(ctx context.Context, boardID, sourceURL string, pageSize int, bookmark string, allowUnverified *bool) (*PageResult[Pin], error) {
	cleanBoardID := strings.TrimSpace(boardID)
	if cleanBoardID == "" {
		return nil, newError(CodeValidationError, "board_id is required", 0, "", "", nil)
	}
	if sourceURL == "" {
		sourceURL = "/"
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var bookmarksPtr *[]string
	grade := "B"
	if bookmark != "" {
		if !c.canPaginateUnverified(allowUnverified) {
			return nil, newError(CodePaginationNotVerified, "BoardFeedResource continuation is not verified as unauthenticated; pass allow_unverified=true to test", 0, "", "", nil)
		}
		b := []string{bookmark}
		bookmarksPtr = &b
		grade = "C"
	}

	options := boardFeedOptions{
		BoardID:            cleanBoardID,
		FieldSetKey:        "react_grid_pin",
		FilterSectionPins:  false,
		IsReact:            true,
		Prepend:            false,
		PageSize:           pageSize,
		Bookmarks:          bookmarksPtr,
		ReduxNormalizeFeed: true,
		Gated:              false,
		AddVase:            true,
	}
	env, rawBody, err := doResourceRequest(ctx, c, "BoardFeedResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/[username]/[slug].js",
	})
	if err != nil {
		return nil, err
	}
	page, err := decodePinOrBoardPage(env, rawBody, "BoardFeedResource/get", grade, NormalizePin)
	if err != nil {
		return nil, err
	}
	return filterValidPins(page), nil
}

type pinOptions struct {
	ID          string `json:"id"`
	FieldSetKey string `json:"field_set_key"`
	AddFields   string `json:"add_fields,omitempty"`
}

// ExtractPinID normalizes a pin path segment such as "181199584997439257" or
// an SEO slug such as "meadows-necklace--181199584997439257" into the raw pin ID.
func ExtractPinID(rawSegment string) string {
	cleaned := strings.Trim(strings.TrimSpace(rawSegment), "/")
	if idx := strings.LastIndex(cleaned, "--"); idx >= 0 && idx+2 < len(cleaned) {
		return cleaned[idx+2:]
	}
	return cleaned
}

// PublicPin calls PinResource/get with field_set_key="detailed" and extended metadata fields.
func (c *Client) PublicPin(ctx context.Context, rawID string) (*Pin, error) {
	pinID := ExtractPinID(rawID)
	if pinID == "" || strings.ContainsAny(pinID, "/?#\\") {
		return nil, newError(CodeValidationError, "invalid pin id", 0, "", "", nil)
	}
	sourceURL := "/pin/" + url.PathEscape(pinID) + "/"
	options := pinOptions{
		ID:          pinID,
		FieldSetKey: "detailed",
		AddFields:   "pin.gen_ai_topics,pin.is_eligible_for_image_download,pin.is_eligible_for_video_insights",
	}
	env, _, err := doResourceRequest(ctx, c, "PinResource", options, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/pin/[id].js",
	})
	if err != nil {
		return nil, err
	}
	pin, err := NormalizePin(env.ResourceResponse.Data)
	if err != nil {
		return nil, newError(CodeProtocolError, "PinResource data was not a valid pin object", 200, "application/json", "", err)
	}
	if pin.ID == "" {
		pin.ID = pinID
	}
	return &pin, nil
}

type commentsOptions struct {
	AggregatedPinID    string   `json:"aggregated_pin_id"`
	PageSize           int      `json:"page_size"`
	ReduxNormalizeFeed bool     `json:"redux_normalize_feed"`
	IsReversed         bool     `json:"is_reversed"`
	CommentFeaturedIDs []string `json:"comment_featured_ids"`
	Bookmarks          []string `json:"bookmarks,omitempty"`
}

// PublicPinComments calls UnifiedCommentsResource/get for public comments on a pin.
func (c *Client) PublicPinComments(ctx context.Context, rawPinID, aggregatedPinID, bookmark string) (*CommentResult, error) {
	pinID := ExtractPinID(rawPinID)
	if pinID == "" {
		return nil, newError(CodeValidationError, "pin id is required", 0, "", "", nil)
	}
	targetID := strings.TrimSpace(aggregatedPinID)
	if targetID == "" {
		if pin, err := c.PublicPin(ctx, pinID); err == nil && pin.AggregatedPinID != "" {
			targetID = pin.AggregatedPinID
		} else {
			targetID = pinID
		}
	}

	sourceURL := "/pin/" + url.PathEscape(pinID) + "/"
	opts := commentsOptions{
		AggregatedPinID:    targetID,
		PageSize:           10,
		ReduxNormalizeFeed: true,
		IsReversed:         false,
		CommentFeaturedIDs: []string{},
	}
	if bookmark != "" {
		opts.Bookmarks = []string{bookmark}
	}

	env, _, err := doResourceRequest(ctx, c, "UnifiedCommentsResource", opts, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/pin/[id].js",
	})
	if err != nil && targetID != pinID {
		opts.AggregatedPinID = pinID
		targetID = pinID
		env, _, err = doResourceRequest(ctx, c, "UnifiedCommentsResource", opts, requestOpts{
			sourceURL: sourceURL,
			method:    http.MethodGet,
			handler:   "www/pin/[id].js",
		})
	}
	if err != nil {
		if pErr, ok := AsError(err); ok && strings.Contains(pErr.Message, "Aggregated pin data could not be found") {
			return &CommentResult{
				Comments:        []Comment{},
				AggregatedPinID: targetID,
				Endpoint:        "UnifiedCommentsResource/get",
			}, nil
		}
		return nil, err
	}

	rawItems, _, err := extractSearchEnvelope(env.ResourceResponse.Data, "")
	if err != nil {
		return nil, newError(CodeProtocolError, "unexpected UnifiedCommentsResource data shape", 200, "application/json", "", err)
	}
	return &CommentResult{
		Comments:        decodeCommentsList(rawItems),
		Bookmark:        normalizeBookmark(env.ResourceResponse.Bookmark),
		AggregatedPinID: targetID,
		Endpoint:        "UnifiedCommentsResource/get",
	}, nil
}

type commentRepliesOptions struct {
	IsUnifiedComment   bool     `json:"isUnifiedComment"`
	ObjectID           string   `json:"objectId"`
	ReduxNormalizeFeed bool     `json:"redux_normalize_feed"`
	Bookmarks          []string `json:"bookmarks,omitempty"`
}

// PublicCommentReplies calls AggregatedCommentReplyFeedResource/get for replies to a unified comment.
func (c *Client) PublicCommentReplies(ctx context.Context, rawCommentID, bookmark string) (*CommentResult, error) {
	commentID := strings.TrimSpace(rawCommentID)
	if commentID == "" || strings.ContainsAny(commentID, "/?#\\") {
		return nil, newError(CodeValidationError, "comment id is required", 0, "", "", nil)
	}
	opts := commentRepliesOptions{
		IsUnifiedComment:   true,
		ObjectID:           commentID,
		ReduxNormalizeFeed: true,
	}
	if bookmark != "" {
		opts.Bookmarks = []string{bookmark}
	}
	env, _, err := doResourceRequest(ctx, c, "AggregatedCommentReplyFeedResource", opts, requestOpts{
		sourceURL: "/pin/",
		method:    http.MethodGet,
		handler:   "www/pin/[id].js",
	})
	if err != nil {
		return nil, err
	}
	rawItems, _, err := extractSearchEnvelope(env.ResourceResponse.Data, "")
	if err != nil {
		return nil, newError(CodeProtocolError, "unexpected AggregatedCommentReplyFeedResource data shape", 200, "application/json", "", err)
	}
	return &CommentResult{
		Comments: decodeCommentsList(rawItems),
		Bookmark: normalizeBookmark(env.ResourceResponse.Bookmark),
		Endpoint: "AggregatedCommentReplyFeedResource/get",
	}, nil
}

func decodeCommentsList(rawItems []json.RawMessage) []Comment {
	comments := make([]Comment, 0, len(rawItems))
	for _, item := range rawItems {
		var w rawCommentWire
		if err := json.Unmarshal(item, &w); err != nil {
			continue
		}
		likes := w.LikeCount
		if likes == 0 && w.ReactionCounts != nil {
			likes = w.ReactionCounts["1"]
		}
		text := strings.TrimSpace(w.Text)
		if text == "" && w.Type == "userdiditdata" {
			text = strings.TrimSpace(w.Details)
		}
		var imgs []CommentImage
		for _, img := range w.Images {
			if img.Originals.URL != "" {
				imgs = append(imgs, CommentImage{
					URL:      img.Originals.URL,
					ProxyURL: ProxyPath(img.Originals.URL),
					Width:    img.Originals.Width,
					Height:   img.Originals.Height,
				})
			}
		}
		avatar := firstNonEmpty(w.User.ImageLargeURL, w.User.ImageMediumURL, w.User.ImageSmallURL)
		comments = append(comments, Comment{
			ID:                w.ID,
			Type:              w.Type,
			Text:              text,
			AuthorName:        firstNonEmpty(w.User.FullName, w.User.FirstName, w.User.Username, "Pinterest User"),
			AuthorUsername:    w.User.Username,
			AuthorAvatar:      avatar,
			ProxyAuthorAvatar: ProxyPath(avatar),
			CreatedAt:         w.CreatedAt,
			Likes:             likes,
			ReplyCount:        w.CommentCount,
			Images:            imgs,
		})
	}
	return comments
}

type ideasOptions struct {
	Interest    string   `json:"interest"`
	FieldSetKey string   `json:"field_set_key"`
	AddVase     bool     `json:"add_vase"`
	ImgNII      bool     `json:"img_nii"`
	OverrideIDs []string `json:"override_ids"`
	Bookmarks   []string `json:"bookmarks,omitempty"`
}

// PublicIdeas calls BestPinsFeedAltResource/get for a Pinterest /ideas/{slug}/{id}/ page.
func (c *Client) PublicIdeas(ctx context.Context, slug, interestID, bookmark string) (*PageResult[Pin], error) {
	cleanID := strings.Trim(strings.TrimSpace(interestID), "/")
	if cleanID == "" {
		return nil, newError(CodeValidationError, "interest id is required", 0, "", "", nil)
	}
	cleanSlug := strings.Trim(strings.TrimSpace(slug), "/")
	sourceURL := "/ideas/hub/" + url.PathEscape(cleanID) + "/"
	if cleanSlug != "" {
		sourceURL = fmt.Sprintf("/ideas/%s/%s/", url.PathEscape(cleanSlug), url.PathEscape(cleanID))
	}
	opts := ideasOptions{
		Interest:    cleanID,
		FieldSetKey: "unauth_react",
		AddVase:     true,
		ImgNII:      true,
		OverrideIDs: []string{},
	}
	if bookmark != "" {
		opts.Bookmarks = []string{bookmark}
	}
	env, rawBody, err := doResourceRequest(ctx, c, "BestPinsFeedAltResource", opts, requestOpts{
		sourceURL: sourceURL,
		method:    http.MethodGet,
		handler:   "www/ideas/[interest]/[id].js",
	})
	if err != nil {
		return nil, err
	}
	page, err := decodePinOrBoardPage(env, rawBody, "BestPinsFeedAltResource/get", "B", NormalizePin)
	if err != nil {
		return nil, err
	}
	return filterValidPins(page), nil
}

func filterValidPins(page *PageResult[Pin]) *PageResult[Pin] {
	filtered := make([]Pin, 0, len(page.Results))
	for _, p := range page.Results {
		if p.IsPromoted || p.ID == "" || (p.ImageURL == "" && p.VideoURL == "") {
			continue
		}
		filtered = append(filtered, p)
	}
	page.Results = filtered
	return page
}

func filterValidBoards(page *PageResult[Board]) *PageResult[Board] {
	filtered := make([]Board, 0, len(page.Results))
	for _, b := range page.Results {
		if b.ID == "" || strings.HasPrefix(b.ID, "-") || b.Slug == "" || b.Name == "" {
			continue
		}
		filtered = append(filtered, b)
	}
	page.Results = filtered
	return page
}

func normalizeUsername(username string) (string, error) {
	u := strings.TrimPrefix(strings.TrimSpace(username), "@")
	u = strings.Trim(u, "/")
	if u == "" || strings.ContainsAny(u, "/?#\\") {
		return "", newError(CodeValidationError, "invalid username", 0, "", "", nil)
	}
	return u, nil
}

func extractSearchEnvelope(data json.RawMessage, scope string) ([]json.RawMessage, []SearchTab, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil, nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []json.RawMessage
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, nil, err
		}
		return list, nil, nil
	}
	var env searchDataEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, nil, err
	}
	items := env.Results
	if len(items) == 0 {
		items = env.Items
	}

	var tabs []SearchTab
	for _, g := range env.RankedGuides {
		if tab, ok := normalizeGuideTab(nil, g, scope); ok {
			tabs = append(tabs, tab)
		}
	}
	for _, m := range env.OneBarModules {
		if label := strings.TrimSpace(m.Display.DisplayText); label != "" && m.Action.FeedURL != "" {
			tabs = append(tabs, SearchTab{
				ID:    m.ID,
				Label: label,
				Query: parseQueryFromFeedURL(m.Action.FeedURL),
				Scope: scope,
			})
		}
	}
	return items, tabs, nil
}

func parseQueryFromFeedURL(feedURL string) string {
	u, err := url.Parse(feedURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("q")
}

func decodePinOrBoardPage[T any](env *resourceEnvelope, rawBody json.RawMessage, endpoint, grade string, convert func(json.RawMessage) (T, error)) (*PageResult[T], error) {
	items, _, err := extractSearchEnvelope(env.ResourceResponse.Data, "")
	if err != nil {
		return nil, newError(CodeProtocolError, fmt.Sprintf("unexpected %s data shape", endpoint), 200, "application/json", "", err)
	}
	results := make([]T, 0, len(items))
	for _, item := range items {
		converted, err := convert(item)
		if err != nil {
			continue
		}
		results = append(results, converted)
	}
	bookmark := normalizeBookmark(env.ResourceResponse.Bookmark)
	return &PageResult[T]{
		Results:       results,
		Bookmark:      bookmark,
		Endpoint:      endpoint,
		EvidenceGrade: grade,
		Raw:           rawBody,
	}, nil
}

func normalizeBookmark(bm *string) string {
	if bm == nil {
		return ""
	}
	v := strings.TrimSpace(*bm)
	if v == "" || v == "-end-" {
		return ""
	}
	return v
}

func mapPageResults[T any](src *PageResult[json.RawMessage], convert func(json.RawMessage) (T, error)) *PageResult[T] {
	results := make([]T, 0, len(src.Results))
	for _, item := range src.Results {
		converted, err := convert(item)
		if err != nil {
			continue
		}
		results = append(results, converted)
	}
	return &PageResult[T]{
		Results:       results,
		Bookmark:      src.Bookmark,
		Endpoint:      src.Endpoint,
		EvidenceGrade: src.EvidenceGrade,
		Tabs:          src.Tabs,
		Raw:           src.Raw,
	}
}
