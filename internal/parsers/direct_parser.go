package parsers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Supported video extensions for direct URLs
var videoExtensions = map[string]bool{
	"mp4": true, "mkv": true, "avi": true, "mov": true,
	"webm": true, "flv": true, "wmv": true, "ts": true,
	"m4v": true, "3gp": true, "mpg": true, "mpeg": true,
	"m3u8": true,
}

// DirectParser handles direct video URLs (mp4, m3u8, etc.)
type DirectParser struct {
	httpClient *http.Client
}

// NewDirectParser creates a new Direct URL parser
func NewDirectParser() *DirectParser {
	return &DirectParser{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// GetName returns the parser name
func (p *DirectParser) GetName() string {
	return "Direct URL Parser"
}

// CanHandle checks if the URL points to a direct video/m3u8 file
func (p *DirectParser) CanHandle(rawURL string) bool {
	ext := extractExtension(rawURL)
	return videoExtensions[ext]
}

// NeedsHTML returns false — direct URLs use HEAD/GET requests
func (p *DirectParser) NeedsHTML() bool {
	return false
}

// Parse is not used (NeedsHTML = false)
func (p *DirectParser) Parse(html string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("Direct parser does not parse HTML, use FetchAndParse() instead")
}

// NormalizeURL cleans the URL (removes fragments)
func (p *DirectParser) NormalizeURL(rawURL string) (string, string) {
	// Strip fragment
	if idx := strings.Index(rawURL, "#"); idx != -1 {
		rawURL = rawURL[:idx]
	}
	return rawURL, ""
}

// FetchAndParse validates the direct URL and extracts metadata
func (p *DirectParser) FetchAndParse(rawURL string) (map[string]interface{}, error) {
	ext := extractExtension(rawURL)
	if !videoExtensions[ext] {
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}

	result := make(map[string]interface{})
	result["source"] = rawURL
	result["downloadUrl"] = rawURL
	result["extension"] = ext

	// Extract filename from URL path
	parsedURL, err := url.Parse(rawURL)
	if err == nil {
		basename := path.Base(parsedURL.Path)
		if basename != "" && basename != "." && basename != "/" {
			result["title"] = basename
		}
	}

	if ext == "m3u8" {
		// M3U8: validate playlist by fetching content
		return p.validateM3U8(rawURL, result)
	}

	// Video file: validate via HEAD request
	return p.validateVideoFile(rawURL, result)
}

// validateVideoFile checks a direct video URL via HEAD request
func (p *DirectParser) validateVideoFile(rawURL string, result map[string]interface{}) (map[string]interface{}, error) {
	req, err := http.NewRequest("HEAD", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("connection failed: %v", err)
		return result, nil
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode != http.StatusOK {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result, nil
	}

	// Validate content type is video
	contentType := resp.Header.Get("Content-Type")
	result["mimeType"] = contentType
	result["type"] = classifyMimeType(contentType)

	if result["type"] != "video" && contentType != "" &&
		!strings.Contains(contentType, "octet-stream") &&
		!strings.Contains(contentType, "mpegurl") {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("not a video file (Content-Type: %s)", contentType)
		return result, nil
	}

	// If mimeType is generic, classify by extension
	if result["type"] != "video" {
		result["type"] = "video"
	}

	// Extract file size
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if size, parseErr := strconv.ParseInt(cl, 10, 64); parseErr == nil {
			result["size"] = size
			result["sizeText"] = formatFileSize(size)
		}
	}

	// Extract filename from Content-Disposition if available
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if fn := extractFilenameFromContentDisposition(cd); fn != "" {
			result["title"] = fn
		}
	}

	result["accessible"] = true
	log.Printf("📋 Direct Video: %s (%v, %s)", result["title"], result["sizeText"], contentType)
	return result, nil
}

// validateM3U8 fetches and validates an M3U8 playlist
func (p *DirectParser) validateM3U8(rawURL string, result map[string]interface{}) (map[string]interface{}, error) {
	result["type"] = "playlist"
	result["mimeType"] = "application/vnd.apple.mpegurl"

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("connection failed: %v", err)
		return result, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result, nil
	}

	// Read playlist content (limit to 1MB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		result["accessible"] = false
		result["error"] = fmt.Sprintf("read failed: %v", err)
		return result, nil
	}

	content := string(body)

	// Validate M3U8 format
	if !strings.Contains(content, "#EXTM3U") {
		result["accessible"] = false
		result["error"] = "invalid M3U8 playlist (missing #EXTM3U header)"
		return result, nil
	}

	// Determine playlist type
	isMaster := strings.Contains(content, "#EXT-X-STREAM-INF")
	isMedia := strings.Contains(content, "#EXTINF") || strings.Contains(content, "#EXT-X-TARGETDURATION")

	if isMaster {
		result["playlistType"] = "master"
		// Extract available resolutions
		resolutions := extractM3U8Resolutions(content)
		if len(resolutions) > 0 {
			result["resolutions"] = resolutions
		}
		// Count variants
		variantCount := strings.Count(content, "#EXT-X-STREAM-INF")
		result["variants"] = variantCount
	} else if isMedia {
		result["playlistType"] = "media"
		// Count segments
		segmentCount := strings.Count(content, "#EXTINF")
		result["segments"] = segmentCount

		// Extract total duration
		duration := extractM3U8Duration(content)
		if duration > 0 {
			result["duration"] = int64(duration)
		}
	} else {
		result["accessible"] = false
		result["error"] = "invalid M3U8 playlist (no stream or segment data)"
		return result, nil
	}

	result["accessible"] = true
	result["playlist"] = rawURL
	log.Printf("📋 Direct M3U8: %s (type: %s, accessible: true)", rawURL, result["playlistType"])
	return result, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// extractExtension extracts file extension from URL (ignoring query params)
func extractExtension(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(path.Ext(parsedURL.Path))
	if ext != "" {
		ext = ext[1:] // remove leading dot
	}
	return ext
}

// extractM3U8Resolutions extracts resolution info from master playlist
func extractM3U8Resolutions(content string) []string {
	re := regexp.MustCompile(`RESOLUTION=(\d+x\d+)`)
	matches := re.FindAllStringSubmatch(content, -1)

	seen := make(map[string]bool)
	var resolutions []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			resolutions = append(resolutions, m[1])
		}
	}
	return resolutions
}

// extractM3U8Duration calculates total duration from media playlist
func extractM3U8Duration(content string) float64 {
	re := regexp.MustCompile(`#EXTINF:([\d.]+)`)
	matches := re.FindAllStringSubmatch(content, -1)

	var total float64
	for _, m := range matches {
		if d, err := strconv.ParseFloat(m[1], 64); err == nil {
			total += d
		}
	}
	return total
}
