package main

import (
	"fmt"
	"log"
	"net/http"

	"server-scraper/internal/config"
	"server-scraper/internal/db/database"
	"server-scraper/internal/handlers"
	"server-scraper/internal/parsers"

	"github.com/joho/godotenv"
)

func main() {
	log.Println("🚀 Starting Web Scraper API Server")

	// Load .env (optional)
	_ = godotenv.Load()

	// Load config
	config.Load()

	// Connect to MongoDB (optional — only needed for Google Drive OAuth)
	if err := database.Connect(); err != nil {
		log.Printf("⚠️ MongoDB connection failed: %v — Google Drive OAuth will be unavailable", err)
	} else {
		defer database.Disconnect()
	}

	// Initialize parser registry
	registry := parsers.NewRegistry()

	// Register parsers
	registry.Register(parsers.NewGoogleDriveParser(database.Oauths()))
	registry.Register(parsers.NewMissAVParser())
	registry.Register(parsers.NewXVideosParser())
	registry.Register(parsers.NewPornHubParser())
	registry.Register(parsers.NewDirectParser()) // catch-all: must be last

	// Get port from config
	port := config.AppConfig.Port
	if port == "" {
		port = "8081"
	}

	// Initialize handlers
	h := handlers.NewHandler(registry)

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.Health)
	mux.HandleFunc("/scraper", h.Scraper)
	mux.HandleFunc("/parsers", h.ListParsers)

	// Add CORS middleware
	handler := corsMiddleware(mux)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	fmt.Printf("🌐 Server listening on http://localhost:%s\n", port)
	fmt.Printf("📡 Scraper endpoint: http://localhost:%s/scraper?url=<URL>\n", port)
	fmt.Printf("📋 Parsers list: http://localhost:%s/parsers\n", port)

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("❌ Server error: %v", err)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
