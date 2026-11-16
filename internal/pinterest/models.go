package pinterest

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// ImageVariant represents a single image rendition from Pinterest's CDN.
type ImageVariant struct {
	URL      string `json:"url"`
	ProxyURL string `json:"proxy_url,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// VideoVariant represents a single video stream or progressive download rendition.
type VideoVariant struct {
	Key            string  `json:"key"`
	URL            string  `json:"url"`
	ProxyURL       string  `json:"proxy_url,omitempty"`
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	Duration       float64 `json:"duration,omitempty"`
	Thumbnail      string  `json:"thumbnail,omitempty"`
	ProxyThumbnail string  `json:"proxy_thumbnail,omitempty"`
}

// OwnerSummary describes a pin or board creator.
type OwnerSummary struct {
	ID             string `json:"id,omitempty"`
	Username       string `json:"username,omitempty"`
	FullName       string `json:"full_name,omitempty"`
	AvatarURL      string `json:"avatar_url,omitempty"`
	ProxyAvatarURL string `json:"proxy_avatar_url,omitempty"`
}

// BoardSummary describes the parent board attached to a pin.
type BoardSummary struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	URL           string `json:"url,omitempty"`
	Username      string `json:"username,omitempty"`
	Slug          string `json:"slug,omitempty"`
	CoverURL      string `json:"cover_url,omitempty"`
	ProxyCoverURL string `json:"proxy_cover_url,omitempty"`
}

// Pin is a normalized Pinterest image or video pin that retains its raw JSON.
type Pin struct {
	ID                    string                  `json:"id"`
	NodeID                string                  `json:"node_id,omitempty"`
	AggregatedPinID       string                  `json:"aggregated_pin_id,omitempty"`
	Type                  string                  `json:"type,omitempty"`
	Title                 string                  `json:"title,omitempty"`
	SEOTitle              string                  `json:"seo_title,omitempty"`
	Description           string                  `json:"description,omitempty"`
	DescriptionHTML       string                  `json:"description_html,omitempty"`
	AltText               string                  `json:"alt_text,omitempty"`
	Domain                string                  `json:"domain,omitempty"`
	Link                  string                  `json:"link,omitempty"`
	DominantColor         string                  `json:"dominant_color,omitempty"`
	CreatedAt             string                  `json:"created_at,omitempty"`
	RichTitle             string                  `json:"rich_title,omitempty"`
	RichDescription       string                  `json:"rich_description,omitempty"`
	SiteName              string                  `json:"site_name,omitempty"`
	Hashtags              []string                `json:"hashtags,omitempty"`
	VisualTags            []string                `json:"visual_tags,omitempty"`
	Likes                 int                     `json:"likes,omitempty"`
	Saves                 int                     `json:"saves,omitempty"`
	Repins                int                     `json:"repins,omitempty"`
	CommentCount          int                     `json:"comment_count,omitempty"`
	ShareCount            int                     `json:"share_count,omitempty"`
	CommentsDisabled      bool                    `json:"comments_disabled,omitempty"`
	IsPromoted            bool                    `json:"is_promoted,omitempty"`
	ImageURL              string                  `json:"image_url,omitempty"`
	ProxyImageURL         string                  `json:"proxy_image_url,omitempty"`
	FallbackImageURL      string                  `json:"fallback_image_url,omitempty"`
	ProxyFallbackImageURL string                  `json:"proxy_fallback_image_url,omitempty"`
	IsHEIC                bool                    `json:"is_heic,omitempty"`
	Width                 int                     `json:"width,omitempty"`
	Height                int                     `json:"height,omitempty"`
	Images                map[string]ImageVariant `json:"images,omitempty"`
	IsVideo               bool                    `json:"is_video"`
	Duration              float64                 `json:"duration,omitempty"`
	VideoURL              string                  `json:"video_url,omitempty"`
	ProxyVideoURL         string                  `json:"proxy_video_url,omitempty"`
	HLSVideoURL           string                  `json:"hls_video_url,omitempty"`
	ProxyHLSVideoURL      string                  `json:"proxy_hls_video_url,omitempty"`
	MP4VideoURL           string                  `json:"mp4_video_url,omitempty"`
	ProxyMP4VideoURL      string                  `json:"proxy_mp4_video_url,omitempty"`
	Videos                map[string]VideoVariant `json:"videos,omitempty"`
	Pinner                OwnerSummary            `json:"pinner,omitempty"`
	Board                 BoardSummary            `json:"board,omitempty"`
	Raw                   json.RawMessage         `json:"raw,omitempty"`
}

// CommentImage represents an inline image attached to a Pinterest comment.
type CommentImage struct {
	URL      string `json:"url"`
	ProxyURL string `json:"proxy_url,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// Comment represents a public comment or "Tried It" note on a pin.
type Comment struct {
	ID                string         `json:"id"`
	Type              string         `json:"type,omitempty"`
	Text              string         `json:"text"`
	AuthorName        string         `json:"author_name"`
	AuthorUsername    string         `json:"author_username,omitempty"`
	AuthorAvatar      string         `json:"author_avatar,omitempty"`
	ProxyAuthorAvatar string         `json:"proxy_author_avatar,omitempty"`
	CreatedAt         string         `json:"created_at,omitempty"`
	Likes             int            `json:"likes,omitempty"`
	ReplyCount        int            `json:"reply_count,omitempty"`
	Images            []CommentImage `json:"images,omitempty"`
}

// CommentResult is a paginated UnifiedCommentsResource response.
type CommentResult struct {
	Comments        []Comment `json:"comments"`
	Bookmark        string    `json:"bookmark,omitempty"`
	AggregatedPinID string    `json:"aggregated_pin_id,omitempty"`
	Endpoint        string    `json:"endpoint"`
}

// Board is a normalized public Pinterest board that retains its raw JSON.
type Board struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	URL             string          `json:"url,omitempty"`
	Username        string          `json:"username,omitempty"`
	Slug            string          `json:"slug,omitempty"`
	Privacy         string          `json:"privacy,omitempty"`
	Category        string          `json:"category,omitempty"`
	PinCount        int             `json:"pin_count,omitempty"`
	FollowerCount   int             `json:"follower_count,omitempty"`
	CoverURL        string          `json:"cover_url,omitempty"`
	ProxyCoverURL   string          `json:"proxy_cover_url,omitempty"`
	Thumbnails      []string        `json:"thumbnails,omitempty"`
	ProxyThumbnails []string        `json:"proxy_thumbnails,omitempty"`
	Owner           OwnerSummary    `json:"owner,omitempty"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// UserProfile is a normalized public Pinterest profile from UserResource unauth_profile.
type UserProfile struct {
	ID                 string          `json:"id,omitempty"`
	Username           string          `json:"username"`
	FullName           string          `json:"full_name,omitempty"`
	FirstName          string          `json:"first_name,omitempty"`
	LastName           string          `json:"last_name,omitempty"`
	About              string          `json:"about,omitempty"`
	WebsiteURL         string          `json:"website_url,omitempty"`
	AvatarURL          string          `json:"avatar_url,omitempty"`
	ProxyAvatarURL     string          `json:"proxy_avatar_url,omitempty"`
	BannerURL          string          `json:"banner_url,omitempty"`
	ProxyBannerURL     string          `json:"proxy_banner_url,omitempty"`
	PinCount           int             `json:"pin_count,omitempty"`
	BoardCount         int             `json:"board_count,omitempty"`
	FollowerCount      int             `json:"follower_count,omitempty"`
	FollowingCount     int             `json:"following_count,omitempty"`
	MonthlyViews       int             `json:"monthly_views,omitempty"`
	IsVerifiedMerchant bool            `json:"is_verified_merchant,omitempty"`
	Raw                json.RawMessage `json:"raw,omitempty"`
}

// Suggestion represents a typeahead autocomplete result from AdvancedTypeaheadResource.
type Suggestion struct {
	Query     string          `json:"query"`
	Label     string          `json:"label"`
	Type      string          `json:"type,omitempty"`
	Username  string          `json:"username,omitempty"`
	ImageURL  string          `json:"image_url,omitempty"`
	ProxyURL  string          `json:"proxy_url,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// SearchTab represents a search filter or guide tab from SearchTabResource or search guides.
type SearchTab struct {
	ID            string          `json:"id,omitempty"`
	Label         string          `json:"label"`
	Query         string          `json:"query,omitempty"`
	Scope         string          `json:"scope,omitempty"`
	FilterGenAI   bool            `json:"filter_genai,omitempty"`
	IsGenAIFilter bool            `json:"is_genai_filter,omitempty"`
	IsSelected    bool            `json:"is_selected,omitempty"`
	DominantColor string          `json:"dominant_color,omitempty"`
	ImageURL      string          `json:"image_url,omitempty"`
	ProxyImageURL string          `json:"proxy_image_url,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

// PageResult represents a paginated resource slice with its opaque bookmark cursor.
type PageResult[T any] struct {
	Results       []T             `json:"results"`
	Bookmark      string          `json:"bookmark,omitempty"`
	Endpoint      string          `json:"endpoint"`
	EvidenceGrade string          `json:"evidence_grade,omitempty"`
	Tabs          []SearchTab     `json:"tabs,omitempty"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

// ProxyPath converts an allowlisted Pinterest media URL to a local proxy route path.
func ProxyPath(rawURL string) string {
	u, err := ValidateMediaURL(rawURL)
	if err != nil {
		return ""
	}
	return "/api/v1/media/proxy?url=" + url.QueryEscape(u.String())
}

// Wire decoding structs prevent map[string]any type evasion while preserving forward compatibility.

type resourceEnvelope struct {
	ResourceResponse  *resourceResponseEnvelope `json:"resource_response"`
	RequestIdentifier string                    `json:"request_identifier"`
}

type resourceResponseEnvelope struct {
	Status       string          `json:"status"`
	Code         int             `json:"code"`
	Message      string          `json:"message"`
	EndpointName string          `json:"endpoint_name"`
	Data         json.RawMessage `json:"data"`
	Bookmark     *string         `json:"bookmark"`
	HTTPStatus   int             `json:"http_status"`
}

type searchDataEnvelope struct {
	Results       []json.RawMessage     `json:"results"`
	Items         []json.RawMessage     `json:"items"`
	RankedGuides  []rawGuideWire        `json:"rankedGuides"`
	OneBarModules []rawOneBarModuleWire `json:"oneBarModules"`
}

type rawImageWire struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type rawVideoWire struct {
	URL       string  `json:"url"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Duration  float64 `json:"duration"`
	Thumbnail string  `json:"thumbnail"`
}

type rawVideosWire struct {
	Duration  float64                 `json:"duration"`
	VideoList map[string]rawVideoWire `json:"video_list"`
}

type rawOwnerWire struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	FullName       string `json:"full_name"`
	FirstName      string `json:"first_name"`
	ImageMediumURL string `json:"image_medium_url"`
	ImageSmallURL  string `json:"image_small_url"`
	ImageLargeURL  string `json:"image_large_url"`
}

type rawBoardSummaryWire struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	ImageCoverURL   string `json:"image_cover_url"`
	ImageCoverHDURL string `json:"image_cover_hd_url"`
}

type rawStoryPinDataWire struct {
	TotalVideoDuration float64 `json:"total_video_duration"`
	Pages              []struct {
		Blocks []struct {
			Video *rawVideosWire `json:"video"`
			Image *struct {
				Images map[string]rawImageWire `json:"images"`
			} `json:"image"`
		} `json:"blocks"`
	} `json:"pages"`
}

type rawAggregatedStatsWire struct {
	Saves        int `json:"saves"`
	Comments     int `json:"comments"`
	CommentCount int `json:"comment_count"`
}

type rawAggregatedPinDataWire struct {
	ID              string                  `json:"id"`
	NodeID          string                  `json:"node_id"`
	AggregatedStats *rawAggregatedStatsWire `json:"aggregated_stats"`
	AltStats        *rawAggregatedStatsWire `json:"aggregatedStats"`
}

type rawPinJoinWire struct {
	VisualAnnotation []string `json:"visual_annotation"`
}

type rawRichMetadataWire struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	SiteName    string `json:"site_name"`
}

type rawRichSummaryWire struct {
	TypeName string `json:"type_name"`
}

type rawPinWire struct {
	ID                        string                    `json:"id"`
	EntityID                  string                    `json:"entity_id"`
	NodeID                    string                    `json:"node_id"`
	Type                      string                    `json:"type"`
	Title                     string                    `json:"title"`
	GridTitle                 string                    `json:"grid_title"`
	SEOTitle                  string                    `json:"seo_title"`
	Description               string                    `json:"description"`
	DescriptionHTML           string                    `json:"description_html"`
	CloseupUnifiedDescription string                    `json:"closeup_unified_description"`
	CloseupDescription        string                    `json:"closeup_description"`
	AltText                   string                    `json:"alt_text"`
	AutoAltText               string                    `json:"auto_alt_text"`
	SEOAltText                string                    `json:"seo_alt_text"`
	Domain                    string                    `json:"domain"`
	Link                      string                    `json:"link"`
	DominantColor             string                    `json:"dominant_color"`
	CreatedAt                 string                    `json:"created_at"`
	Hashtags                  []string                  `json:"hashtags"`
	PinJoin                   *rawPinJoinWire           `json:"pin_join"`
	ReactionCounts            map[string]int            `json:"reaction_counts"`
	RepinCount                int                       `json:"repin_count"`
	RepinCountAlt             int                       `json:"repinCount"`
	CommentCount              int                       `json:"comment_count"`
	ShareCount                int                       `json:"share_count"`
	CommentsDisabled          bool                      `json:"comments_disabled"`
	CommentsDisabledAlt       bool                      `json:"commentsDisabled"`
	IsPromoted                bool                      `json:"is_promoted"`
	Method                    string                    `json:"method"`
	RichSummary               *rawRichSummaryWire       `json:"rich_summary"`
	RichMetadata              *rawRichMetadataWire      `json:"rich_metadata"`
	AggregatedPinData         *rawAggregatedPinDataWire `json:"aggregated_pin_data"`
	AggregatedPinDataAlt      *rawAggregatedPinDataWire `json:"aggregatedPinData"`
	Images                    map[string]rawImageWire   `json:"images"`
	Videos                    *rawVideosWire            `json:"videos"`
	Video                     *rawVideosWire            `json:"video"`
	VideoList                 map[string]rawVideoWire   `json:"video_list"`
	IsVideo                   bool                      `json:"is_video"`
	Duration                  float64                   `json:"duration"`
	Pinner                    *rawOwnerWire             `json:"pinner"`
	CloseupAttribution        *rawOwnerWire             `json:"closeup_attribution"`
	NativeCreator             *rawOwnerWire             `json:"native_creator"`
	Board                     *rawBoardSummaryWire      `json:"board"`
	StoryPinData              *rawStoryPinDataWire      `json:"story_pin_data"`
}

type rawCommentWire struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Text           string         `json:"text"`
	Details        string         `json:"details"`
	CreatedAt      string         `json:"created_at"`
	CommentCount   int            `json:"comment_count"`
	LikeCount      int            `json:"like_count"`
	ReactionCounts map[string]int `json:"reaction_counts"`
	User           rawOwnerWire   `json:"user"`
	Images         []struct {
		Originals rawImageWire `json:"originals"`
	} `json:"images"`
}

type rawBoardWire struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	URL               string                  `json:"url"`
	Privacy           string                  `json:"privacy"`
	Category          string                  `json:"category"`
	PinCount          int                     `json:"pin_count"`
	FollowerCount     int                     `json:"follower_count"`
	ImageCoverURL     string                  `json:"image_cover_url"`
	ImageCoverHDURL   string                  `json:"image_cover_hd_url"`
	ImageThumbnailURL string                  `json:"image_thumbnail_url"`
	PinThumbnailURLs  []string                `json:"pin_thumbnail_urls"`
	CoverImages       map[string]rawImageWire `json:"cover_images"`
	Owner             *rawOwnerWire           `json:"owner"`
}

type rawProfileCoverWire struct {
	SourceURL string                  `json:"source_url"`
	Images    map[string]rawImageWire `json:"images"`
}

type rawUserWire struct {
	ID                 string               `json:"id"`
	Username           string               `json:"username"`
	FullName           string               `json:"full_name"`
	FirstName          string               `json:"first_name"`
	LastName           string               `json:"last_name"`
	About              string               `json:"about"`
	WebsiteURL         string               `json:"website_url"`
	DomainURL          string               `json:"domain_url"`
	ImageXLargeURL     string               `json:"image_xlarge_url"`
	ImageLargeURL      string               `json:"image_large_url"`
	ImageMediumURL     string               `json:"image_medium_url"`
	ProfileCover       *rawProfileCoverWire `json:"profile_cover"`
	PinCount           int                  `json:"pin_count"`
	BoardCount         int                  `json:"board_count"`
	FollowerCount      int                  `json:"follower_count"`
	FollowingCount     int                  `json:"following_count"`
	MonthlyViews       int                  `json:"monthly_views"`
	IsVerifiedMerchant bool                 `json:"is_verified_merchant"`
}

type rawSuggestionWire struct {
	Query    string `json:"query"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Text     string `json:"text"`
	Type     string `json:"type"`
	Username string `json:"username"`
	ImageURL string `json:"image_url"`
}

type rawGuideWire struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Display        string `json:"display"`
	Term           string `json:"term"`
	Query          string `json:"query"`
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	DominantColor  string `json:"dominant_color"`
	ImageURL       string `json:"image_url"`
	ImageMediumURL string `json:"image_medium_url"`
}

type rawOneBarDisplayWire struct {
	DisplayText string `json:"display_text"`
	IsSelected  bool   `json:"is_selected"`
}

type rawOneBarActionWire struct {
	FeedURL string `json:"feed_url"`
	Title   string `json:"title"`
	Filters []struct {
		Display rawOneBarDisplayWire `json:"display"`
		Action  struct {
			FeedURL string `json:"feed_url"`
		} `json:"action"`
	} `json:"filters"`
}

type rawOneBarModuleWire struct {
	ID      string               `json:"id"`
	Type    string               `json:"type"`
	Display rawOneBarDisplayWire `json:"display"`
	Action  rawOneBarActionWire  `json:"action"`
	AuxData struct {
		ModuleType string `json:"module_type"`
	} `json:"aux_data"`
}

var defaultPreferredImageOrder = []string{"orig", "736x", "564x", "474x", "236x"}

// BestImageVariant selects the highest-priority image variant from a Pin's image map.
func BestImageVariant(images map[string]ImageVariant, preferred ...string) (ImageVariant, bool) {
	if len(images) == 0 {
		return ImageVariant{}, false
	}
	order := preferred
	if len(order) == 0 {
		order = defaultPreferredImageOrder
	}
	for _, key := range order {
		if v, ok := images[key]; ok && v.URL != "" {
			return v, true
		}
	}
	keys := make([]string, 0, len(images))
	for k, v := range images {
		if v.URL != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		return images[k], true
	}
	return ImageVariant{}, false
}

// BestVideoVariant picks the highest-priority playable video variant.
func BestVideoVariant(videos map[string]VideoVariant) (VideoVariant, bool) {
	if len(videos) == 0 {
		return VideoVariant{}, false
	}
	preferred := []string{"V_720P", "V_720P_MP4", "V_EXP7", "V_HLSV4", "V_HLSV3_MOBILE", "V_HLSV3_WEB"}
	for _, key := range preferred {
		if v, ok := videos[key]; ok && v.URL != "" {
			return v, true
		}
	}
	keys := make([]string, 0, len(videos))
	for k, v := range videos {
		if v.URL != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		return videos[k], true
	}
	return VideoVariant{}, false
}

func bestMatchingVideo(videos map[string]VideoVariant, keys []string) (VideoVariant, bool) {
	for _, key := range keys {
		if v, ok := videos[key]; ok && v.URL != "" {
			return v, true
		}
	}
	return VideoVariant{}, false
}

func isHEICURL(u string) bool {
	low := strings.ToLower(u)
	return strings.HasSuffix(low, ".heic") || strings.HasSuffix(low, ".heif")
}

func bestNonHEICVariant(images map[string]ImageVariant) (ImageVariant, bool) {
	for _, key := range []string{"736x", "564x", "474x", "236x"} {
		if v, ok := images[key]; ok && v.URL != "" && !isHEICURL(v.URL) {
			return v, true
		}
	}
	for _, v := range images {
		if v.URL != "" && !isHEICURL(v.URL) {
			return v, true
		}
	}
	return ImageVariant{}, false
}

func normalizeDurationSeconds(raw float64) float64 {
	if raw <= 0 {
		return 0
	}
	// Pinterest video objects typically report duration in milliseconds (e.g., 19600).
	if raw >= 1000 {
		return raw / 1000.0
	}
	return raw
}

func normalizeOwner(w *rawOwnerWire) OwnerSummary {
	if w == nil {
		return OwnerSummary{}
	}
	avatar := firstNonEmpty(w.ImageLargeURL, w.ImageMediumURL, w.ImageSmallURL)
	return OwnerSummary{
		ID:             w.ID,
		Username:       w.Username,
		FullName:       firstNonEmpty(w.FullName, w.FirstName),
		AvatarURL:      avatar,
		ProxyAvatarURL: ProxyPath(avatar),
	}
}

func parseBoardURLParts(boardURL string) (username, slug string) {
	trimmed := strings.Trim(boardURL, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

// NormalizePin converts a raw Pinterest pin JSON payload into a strongly typed Pin.
func NormalizePin(raw json.RawMessage) (Pin, error) {
	var w rawPinWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Pin{}, err
	}

	images := make(map[string]ImageVariant, len(w.Images))
	for k, img := range w.Images {
		if img.URL == "" {
			continue
		}
		images[k] = ImageVariant{
			URL:      img.URL,
			ProxyURL: ProxyPath(img.URL),
			Width:    img.Width,
			Height:   img.Height,
		}
	}
	if len(images) == 0 && w.StoryPinData != nil {
		for _, page := range w.StoryPinData.Pages {
			for _, blk := range page.Blocks {
				if blk.Image != nil {
					for k, img := range blk.Image.Images {
						if img.URL != "" {
							images[k] = ImageVariant{
								URL:      img.URL,
								ProxyURL: ProxyPath(img.URL),
								Width:    img.Width,
								Height:   img.Height,
							}
						}
					}
				}
			}
			if len(images) > 0 {
				break
			}
		}
	}

	rawVideos := w.VideoList
	if len(rawVideos) == 0 && w.Videos != nil {
		rawVideos = w.Videos.VideoList
	}
	if len(rawVideos) == 0 && w.Video != nil {
		rawVideos = w.Video.VideoList
	}
	if len(rawVideos) == 0 && w.StoryPinData != nil {
		for _, page := range w.StoryPinData.Pages {
			for _, blk := range page.Blocks {
				if blk.Video != nil && len(blk.Video.VideoList) > 0 {
					rawVideos = blk.Video.VideoList
					break
				}
			}
			if len(rawVideos) > 0 {
				break
			}
		}
	}

	videos := make(map[string]VideoVariant, len(rawVideos))
	var maxDuration float64
	for k, v := range rawVideos {
		if v.URL == "" {
			continue
		}
		if v.Duration > maxDuration {
			maxDuration = v.Duration
		}
		videos[k] = VideoVariant{
			Key:            k,
			URL:            v.URL,
			ProxyURL:       ProxyPath(v.URL),
			Width:          v.Width,
			Height:         v.Height,
			Duration:       v.Duration,
			Thumbnail:      v.Thumbnail,
			ProxyThumbnail: ProxyPath(v.Thumbnail),
		}
	}

	bestImg, hasImg := BestImageVariant(images)
	bestVid, hasVid := BestVideoVariant(videos)

	owner := normalizeOwner(w.Pinner)
	if w.CloseupAttribution != nil {
		ca := normalizeOwner(w.CloseupAttribution)
		if ca.FullName != "" {
			owner.FullName = ca.FullName
		}
		if ca.AvatarURL != "" {
			owner.AvatarURL = ca.AvatarURL
			owner.ProxyAvatarURL = ca.ProxyAvatarURL
		}
		if owner.Username == "" {
			owner.Username = ca.Username
		}
	}
	if owner.Username == "" && w.NativeCreator != nil {
		nc := normalizeOwner(w.NativeCreator)
		if owner.FullName == "" {
			owner = nc
		} else {
			owner.Username = nc.Username
		}
	}

	var board BoardSummary
	if w.Board != nil {
		u, s := parseBoardURLParts(w.Board.URL)
		cover := firstNonEmpty(w.Board.ImageCoverHDURL, w.Board.ImageCoverURL)
		board = BoardSummary{
			ID:            w.Board.ID,
			Name:          w.Board.Name,
			URL:           w.Board.URL,
			Username:      u,
			Slug:          s,
			CoverURL:      cover,
			ProxyCoverURL: ProxyPath(cover),
		}
	}

	duration := w.Duration
	if duration == 0 && w.Videos != nil && w.Videos.Duration > 0 {
		duration = w.Videos.Duration
	}
	if duration == 0 && w.StoryPinData != nil && w.StoryPinData.TotalVideoDuration > 0 {
		duration = w.StoryPinData.TotalVideoDuration
	}
	if duration == 0 {
		duration = maxDuration
	}
	duration = normalizeDurationSeconds(duration)
	isVideo := w.IsVideo || hasVid

	var aggID string
	var saves int
	commentCount := w.CommentCount
	agg := w.AggregatedPinData
	if agg == nil {
		agg = w.AggregatedPinDataAlt
	}
	if agg != nil {
		aggID = agg.ID
		if aggID == "" && agg.NodeID != "" {
			aggID = decodeNodeIDSuffix(agg.NodeID, "AggregatedPinData:")
		}
		stats := agg.AggregatedStats
		if stats == nil {
			stats = agg.AltStats
		}
		if stats != nil {
			saves = stats.Saves
			if stats.Comments > commentCount {
				commentCount = stats.Comments
			}
			if stats.CommentCount > commentCount {
				commentCount = stats.CommentCount
			}
		}
	}

	var visualTags []string
	if w.PinJoin != nil && len(w.PinJoin.VisualAnnotation) > 0 {
		visualTags = w.PinJoin.VisualAnnotation
	}

	repins := w.RepinCount
	if repins == 0 {
		repins = w.RepinCountAlt
	}
	likes := 0
	if w.ReactionCounts != nil {
		likes = w.ReactionCounts["1"]
	}

	var richTitle, richDesc, siteName string
	if w.RichMetadata != nil {
		richTitle = w.RichMetadata.Title
		richDesc = w.RichMetadata.Description
		siteName = w.RichMetadata.SiteName
	}

	isPromoted := w.IsPromoted ||
		w.Method == "catalog_bulk_create" ||
		(w.RichSummary != nil && strings.EqualFold(w.RichSummary.TypeName, "product"))

	pinID := firstNonEmpty(w.EntityID, w.ID)
	desc := strings.TrimSpace(firstNonEmpty(w.Description, w.CloseupUnifiedDescription, w.CloseupDescription))

	pin := Pin{
		ID:               pinID,
		NodeID:           w.NodeID,
		AggregatedPinID:  aggID,
		Type:             firstNonEmpty(w.Type, "pin"),
		Title:            firstNonEmpty(w.Title, w.GridTitle),
		SEOTitle:         w.SEOTitle,
		Description:      desc,
		DescriptionHTML:  w.DescriptionHTML,
		AltText:          firstNonEmpty(w.AltText, w.SEOAltText, w.AutoAltText),
		Domain:           w.Domain,
		Link:             w.Link,
		DominantColor:    w.DominantColor,
		CreatedAt:        w.CreatedAt,
		RichTitle:        richTitle,
		RichDescription:  richDesc,
		SiteName:         siteName,
		Hashtags:         w.Hashtags,
		VisualTags:       visualTags,
		Likes:            likes,
		Saves:            saves,
		Repins:           repins,
		CommentCount:     commentCount,
		ShareCount:       w.ShareCount,
		CommentsDisabled: w.CommentsDisabled || w.CommentsDisabledAlt,
		IsPromoted:       isPromoted,
		Images:           images,
		IsVideo:          isVideo,
		Duration:         duration,
		Videos:           videos,
		Pinner:           owner,
		Board:            board,
		Raw:              append(json.RawMessage(nil), raw...),
	}
	if hasImg {
		pin.ImageURL = bestImg.URL
		pin.ProxyImageURL = bestImg.ProxyURL
		pin.FallbackImageURL = bestImg.URL
		pin.ProxyFallbackImageURL = bestImg.ProxyURL
		pin.IsHEIC = isHEICURL(bestImg.URL)
		if pin.IsHEIC {
			if fb, ok := bestNonHEICVariant(images); ok {
				pin.FallbackImageURL = fb.URL
				pin.ProxyFallbackImageURL = fb.ProxyURL
			}
		}
		pin.Width = bestImg.Width
		pin.Height = bestImg.Height
	} else if hasVid && bestVid.Thumbnail != "" {
		pin.ImageURL = bestVid.Thumbnail
		pin.ProxyImageURL = bestVid.ProxyThumbnail
		pin.FallbackImageURL = bestVid.Thumbnail
		pin.ProxyFallbackImageURL = bestVid.ProxyThumbnail
		pin.Width = bestVid.Width
		pin.Height = bestVid.Height
	}
	if hasVid {
		if hls, ok := bestMatchingVideo(videos, []string{"V_HLSV4", "V_HLSV3_MOBILE", "V_HLSV3_WEB"}); ok {
			pin.HLSVideoURL = hls.URL
			pin.ProxyHLSVideoURL = hls.ProxyURL
		}
		if mp4, ok := bestMatchingVideo(videos, []string{"V_1080P", "V_1080P_MP4", "V_720P", "V_720P_MP4", "V_EXP7", "V_480P", "V_360P"}); ok {
			pin.MP4VideoURL = mp4.URL
			pin.ProxyMP4VideoURL = mp4.ProxyURL
		}
		pin.VideoURL = firstNonEmpty(pin.HLSVideoURL, pin.MP4VideoURL, bestVid.URL)
		pin.ProxyVideoURL = ProxyPath(pin.VideoURL)
		if bestVid.Width > 0 && bestVid.Height > 0 {
			pin.Width = bestVid.Width
			pin.Height = bestVid.Height
		}
	}
	return pin, nil
}

// NormalizeBoard converts a raw Pinterest board JSON object into a strongly typed Board.
func NormalizeBoard(raw json.RawMessage) (Board, error) {
	var w rawBoardWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Board{}, err
	}
	u, s := parseBoardURLParts(w.URL)
	owner := normalizeOwner(w.Owner)
	if u == "" {
		u = owner.Username
	}
	cover := firstNonEmpty(w.ImageCoverHDURL, w.ImageCoverURL, w.ImageThumbnailURL)
	if cover == "" && len(w.CoverImages) > 0 {
		for _, img := range w.CoverImages {
			if img.URL != "" {
				cover = img.URL
				break
			}
		}
	}
	if cover == "" && len(w.PinThumbnailURLs) > 0 {
		cover = w.PinThumbnailURLs[0]
	}

	proxyThumbs := make([]string, 0, len(w.PinThumbnailURLs))
	for _, t := range w.PinThumbnailURLs {
		if p := ProxyPath(t); p != "" {
			proxyThumbs = append(proxyThumbs, p)
		}
	}

	return Board{
		ID:              w.ID,
		Name:            w.Name,
		Description:     w.Description,
		URL:             w.URL,
		Username:        u,
		Slug:            s,
		Privacy:         w.Privacy,
		Category:        w.Category,
		PinCount:        w.PinCount,
		FollowerCount:   w.FollowerCount,
		CoverURL:        cover,
		ProxyCoverURL:   ProxyPath(cover),
		Thumbnails:      w.PinThumbnailURLs,
		ProxyThumbnails: proxyThumbs,
		Owner:           owner,
		Raw:             append(json.RawMessage(nil), raw...),
	}, nil
}

// NormalizeUserProfile converts a raw UserResource payload into a UserProfile.
func NormalizeUserProfile(raw json.RawMessage) (UserProfile, error) {
	var w rawUserWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return UserProfile{}, err
	}
	avatar := firstNonEmpty(w.ImageXLargeURL, w.ImageLargeURL, w.ImageMediumURL)
	banner := ""
	if w.ProfileCover != nil {
		banner = w.ProfileCover.SourceURL
		if banner == "" {
			for _, img := range w.ProfileCover.Images {
				if img.URL != "" {
					banner = img.URL
					break
				}
			}
		}
	}
	return UserProfile{
		ID:                 w.ID,
		Username:           w.Username,
		FullName:           firstNonEmpty(w.FullName, strings.TrimSpace(w.FirstName+" "+w.LastName), w.Username),
		FirstName:          w.FirstName,
		LastName:           w.LastName,
		About:              w.About,
		WebsiteURL:         firstNonEmpty(w.WebsiteURL, w.DomainURL),
		AvatarURL:          avatar,
		ProxyAvatarURL:     ProxyPath(avatar),
		BannerURL:          banner,
		ProxyBannerURL:     ProxyPath(banner),
		PinCount:           w.PinCount,
		BoardCount:         w.BoardCount,
		FollowerCount:      w.FollowerCount,
		FollowingCount:     w.FollowingCount,
		MonthlyViews:       w.MonthlyViews,
		IsVerifiedMerchant: w.IsVerifiedMerchant,
		Raw:                append(json.RawMessage(nil), raw...),
	}, nil
}

// NormalizeSuggestion converts a raw AdvancedTypeaheadResource item into a Suggestion.
func NormalizeSuggestion(raw json.RawMessage) (Suggestion, error) {
	var w rawSuggestionWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Suggestion{}, err
	}
	label := firstNonEmpty(w.Label, w.Query, w.Value, w.Text, w.Username)
	query := firstNonEmpty(w.Query, w.Value, w.Text, w.Label, w.Username)
	return Suggestion{
		Query:    query,
		Label:    label,
		Type:     w.Type,
		Username: w.Username,
		ImageURL: w.ImageURL,
		ProxyURL: ProxyPath(w.ImageURL),
		Raw:      append(json.RawMessage(nil), raw...),
	}, nil
}

func normalizeGuideTab(raw json.RawMessage, g rawGuideWire, defaultScope string) (SearchTab, bool) {
	label := firstNonEmpty(g.Display, g.Label, g.Term, g.Query, g.Name)
	if label == "" {
		return SearchTab{}, false
	}
	query := firstNonEmpty(g.Query, g.Term, g.Display, g.Label)
	scope := firstNonEmpty(g.Scope, defaultScope)
	img := firstNonEmpty(g.ImageURL, g.ImageMediumURL)
	return SearchTab{
		ID:            g.ID,
		Label:         label,
		Query:         query,
		Scope:         scope,
		DominantColor: g.DominantColor,
		ImageURL:      img,
		ProxyImageURL: ProxyPath(img),
		Raw:           append(json.RawMessage(nil), raw...),
	}, true
}

func decodeNodeIDSuffix(nodeID, expectedPrefix string) string {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(nodeID, "="))
	if err != nil {
		return ""
	}
	s := string(decoded)
	if after, ok := strings.CutPrefix(s, expectedPrefix); ok {
		return after
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
