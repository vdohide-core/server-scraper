package parsers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server-scraper/internal/db/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// GDriveFileInfo contains Google Drive file metadata from API v3
type GDriveFileInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     string `json:"size"`
	MimeType string `json:"mimeType"`
}

// GoogleDriveParser extracts file metadata from Google Drive using OAuth + API v3.
type GoogleDriveParser struct {
	oauthsCol *mongo.Collection
}

// NewGoogleDriveParser creates a new Google Drive parser with OAuth support
func NewGoogleDriveParser(oauthsCol *mongo.Collection) *GoogleDriveParser {
	return &GoogleDriveParser{
		oauthsCol: oauthsCol,
	}
}

// GetName returns the parser name
func (p *GoogleDriveParser) GetName() string {
	return "Google Drive Parser"
}

// CanHandle checks if this parser can handle the given URL
func (p *GoogleDriveParser) CanHandle(rawURL string) bool {
	return strings.Contains(rawURL, "drive.google.com") ||
		strings.Contains(rawURL, "docs.google.com")
}

// NeedsHTML returns false — Google Drive uses OAuth API calls
func (p *GoogleDriveParser) NeedsHTML() bool {
	return false
}

// Parse is not used for Google Drive (NeedsHTML = false)
func (p *GoogleDriveParser) Parse(html string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("Google Drive parser does not parse HTML, use FetchAndParse() instead")
}

// NormalizeURL extracts the file ID from a Google Drive URL.
func (p *GoogleDriveParser) NormalizeURL(rawURL string) (string, string) {
	fileID := extractGDriveFileID(rawURL)
	if fileID == "" {
		return rawURL, ""
	}

	// Normalize to a standard file URL
	normalized := fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)
	return normalized, fileID
}

// FetchAndParse fetches file metadata from Google Drive using OAuth + API v3
func (p *GoogleDriveParser) FetchAndParse(rawURL string) (map[string]interface{}, error) {
	_, fileID := p.NormalizeURL(rawURL)
	if fileID == "" {
		return nil, fmt.Errorf("could not extract file ID from URL: %s", rawURL)
	}

	result := make(map[string]interface{})
	result["fileId"] = fileID
	result["source"] = fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)

	// Get OAuth credentials from MongoDB
	oauth, err := getRandomOAuth(p.oauthsCol)
	if err != nil {
		log.Printf("⚠️ OAuth not available: %v — trying get_video_info only", err)
		return p.fetchVideoInfoOnly(fileID, result)
	}

	// Get fresh access token
	accessToken, err := refreshAccessToken(oauth, p.oauthsCol)
	if err != nil {
		log.Printf("⚠️ Token refresh failed: %v — trying get_video_info only", err)
		return p.fetchVideoInfoOnly(fileID, result)
	}

	// Step 1: Try get_video_info for quick video metadata
	videoTitle, isVideo := fetchVideoInfo(fileID, accessToken)
	if isVideo {
		log.Printf("🎬 get_video_info OK: %s", videoTitle)
	}

	// Step 2: Fetch full file metadata via Drive API v3
	fileInfo, err := getGDriveFileInfo(fileID, accessToken)
	if err != nil {
		log.Printf("⚠️ Drive API failed: %v — falling back to public access", err)
		// If get_video_info succeeded, still return what we have
		if isVideo && videoTitle != "" {
			result["title"] = videoTitle
			result["type"] = "video"
			result["downloadUrl"] = fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)
			result["authMethod"] = "oauth"
			result["accessible"] = true
			return result, nil
		}
		return p.fetchPublic(fileID, result)
	}

	// Populate result with API data
	downloadURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media&supportsAllDrives=true", fileID)
	// Prefer get_video_info title (more accurate for videos)
	if isVideo && videoTitle != "" {
		result["title"] = videoTitle
	} else {
		result["title"] = fileInfo.Name
	}
	result["mimeType"] = fileInfo.MimeType
	result["type"] = classifyMimeType(fileInfo.MimeType)
	result["downloadUrl"] = fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)
	result["authMethod"] = "oauth"

	if fileInfo.Size != "" {
		if size, parseErr := strconv.ParseInt(fileInfo.Size, 10, 64); parseErr == nil {
			result["size"] = size
			result["sizeText"] = formatFileSize(size)
		}
	}

	// Verify file is actually downloadable via HEAD request
	accessible := checkDownloadAccessible(downloadURL, accessToken)
	result["accessible"] = accessible

	log.Printf("📋 GDrive File: %s (%s, %s) [%s] accessible=%v", fileInfo.Name, fileInfo.Size, fileInfo.MimeType, result["type"], accessible)
	return result, nil
}

// fetchPublic falls back to public HEAD/GET when OAuth is unavailable
func (p *GoogleDriveParser) fetchPublic(fileID string, result map[string]interface{}) (map[string]interface{}, error) {
	result["authMethod"] = "public"
	downloadURL := fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)
	result["downloadUrl"] = downloadURL

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest("HEAD", downloadURL, nil)
	if err != nil {
		return result, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return result, nil
	}
	defer resp.Body.Close()

	// Extract filename from Content-Disposition
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if fn := extractFilenameFromContentDisposition(cd); fn != "" {
			result["title"] = fn
		}
	}

	// Extract content type
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		result["mimeType"] = ct
		result["type"] = classifyMimeType(ct)
	}

	// Extract content length
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if size, parseErr := strconv.ParseInt(cl, 10, 64); parseErr == nil {
			result["size"] = size
			result["sizeText"] = formatFileSize(size)
		}
	}

	result["accessible"] = resp.StatusCode == http.StatusOK
	return result, nil
}

// fetchVideoInfoOnly uses get_video_info without OAuth when no credentials are available
func (p *GoogleDriveParser) fetchVideoInfoOnly(fileID string, result map[string]interface{}) (map[string]interface{}, error) {
	result["authMethod"] = "public"
	result["downloadUrl"] = fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)

	videoTitle, isVideo := fetchVideoInfo(fileID, "")
	if !isVideo || videoTitle == "" {
		return nil, fmt.Errorf("get_video_info failed for file: %s (no OAuth available)", fileID)
	}

	log.Printf("🎬 get_video_info (no OAuth) OK: %s", videoTitle)
	result["title"] = videoTitle
	result["type"] = "video"
	result["accessible"] = true
	return result, nil
}

// ─── OAuth Helpers ──────────────────────────────────────────────────────────

// refreshAccessToken refreshes the OAuth access token using client credentials
func refreshAccessToken(oauth *models.OAuth, oauthsCol *mongo.Collection) (string, error) {
	// Check if current token is still valid (< 55 minutes elapsed)
	if oauth.Token != nil && oauth.TokenAt != nil {
		elapsed := time.Since(*oauth.TokenAt).Seconds()
		if elapsed < 3300 { // token valid for 55 minutes
			// Try to extract access token from token field
			if tokenMap, ok := oauth.Token.(map[string]interface{}); ok {
				if at, ok := tokenMap["access_token"].(string); ok && at != "" {
					return at, nil
				}
			}
		}
	}

	log.Printf("🔄 Refreshing Google OAuth token...")

	if oauth.ClientID == nil || oauth.ClientSecret == nil || oauth.RefreshToken == nil {
		return "", fmt.Errorf("OAuth credentials incomplete (clientId, clientSecret, or refreshToken missing)")
	}

	// Use oauth2/v4/token endpoint with JSON body
	refreshBody := map[string]string{
		"client_id":     *oauth.ClientID,
		"client_secret": *oauth.ClientSecret,
		"refresh_token": *oauth.RefreshToken,
		"grant_type":    "refresh_token",
	}
	jsonBody, err := json.Marshal(refreshBody)
	if err != nil {
		return "", fmt.Errorf("marshal refresh body: %w", err)
	}

	req, err := http.NewRequest("POST", "https://www.googleapis.com/oauth2/v4/token", strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		// Disable OAuth if refresh fails
		if oauthsCol != nil {
			oauthsCol.UpdateOne(context.Background(),
				bson.M{"_id": oauth.ID},
				bson.M{"$set": bson.M{"enable": false}},
			)
		}
		return "", fmt.Errorf("token refresh error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}

	// Update token in DB
	if oauthsCol != nil {
		now := time.Now()
		oauthsCol.UpdateOne(context.Background(),
			bson.M{"_id": oauth.ID},
			bson.M{"$set": bson.M{
				"enable":  true,
				"token":   bson.M{"access_token": tokenResp.AccessToken, "token_type": tokenResp.TokenType},
				"tokenAt": now,
			}},
		)
	}

	log.Printf("✅ Token refreshed successfully")
	return tokenResp.AccessToken, nil
}

// getRandomOAuth finds a random enabled OAuth record from the oauths collection.
// Prioritizes shared OAuth (no ownerId) first, then falls back to any enabled OAuth.
func getRandomOAuth(oauthsCol *mongo.Collection) (*models.OAuth, error) {
	if oauthsCol == nil {
		return nil, fmt.Errorf("oauths collection not configured")
	}

	ctx := context.Background()

	// Try shared OAuth first (no ownerId = shared/global)
	sharedPipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"enable": true,
			"$or": []bson.M{
				{"ownerId": bson.M{"$exists": false}},
				{"ownerId": nil},
				{"ownerId": ""},
			},
		}}},
		{{Key: "$sample", Value: bson.M{"size": 1}}},
	}

	cursor, err := oauthsCol.Aggregate(ctx, sharedPipeline)
	if err == nil {
		defer cursor.Close(ctx)
		if cursor.Next(ctx) {
			var oauth models.OAuth
			if err := cursor.Decode(&oauth); err == nil {
				log.Printf("🔑 Using shared OAuth: %s", oauth.Email)
				return &oauth, nil
			}
		}
	}

	// Fallback: any enabled OAuth
	anyPipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"enable": true}}},
		{{Key: "$sample", Value: bson.M{"size": 1}}},
	}

	cursor2, err := oauthsCol.Aggregate(ctx, anyPipeline)
	if err != nil {
		return nil, fmt.Errorf("query oauths: %w", err)
	}
	defer cursor2.Close(ctx)

	if !cursor2.Next(ctx) {
		return nil, fmt.Errorf("no enabled OAuth credentials found in oauths collection")
	}

	var oauth models.OAuth
	if err := cursor2.Decode(&oauth); err != nil {
		return nil, fmt.Errorf("decode oauth: %w", err)
	}

	ownerID := ""
	if oauth.OwnerID != nil {
		ownerID = *oauth.OwnerID
	}
	log.Printf("🔑 Using OAuth: %s (owner: %s)", oauth.Email, ownerID)
	return &oauth, nil
}

// checkDownloadAccessible verifies that a file is actually downloadable via HEAD request
func checkDownloadAccessible(downloadURL, accessToken string) bool {
	req, err := http.NewRequest("HEAD", downloadURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// fetchVideoInfo tries the get_video_info endpoint for quick video metadata.
// This is a hidden Google endpoint that returns video title without a full API call.
func fetchVideoInfo(fileID, accessToken string) (title string, ok bool) {
	infoURL := fmt.Sprintf("https://docs.google.com/get_video_info?docid=%s", fileID)

	req, err := http.NewRequest("GET", infoURL, nil)
	if err != nil {
		return "", false
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}

	// Response is query-string encoded: status=ok&title=...
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", false
	}

	if values.Get("status") == "ok" {
		return values.Get("title"), true
	}

	return "", false
}

// getGDriveFileInfo fetches file metadata from Google Drive API v3
func getGDriveFileInfo(fileID, accessToken string) (*GDriveFileInfo, error) {
	apiURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?fields=id,name,size,mimeType&supportsAllDrives=true", fileID)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("Google Drive file not found: %s", fileID)
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("access denied to Google Drive file: %s", fileID)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Drive API error %d: %s", resp.StatusCode, string(body))
	}

	var info GDriveFileInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ─── URL/File Helpers ───────────────────────────────────────────────────────

// extractGDriveFileID extracts the Google Drive file ID from various URL formats
func extractGDriveFileID(rawURL string) string {
	// Pattern 1: /file/d/{ID}/ or /folder/d/{ID}/ or /document/d/{ID}/
	re1 := regexp.MustCompile(`(?:file|folder|document|presentation)/d/([a-zA-Z0-9_-]{28,})`)
	if matches := re1.FindStringSubmatch(rawURL); len(matches) > 1 {
		return matches[1]
	}

	// Pattern 2: ?id={ID} or &id={ID}
	re2 := regexp.MustCompile(`[?&]id=([a-zA-Z0-9_-]{28,})`)
	if matches := re2.FindStringSubmatch(rawURL); len(matches) > 1 {
		return matches[1]
	}

	// Pattern 3: /spreadsheet/ccc?key={ID}
	re3 := regexp.MustCompile(`[?&]key=([a-zA-Z0-9_-]{28,})`)
	if matches := re3.FindStringSubmatch(rawURL); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// extractFilenameFromContentDisposition extracts filename from Content-Disposition header
func extractFilenameFromContentDisposition(header string) string {
	// Try filename*=UTF-8''encoded_name first
	re1 := regexp.MustCompile(`filename\*=(?:UTF-8|utf-8)''(.+?)(?:;|$)`)
	if matches := re1.FindStringSubmatch(header); len(matches) > 1 {
		return matches[1]
	}

	// Try filename="name"
	re2 := regexp.MustCompile(`filename="(.+?)"`)
	if matches := re2.FindStringSubmatch(header); len(matches) > 1 {
		return matches[1]
	}

	// Try filename=name (without quotes)
	re3 := regexp.MustCompile(`filename=([^\s;]+)`)
	if matches := re3.FindStringSubmatch(header); len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// formatFileSize converts bytes to human-readable format
func formatFileSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// classifyMimeType maps a MIME type to a simple category
func classifyMimeType(mimeType string) string {
	mt := strings.ToLower(mimeType)

	switch {
	case strings.HasPrefix(mt, "video/"):
		return "video"
	case strings.HasPrefix(mt, "image/"):
		return "image"
	case strings.HasPrefix(mt, "audio/"):
		return "audio"
	case mt == "application/vnd.google-apps.folder":
		return "folder"
	case strings.HasPrefix(mt, "application/vnd.google-apps."):
		return "document"
	case strings.Contains(mt, "pdf") ||
		strings.Contains(mt, "document") ||
		strings.Contains(mt, "spreadsheet") ||
		strings.Contains(mt, "presentation") ||
		strings.Contains(mt, "text/"):
		return "document"
	case strings.Contains(mt, "zip") ||
		strings.Contains(mt, "rar") ||
		strings.Contains(mt, "tar") ||
		strings.Contains(mt, "compressed") ||
		strings.Contains(mt, "archive"):
		return "archive"
	default:
		return "other"
	}
}
