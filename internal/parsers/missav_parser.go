package parsers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"server-scraper/internal/utils"

	"github.com/PuerkitoBio/goquery"
)

// MissAVParser parses MissAV.com movie pages
type MissAVParser struct{}

// NewMissAVParser creates a new MissAV parser
func NewMissAVParser() *MissAVParser {
	return &MissAVParser{}
}

// GetName returns the parser name
func (p *MissAVParser) GetName() string {
	return "MissAV Parser"
}

// CanHandle checks if this parser can handle the given URL
func (p *MissAVParser) CanHandle(rawURL string) bool {
	domains := []string{
		"missav.ai", "missav.ws",
	}
	for _, d := range domains {
		if strings.Contains(rawURL, d) {
			return true
		}
	}
	return false
}

// NeedsHTML returns true — MissAV requires HTML scraping
func (p *MissAVParser) NeedsHTML() bool {
	return true
}

// FetchAndParse is not used for MissAV (NeedsHTML = true)
func (p *MissAVParser) FetchAndParse(url string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("MissAV parser requires HTML content, use Parse() instead")
}

// NormalizeURL forces the /en/ locale and extracts the slug from the URL.
func (p *MissAVParser) NormalizeURL(rawURL string) (string, string) {
	// Known locale codes used by MissAV
	locales := map[string]bool{
		"en": true, "cn": true, "ja": true, "ko": true, "ms": true, "th": true,
		"de": true, "fr": true, "vi": true, "id": true, "fil": true, "pt": true,
	}

	// Strip fragment (#...) and query string (?...)
	if idx := strings.Index(rawURL, "#"); idx != -1 {
		rawURL = rawURL[:idx]
	}
	if idx := strings.Index(rawURL, "?"); idx != -1 {
		rawURL = rawURL[:idx]
	}

	// Remove trailing slash
	rawURL = strings.TrimRight(rawURL, "/")

	// Split by "/"
	parts := strings.Split(rawURL, "/")
	// URL structure: https://missav.ai[/prefix][/locale]/slug
	// parts: ["https:", "", "missav.ai", ...]

	if len(parts) < 4 {
		return rawURL, ""
	}

	host := parts[2] // e.g. "missav.ai"
	remaining := parts[3:]

	// Filter out locale codes — the last remaining non-locale segment is the slug
	var nonLocaleSegments []string
	for _, seg := range remaining {
		if !locales[seg] {
			nonLocaleSegments = append(nonLocaleSegments, seg)
		}
	}

	slug := ""
	if len(nonLocaleSegments) > 0 {
		// Slug is the last non-locale segment
		slug = nonLocaleSegments[len(nonLocaleSegments)-1]
	}

	// Rebuild with /en/ locale
	normalized := "https://" + host + "/en"
	if slug != "" {
		normalized += "/" + slug
	}

	return normalized, slug
}

// Parse extracts data from MissAV HTML
func (p *MissAVParser) Parse(html string) (map[string]interface{}, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	result := make(map[string]interface{})

	// 1. Meta Tags (Open Graph)
	result["title_original"] = extractMeta(doc, "og:title")
	result["poster"] = extractMeta(doc, "og:image")
	result["releaseDate"] = extractMeta(doc, "og:video:release_date")
	result["sourceUrl"] = extractMeta(doc, "og:url")

	durationStr := extractMeta(doc, "og:video:duration")
	if durationStr != "" {
		if dur, err := strconv.Atoi(durationStr); err == nil {
			result["duration"] = dur
			// Also provide human-readable duration
			hours := dur / 3600
			minutes := (dur % 3600) / 60
			if hours > 0 {
				result["duration_text"] = fmt.Sprintf("%dh %dm", hours, minutes)
			} else {
				result["duration_text"] = fmt.Sprintf("%dm", minutes)
			}
		}
	}

	// 2. Clean title by removing code prefix
	if title, ok := result["title_original"].(string); ok && title != "" {
		code := extractCodeFromTitle(title)
		if code != "" {
			cleanTitle := strings.TrimSpace(strings.TrimPrefix(title, code))
			if cleanTitle != "" {
				result["title"] = cleanTitle
			}
		}
	}

	// 3. Description
	descEl := doc.Find("div.mb-1.text-secondary.break-all")
	if descEl.Length() > 0 {
		result["content"] = strings.TrimSpace(descEl.First().Text())
	}

	// 4. Parse div.text-secondary blocks for structured data
	var genreNames, tagNames, actressNames []string
	var makerName, directorName, seriesName, labelName string

	doc.Find("div.text-secondary").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())

		if strings.Contains(text, "Release date:") {
			dateText := strings.TrimSpace(s.Find("time").Text())
			if dateText != "" {
				result["releaseDate"] = dateText
			}
		} else if strings.Contains(text, "Genre:") {
			s.Find("a").Each(func(i int, a *goquery.Selection) {
				name := strings.TrimSpace(a.Text())
				if name != "" {
					genreNames = append(genreNames, name)
				}
			})
		} else if strings.Contains(text, "Series:") {
			seriesName = strings.TrimSpace(s.Find("a").Text())
		} else if strings.Contains(text, "Maker:") {
			makerName = strings.TrimSpace(s.Find("a").Text())
		} else if strings.Contains(text, "Label:") {
			labelName = strings.TrimSpace(s.Find("a").Text())
		} else if strings.Contains(text, "Tag:") {
			s.Find("a").Each(func(i int, a *goquery.Selection) {
				name := strings.TrimSpace(a.Text())
				if name != "" {
					tagNames = append(tagNames, name)
				}
			})
		} else if strings.Contains(text, "Director:") {
			directorName = strings.TrimSpace(s.Find("a").Text())
		} else if strings.Contains(text, "Actress:") {
			s.Find("a").Each(func(i int, a *goquery.Selection) {
				name := strings.TrimSpace(a.Text())
				if name != "" {
					actressNames = append(actressNames, name)
				}
			})
		}
	})

	if len(genreNames) > 0 {
		genres := make([]map[string]string, len(genreNames))
		for i, name := range genreNames {
			genres[i] = map[string]string{
				"id":    utils.RandomString(10, false),
				"value": name,
			}
		}
		result["genres"] = genres
	}
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
	if len(actressNames) > 0 {
		actresses := make([]map[string]string, len(actressNames))
		for i, name := range actressNames {
			actresses[i] = map[string]string{
				"id":    utils.RandomString(10, false),
				"value": name,
			}
		}
		result["actresses"] = actresses
	}
	if makerName != "" {
		result["makers"] = map[string]string{
			"id":    utils.RandomString(10, false),
			"value": makerName,
		}
	}
	if directorName != "" {
		result["directors"] = map[string]string{
			"id":    utils.RandomString(10, false),
			"value": directorName,
		}
	}
	if seriesName != "" {
		result["series"] = map[string]string{
			"id":    utils.RandomString(10, false),
			"value": seriesName,
		}
	}
	if labelName != "" {
		result["labels"] = map[string]string{
			"id":    utils.RandomString(10, false),
			"value": labelName,
		}
	}

	// 5. Extract m3u8 URL from packed JavaScript
	re := regexp.MustCompile(`eval\(function\(p,a,c,k,e,d\).*?\.split\('\|'\).*?\)`)
	evalBlock := re.FindString(html)
	if evalBlock != "" {
		unpacked := unpackJS(evalBlock)
		if unpacked != "" {
			urlRe := regexp.MustCompile(`'https?://[^']+\.m3u8'`)
			m3u8Match := urlRe.FindString(unpacked)
			if m3u8Match != "" {
				result["m3u8Url"] = strings.Trim(m3u8Match, "'")
			}
		}
	}

	// Derive trailer URL from poster by replacing cover-n.jpg → preview.mp4
	if poster, ok := result["poster"].(string); ok && poster != "" {
		coverRe := regexp.MustCompile(`cover[^/]*\.jpg`)
		result["trailer"] = coverRe.ReplaceAllString(poster, "preview.mp4")
	}

	// Remove empty string values
	for k, v := range result {
		if str, ok := v.(string); ok && str == "" {
			delete(result, k)
		}
	}
	delete(result, "title_original")

	return result, nil
}

// extractMeta extracts content from meta property tag
func extractMeta(doc *goquery.Document, property string) string {
	content := ""
	doc.Find("meta[property=\"" + property + "\"]").Each(func(i int, s *goquery.Selection) {
		if val, exists := s.Attr("content"); exists {
			content = val
		}
	})
	return content
}

// extractCodeFromTitle extracts DVD code from title (e.g., "GNS-146 Title..." -> "GNS-146")
func extractCodeFromTitle(title string) string {
	re := regexp.MustCompile(`^([A-Z0-9]+-[A-Z0-9]+(?:-[A-Z0-9]+)*)`)
	matches := re.FindStringSubmatch(strings.ToUpper(title))
	if len(matches) > 1 {
		code := matches[1]
		// Remove suffixes
		code = strings.ReplaceAll(code, "-UNCENSORED-LEAK", "")
		code = strings.ReplaceAll(code, "-CHINESE-SUBTITLE", "")
		code = strings.ReplaceAll(code, "-ENGLISH-SUBTITLE", "")
		return code
	}
	return ""
}

// unpackJS decodes packed JavaScript (Dean Edwards packer)
func unpackJS(packedScript string) string {
	argsRe := regexp.MustCompile(`\}\('(.+?)',(\d+),(\d+),'(.+?)'\.split\('\|'\)`)
	matches := argsRe.FindStringSubmatch(packedScript)
	if len(matches) < 5 {
		return ""
	}

	p := matches[1]
	p = strings.ReplaceAll(p, `\'`, `'`)
	p = strings.ReplaceAll(p, `\\`, `\`)

	c, _ := strconv.Atoi(matches[3])
	k := strings.Split(matches[4], "|")

	for i := c - 1; i >= 0; i-- {
		keyword := ""
		if i < len(k) {
			keyword = k[i]
		}
		if keyword == "" {
			keyword = strconv.FormatInt(int64(i), 36)
		}

		encoded := strconv.FormatInt(int64(i), 36)
		pattern := `\b` + regexp.QuoteMeta(encoded) + `\b`
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		p = re.ReplaceAllString(p, keyword)
	}

	return p
}
