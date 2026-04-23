package parsers

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"server-scraper/internal/utils"

	"github.com/PuerkitoBio/goquery"
)

// PornHubParser parses PornHub.com video pages
type PornHubParser struct{}

// NewPornHubParser creates a new PornHub parser
func NewPornHubParser() *PornHubParser {
	return &PornHubParser{}
}

// GetName returns the parser name
func (p *PornHubParser) GetName() string {
	return "PornHub Parser"
}

// CanHandle checks if this parser can handle the given URL
func (p *PornHubParser) CanHandle(rawURL string) bool {
	isPornHub := strings.Contains(rawURL, "pornhub.com") || strings.Contains(rawURL, "pornhub.org")
	if !isPornHub {
		return false
	}
	// Accept viewkey= URLs and /embed/<id> URLs
	return strings.Contains(rawURL, "viewkey=") || strings.Contains(rawURL, "/embed/")
}

// NeedsHTML returns true — PornHub requires HTML scraping
func (p *PornHubParser) NeedsHTML() bool {
	return true
}

// FetchAndParse is not used for PornHub (NeedsHTML = true)
func (p *PornHubParser) FetchAndParse(url string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("PornHub parser requires HTML content, use Parse() instead")
}

// NormalizeURL extracts viewkey and normalizes URL
func (p *PornHubParser) NormalizeURL(rawURL string) (string, string) {
	// Extract viewkey from URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, ""
	}

	viewkey := parsedURL.Query().Get("viewkey")

	// Try /embed/<id> pattern
	if viewkey == "" {
		re := regexp.MustCompile(`/embed/([a-z0-9]+)`)
		matches := re.FindStringSubmatch(rawURL)
		if len(matches) > 1 {
			viewkey = matches[1]
		}
	}

	// Fallback: try viewkey= regex
	if viewkey == "" {
		re := regexp.MustCompile(`viewkey=([a-z0-9]+)`)
		matches := re.FindStringSubmatch(rawURL)
		if len(matches) > 1 {
			viewkey = matches[1]
		}
	}

	// Normalize to www.pornhub.com
	normalized := fmt.Sprintf("https://www.pornhub.com/view_video.php?viewkey=%s", viewkey)
	return normalized, viewkey
}

// Parse extracts data from PornHub HTML
func (p *PornHubParser) Parse(html string) (map[string]interface{}, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	result := make(map[string]interface{})

	// 1. Open Graph meta tags
	result["title"] = extractMeta(doc, "og:title")
	result["poster"] = extractMeta(doc, "og:image")
	result["sourceUrl"] = extractMeta(doc, "og:url")
	result["embedUrl"] = extractMetaByName(doc, "og:video:url")

	// Duration from <meta property="video:duration" content="2209" />
	durationStr := extractMeta(doc, "video:duration")
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

	// 2. Extract from dataLayer script (rich metadata)
	dataLayerBlock := extractDataLayerBlock(html)
	if dataLayerBlock != "" {
		// Pornstars
		pornstars := extractDataLayerField(dataLayerBlock, `'pornstars_in_video'\s*:\s*'([^']*)'`)
		if pornstars != "" {
			names := splitAndTrim(pornstars, ",")
			actors := make([]map[string]string, len(names))
			for i, name := range names {
				actors[i] = map[string]string{
					"id":    utils.RandomString(10, false),
					"value": name,
				}
			}
			result["pornstars"] = actors
		}

		// Categories
		categories := extractDataLayerField(dataLayerBlock, `'categories_in_video'\s*:\s*'([^']*)'`)
		if categories != "" {
			names := splitAndTrim(categories, ",")
			cats := make([]map[string]string, len(names))
			for i, name := range names {
				cats[i] = map[string]string{
					"id":    utils.RandomString(10, false),
					"value": name,
				}
			}
			result["categories"] = cats
		}

		// Uploader name
		uploader := extractDataLayerField(dataLayerBlock, `'video_uploader_name'\s*:\s*'([^']*)'`)
		if uploader != "" {
			result["uploader"] = uploader
		}

		// Production type
		production := extractDataLayerField(dataLayerBlock, `'video_production'\s*:\s*'([^']*)'`)
		if production != "" {
			result["production"] = production
		}

		// HD
		hd := extractDataLayerField(dataLayerBlock, `'hd_video'\s*:\s*'([^']*)'`)
		if hd != "" {
			result["isHD"] = hd == "Yes"
		}

		// Premium
		premium := extractDataLayerField(dataLayerBlock, `'premium_video'\s*:\s*'([^']*)'`)
		if premium != "" {
			result["isPremium"] = premium == "Yes"
		}
	}

	// 3. Tags from ads meta tag: data-context-tag attribute
	doc.Find("meta[name='adsbytrafficjunkycontext']").Each(func(i int, s *goquery.Selection) {
		if tagStr, exists := s.Attr("data-context-tag"); exists && tagStr != "" {
			names := splitAndTrim(tagStr, ",")
			tags := make([]map[string]string, len(names))
			for i, name := range names {
				tags[i] = map[string]string{
					"id":    utils.RandomString(10, false),
					"value": name,
				}
			}
			result["tags"] = tags
		}
	})

	// 4. Upload date from JSON-LD
	uploadDate := extractJSONLDField(html, `"uploadDate"\s*:\s*"([^"]+)"`)
	if uploadDate != "" {
		if len(uploadDate) >= 10 {
			result["releaseDate"] = uploadDate[:10]
		}
	}

	// 5. Views from JSON-LD (WatchAction)
	viewsStr := extractJSONLDField(html, `"interactionType"\s*:\s*"http://schema.org/WatchAction"[^}]*"userInteractionCount"\s*:\s*"([^"]+)"`)
	if viewsStr != "" {
		// Remove commas: "17,005" -> "17005"
		viewsStr = strings.ReplaceAll(viewsStr, ",", "")
		if views, err := strconv.ParseInt(viewsStr, 10, 64); err == nil {
			result["views"] = views
		}
	}

	// 6. Author from JSON-LD
	author := extractJSONLDField(html, `"author"\s*:\s*"([^"]+)"`)
	if author != "" {
		result["author"] = strings.TrimSpace(author)
	}

	// 7. Viewkey from canonical URL
	doc.Find("link[rel='canonical']").Each(func(i int, s *goquery.Selection) {
		if href, exists := s.Attr("href"); exists {
			if u, err := url.Parse(href); err == nil {
				if vk := u.Query().Get("viewkey"); vk != "" {
					result["viewkey"] = vk
				}
			}
		}
	})

	// 8. Extract m3u8/mp4 from flashvars_*/mediaDefinitions (only available in browser-rendered HTML)
	// Pattern: var flashvars_XXXXXX = { ... "mediaDefinitions": [...] ... }
	// or standalone mediaDefinitions array
	m3u8Re := regexp.MustCompile(`https?://[^"'\s]+\.m3u8[^"'\s]*`)
	m3u8Matches := m3u8Re.FindAllString(html, -1)
	if len(m3u8Matches) > 0 {
		// Prefer the highest quality m3u8 (usually the last one or "master.m3u8")
		bestM3u8 := ""
		for _, u := range m3u8Matches {
			if strings.Contains(u, "master.m3u8") {
				bestM3u8 = u
				break
			}
		}
		if bestM3u8 == "" {
			bestM3u8 = m3u8Matches[len(m3u8Matches)-1]
		}
		// Unescape any JSON escaping
		bestM3u8 = strings.ReplaceAll(bestM3u8, `\/`, `/`)
		result["m3u8Url"] = bestM3u8
	}

	// Also extract direct MP4 URLs from mediaDefinitions
	mp4Re := regexp.MustCompile(`https?://[^"'\s]+\.mp4[^"'\s]*`)
	mp4Matches := mp4Re.FindAllString(html, -1)
	if len(mp4Matches) > 0 {
		// Collect unique quality MP4s
		mp4Map := make(map[string]string)
		for _, u := range mp4Matches {
			u = strings.ReplaceAll(u, `\/`, `/`)
			// Identify quality from URL path (e.g. 720P_4000K, 480P_2000K)
			qualRe := regexp.MustCompile(`(\d+P)_\d+K`)
			qMatch := qualRe.FindStringSubmatch(u)
			if len(qMatch) > 1 {
				mp4Map[qMatch[1]] = u
			}
		}
		if len(mp4Map) > 0 {
			result["mp4Urls"] = mp4Map
		}
	}

	// 8. Extract m3u8/mp4 from flashvars_*/mediaDefinitions
	// Pattern: var flashvars_XXXXX = { ... "mediaDefinitions":[...] ... };
	// Extract all HLS m3u8 URLs with quality info
	hlsRe := regexp.MustCompile(`"format"\s*:\s*"hls"\s*,\s*"videoUrl"\s*:\s*"([^"]+)"\s*,\s*"quality"\s*:\s*"(\d+)"`)
	hlsMatches := hlsRe.FindAllStringSubmatch(html, -1)
	if len(hlsMatches) > 0 {
		hlsQualities := make(map[string]string)
		bestQuality := 0
		bestURL := ""
		for _, m := range hlsMatches {
			rawURL := strings.ReplaceAll(m[1], `\/`, `/`)
			quality := m[2]
			hlsQualities[quality+"p"] = rawURL
			if q, err := strconv.Atoi(quality); err == nil && q > bestQuality {
				bestQuality = q
				bestURL = rawURL
			}
		}
		if bestURL != "" {
			result["m3u8Url"] = bestURL
		}
		if len(hlsQualities) > 0 {
			result["hlsQualities"] = hlsQualities
		}
	}

	// Also try regex on the full flashvars block for any m3u8 we missed
	if _, ok := result["m3u8Url"]; !ok {
		m3u8Re := regexp.MustCompile(`https?://[^"'\s\\]+master\.m3u8[^"'\s\\]*`)
		m3u8Matches := m3u8Re.FindAllString(html, -1)
		if len(m3u8Matches) > 0 {
			result["m3u8Url"] = strings.ReplaceAll(m3u8Matches[0], `\/`, `/`)
		}
	}

	// Remove empty string values
	for k, v := range result {
		if str, ok := v.(string); ok && str == "" {
			delete(result, k)
		}
	}

	return result, nil
}

// extractMetaByName extracts content from meta property (alias for og: properties with "video:" prefix)
func extractMetaByName(doc *goquery.Document, property string) string {
	content := ""
	doc.Find("meta[property=\"" + property + "\"]").Each(func(i int, s *goquery.Selection) {
		if val, exists := s.Attr("content"); exists {
			content = val
		}
	})
	return content
}

// extractDataLayerBlock extracts the dataLayer.push block from HTML
func extractDataLayerBlock(html string) string {
	re := regexp.MustCompile(`window\.dataLayer\.push\(\{[^;]+?\}\);`)
	match := re.FindString(html)
	return match
}

// extractDataLayerField extracts a field from the dataLayer block
func extractDataLayerField(block string, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(block)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// splitAndTrim splits a string by separator and trims whitespace
func splitAndTrim(s string, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
