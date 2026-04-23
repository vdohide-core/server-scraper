package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"server-scraper/internal/parsers"
	"server-scraper/internal/scraper"
)

type Handler struct {
	registry *parsers.ParserRegistry
}

func NewHandler(registry *parsers.ParserRegistry) *Handler {
	return &Handler{
		registry: registry,
	}
}

// Health returns a simple health check response
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "server-scraper",
	})
}

// Scraper handles URL scraping requests
// GET  /scraper?url=<URL>
// POST /scraper  {"url":"<URL>"}
func (h *Handler) Scraper(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var url string

	switch r.Method {
	case http.MethodGet:
		url = r.URL.Query().Get("url")
	case http.MethodPost:
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, "Invalid JSON body", http.StatusBadRequest)
			return
		}
		url = body.URL
	default:
		respondError(w, "Method not allowed. Use GET or POST", http.StatusMethodNotAllowed)
		return
	}

	if url == "" {
		respondError(w, "Missing 'url' parameter", http.StatusBadRequest)
		return
	}

	log.Printf("📥 Scraping: %s", url)

	// Find appropriate parser
	parser := h.registry.FindParser(url)
	if parser == nil {
		respondError(w, "No parser found for this URL. Supported: MissAV, XVideos, PornHub, Google Drive, Direct Video", http.StatusNotFound)
		return
	}

	log.Printf("🔍 Using parser: %s", parser.GetName())

	// Normalize URL
	normalizedURL, slug := parser.NormalizeURL(url)
	if slug != "" {
		log.Printf("🔗 Slug: %s", slug)
	}
	log.Printf("🌐 Normalized URL: %s", normalizedURL)

	var data map[string]interface{}
	var err error

	if parser.NeedsHTML() {
		// Parser needs HTML — fetch it first, then parse
		client := scraper.NewHTMLClient()
		html, fetchErr := client.FetchHTMLWithRetry(normalizedURL, 3)
		if fetchErr != nil {
			log.Printf("❌ Fetch error: %v", fetchErr)
			respondError(w, fmt.Sprintf("Failed to fetch HTML: %v", fetchErr), http.StatusInternalServerError)
			return
		}

		data, err = parser.Parse(html)
	} else {
		// Parser handles its own fetching (e.g., API calls, HEAD requests)
		data, err = parser.FetchAndParse(normalizedURL)
	}

	if err != nil {
		log.Printf("❌ Parse error: %v", err)
		respondError(w, fmt.Sprintf("Failed to parse: %v", err), http.StatusInternalServerError)
		return
	}

	// Include slug and code in parsed data if available
	if slug != "" {
		data["slug"] = slug
		data["code"] = strings.ToLower(slug)
	}

	// Return JSON response
	response := map[string]interface{}{
		"success":   true,
		"data":      data,
		"parser":    parser.GetName(),
		"url":       normalizedURL,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
	log.Printf("✅ Success: %s", normalizedURL)
}

// ListParsers returns all registered parsers
// GET /parsers
func (h *Handler) ListParsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	allParsers := h.registry.GetAllParsers()
	parserList := make([]map[string]interface{}, 0)

	for _, p := range allParsers {
		parserList = append(parserList, map[string]interface{}{
			"name":      p.GetName(),
			"needsHtml": p.NeedsHTML(),
		})
	}

	response := map[string]interface{}{
		"parsers": parserList,
		"count":   len(parserList),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func respondError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
