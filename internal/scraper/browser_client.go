package scraper

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

// BrowserResult holds the result from a browser fetch
type BrowserResult struct {
	Content string // Full HTML content
	Title   string // Page title
}

// findChrome finds the Chrome executable on the system
func findChrome() string {
	paths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA") + `\Google\Chrome\Application\chrome.exe`,
		`/usr/bin/google-chrome-stable`,
		`/usr/bin/google-chrome`,
		`/usr/bin/chromium-browser`,
		`/usr/bin/chromium`,
		`/snap/bin/chromium`,
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "" // Let rod download its own
}

// FetchHTMLWithBrowser uses headless Chrome via rod + stealth to fetch HTML.
func FetchHTMLWithBrowser(targetURL string, timeout time.Duration) (*BrowserResult, error) {
	log.Printf("🌐 Launching stealth browser for: %s", targetURL)

	// Find system Chrome to avoid downloading Chromium
	chromePath := findChrome()

	// Create launcher — disable leakless to avoid Windows antivirus false positive
	l := launcher.New().
		Leakless(false). // Disable leakless (causes antivirus issues on Windows)
		Headless(true).
		Set("disable-web-security").
		Set("disable-setuid-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-accelerated-2d-canvas").
		Set("disable-gpu").
		Set("no-sandbox")

	if chromePath != "" {
		log.Printf("🔍 Using system Chrome: %s", chromePath)
		l = l.Bin(chromePath)
	}

	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}

	browser := rod.New().ControlURL(u).MustConnect()
	defer browser.MustClose()

	// Use stealth mode — patches all common bot detection methods
	page := stealth.MustPage(browser)
	defer page.MustClose()

	// Set viewport to match the working Puppeteer service
	page.MustSetViewport(1366, 768, 1.0, false)

	// Set User-Agent
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	})

	// Navigate to URL
	log.Printf("📡 Navigating to: %s", targetURL)
	err = page.Timeout(timeout).Navigate(targetURL)
	if err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}

	// Wait for page to load
	err = page.Timeout(timeout).WaitStable(500 * time.Millisecond)
	if err != nil {
		log.Printf("⚠️ WaitStable timeout, continuing: %v", err)
	}

	// Check for Cloudflare challenge
	title := page.MustEval(`() => document.title`).String()
	log.Printf("📄 Page title: %s", title)

	if title == "Just a moment..." {
		log.Printf("⏳ Cloudflare challenge detected, waiting...")

		for i := 0; i < 30; i++ {
			time.Sleep(1 * time.Second)
			title = page.MustEval(`() => document.title`).String()
			log.Printf("⏳ Title check: %s (%ds)", title, i+1)

			if title != "Just a moment..." {
				break
			}
		}

		if title == "Just a moment..." {
			return nil, fmt.Errorf("cloudflare challenge did not resolve within 30s")
		}
	}

	log.Printf("✅ Page loaded: %s", title)

	// Get full HTML content
	html := page.MustHTML()

	log.Printf("📦 Browser fetched %d bytes from %s", len(html), targetURL)

	return &BrowserResult{
		Content: html,
		Title:   title,
	}, nil
}
