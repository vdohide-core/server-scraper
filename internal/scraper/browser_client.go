package scraper

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

// BrowserResult holds the result from a browser fetch
type BrowserResult struct {
	Content string
	Title   string
	Cookies []*http.Cookie // CF clearance and session cookies for HTTP client reuse
}

// ─── Persistent Browser Pool ─────────────────────────────────────────────────

var (
	globalBrowser *rod.Browser
	browserMu     sync.Mutex
	browserOnce   sync.Once
)

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
	return ""
}

// launchBrowser starts a new headless Chrome instance
func launchBrowser() (*rod.Browser, error) {
	chromePath := findChrome()

	l := launcher.New().
		Leakless(false).
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
	return browser, nil
}

// InitBrowser launches the persistent headless browser at startup.
// Call once from main() to pre-warm Chromium.
func InitBrowser() error {
	var initErr error
	browserOnce.Do(func() {
		log.Println("🌐 Pre-warming headless Chrome...")
		b, err := launchBrowser()
		if err != nil {
			initErr = err
			return
		}
		globalBrowser = b
		log.Println("✅ Headless Chrome ready")
	})
	return initErr
}

// getBrowser returns the persistent browser, re-launching if it crashed
func getBrowser() (*rod.Browser, error) {
	browserMu.Lock()
	defer browserMu.Unlock()

	if globalBrowser != nil {
		// Quick health check
		if _, err := globalBrowser.Pages(); err == nil {
			return globalBrowser, nil
		}
		log.Println("⚠️ Browser crashed, restarting...")
		globalBrowser = nil
	}

	b, err := launchBrowser()
	if err != nil {
		return nil, err
	}
	globalBrowser = b
	return b, nil
}

// CloseBrowser closes the persistent browser. Call from main() on shutdown.
func CloseBrowser() {
	browserMu.Lock()
	defer browserMu.Unlock()
	if globalBrowser != nil {
		globalBrowser.MustClose()
		globalBrowser = nil
	}
}

// ─── Fetch ────────────────────────────────────────────────────────────────────

// FetchHTMLWithBrowser uses the persistent headless Chrome to fetch HTML.
// Reuses the browser instance — no cold start on subsequent requests.
func FetchHTMLWithBrowser(targetURL string, timeout time.Duration) (*BrowserResult, error) {
	log.Printf("🌐 Browser fetching: %s", targetURL)

	browser, err := getBrowser()
	if err != nil {
		return nil, fmt.Errorf("failed to get browser: %w", err)
	}

	// Open a new stealth page (tab) — fast, no browser restart
	page := stealth.MustPage(browser)
	defer page.MustClose()

	page.MustSetViewport(1366, 768, 1.0, false)
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	})

	log.Printf("📡 Navigating to: %s", targetURL)
	if err := page.Timeout(timeout).Navigate(targetURL); err != nil {
		return nil, fmt.Errorf("navigate failed: %w", err)
	}

	if err := page.Timeout(timeout).WaitStable(500 * time.Millisecond); err != nil {
		log.Printf("⚠️ WaitStable timeout, continuing: %v", err)
	}

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

	// Extract cookies (CF clearance etc.) for HTTP client reuse
	var httpCookies []*http.Cookie
	if cookies, err := page.Cookies([]string{targetURL}); err == nil {
		for _, c := range cookies {
			httpCookies = append(httpCookies, &http.Cookie{
				Name:  c.Name,
				Value: c.Value,
			})
		}
		log.Printf("🍪 Extracted %d cookies from browser", len(httpCookies))
	}

	html := page.MustHTML()
	log.Printf("📦 Browser fetched %d bytes from %s", len(html), targetURL)

	return &BrowserResult{Content: html, Title: title, Cookies: httpCookies}, nil
}
