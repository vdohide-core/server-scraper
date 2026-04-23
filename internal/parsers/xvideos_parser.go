package parsers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"server-scraper/internal/utils"

	"github.com/PuerkitoBio/goquery"
)

// XVideosParser parses XVideos.com video pages
type XVideosParser struct{}

// NewXVideosParser creates a new XVideos parser
func NewXVideosParser() *XVideosParser {
	return &XVideosParser{}
}

// GetName returns the parser name
func (p *XVideosParser) GetName() string {
	return "XVideos Parser"
}

// CanHandle checks if this parser can handle the given URL
func (p *XVideosParser) CanHandle(rawURL string) bool {
	domains := []string{
		"xvideos.com", "xvideos.es",
	}
	for _, d := range domains {
		if strings.Contains(rawURL, d) && strings.Contains(rawURL, "/video") {
			return true
		}
	}
	return false
}

// NeedsHTML returns true — XVideos requires HTML scraping
func (p *XVideosParser) NeedsHTML() bool {
	return true
}

// FetchAndParse is not used for XVideos (NeedsHTML = true)
func (p *XVideosParser) FetchAndParse(url string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("XVideos parser requires HTML content, use Parse() instead")
}

// NormalizeURL extracts the encoded video ID and normalizes the URL
func (p *XVideosParser) NormalizeURL(rawURL string) (string, string) {
	// Strip fragment and query
	if idx := strings.Index(rawURL, "#"); idx != -1 {
		rawURL = rawURL[:idx]
	}
	if idx := strings.Index(rawURL, "?"); idx != -1 {
		rawURL = rawURL[:idx]
	}
	rawURL = strings.TrimRight(rawURL, "/")

	// Extract encoded video ID from URL pattern: /video.ENCODED_ID/slug
	// e.g. https://www.xvideos.com/video.kkbdh5c76/mayu_mochizuki
	re := regexp.MustCompile(`/video\.([a-z0-9]+)/`)
	matches := re.FindStringSubmatch(rawURL)
	slug := ""
	if len(matches) > 1 {
		slug = matches[1]
	}

	// Normalize to www.xvideos.com
	re2 := regexp.MustCompile(`https?://[^/]+(/video\..+)`)
	if m := re2.FindStringSubmatch(rawURL); len(m) > 1 {
		rawURL = "https://www.xvideos.com" + m[1]
	}

	return rawURL, slug
}

// Parse extracts data from XVideos HTML
func (p *XVideosParser) Parse(html string) (map[string]interface{}, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	result := make(map[string]interface{})

	// 1. Meta Tags (Open Graph)
	result["title"] = extractMeta(doc, "og:title")
	result["poster"] = extractMeta(doc, "og:image")
	result["sourceUrl"] = extractMeta(doc, "og:url")

	durationStr := extractMeta(doc, "og:duration")
	if durationStr != "" {
		if dur, err := strconv.Atoi(durationStr); err == nil {
			result["duration"] = dur
			hours := dur / 3600
			minutes := (dur % 3600) / 60
			if hours > 0 {
				result["duration_text"] = fmt.Sprintf("%dh %dm", hours, minutes)
			} else {
				result["duration_text"] = fmt.Sprintf("%dm", minutes)
			}
		}
	}

	// 2. Extract video URLs from JavaScript: setVideoHLS, setVideoUrlHigh, setVideoUrlLow
	result["m3u8Url"] = extractJSString(html, `setVideoHLS\('([^']+)'\)`)
	result["videoUrlHigh"] = extractJSString(html, `setVideoUrlHigh\('([^']+)'\)`)
	result["videoUrlLow"] = extractJSString(html, `setVideoUrlLow\('([^']+)'\)`)

	// 3. Extract encoded video ID
	encodedID := extractJSString(html, `setEncodedIdVideo\('([^']+)'\)`)
	if encodedID != "" {
		result["encodedId"] = encodedID
	}

	// 4. Extract tags from HTML tag links
	var tagNames []string
	doc.Find("a.is-keyword").Each(func(i int, s *goquery.Selection) {
		name := strings.TrimSpace(s.Text())
		if name != "" {
			tagNames = append(tagNames, name)
		}
	})
	if len(tagNames) > 0 {
		tags := make([]map[string]string, len(tagNames))
		for i, name := range tagNames {
			tags[i] = map[string]string{
				"id":    utils.RandomString(10, false),
				"value": name,
			}
		}
		result["tags"] = tags
	}

	// 5. Extract upload date from JSON-LD schema
	uploadDate := extractJSONLDField(html, `"uploadDate"\s*:\s*"([^"]+)"`)
	if uploadDate != "" {
		// Take just the date part (YYYY-MM-DD)
		if len(uploadDate) >= 10 {
			result["releaseDate"] = uploadDate[:10]
		}
	}

	// 6. Extract view count from JSON-LD
	viewCount := extractJSONLDField(html, `"userInteractionCount"\s*:\s*(\d+)`)
	if viewCount != "" {
		if views, err := strconv.ParseInt(viewCount, 10, 64); err == nil {
			result["views"] = views
		}
	}

	// 7. Extract thumbnail variations
	thumbSlide := extractJSString(html, `setThumbSlide\('([^']+)'\)`)
	if thumbSlide != "" {
		result["thumbSlide"] = thumbSlide
	}

	// Remove empty string values
	for k, v := range result {
		if str, ok := v.(string); ok && str == "" {
			delete(result, k)
		}
	}

	return result, nil
}

// extractJSString extracts a string value from JavaScript using a regex pattern
func extractJSString(html string, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractJSONLDField extracts a field from JSON-LD schema
func extractJSONLDField(html string, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
