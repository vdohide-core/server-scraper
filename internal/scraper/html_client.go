package scraper

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"

	"server-scraper/internal/config"
)

// HTMLClient handles HTML fetching with browser-like headers
type HTMLClient struct {
	httpClient *http.Client
	cookieJar  *cookiejar.Jar
}

// NewHTMLClient creates a new HTML client
func NewHTMLClient() *HTMLClient {
	jar, _ := cookiejar.New(nil)

	timeout := time.Duration(config.AppConfig.HTTPTimeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &HTMLClient{
		cookieJar: jar,
		httpClient: &http.Client{
			Timeout: timeout,
			Jar:     jar,
			Transport: &utlsRoundTripper{
				helloID: utls.HelloChrome_131,
			},
		},
	}
}

// utlsRoundTripper impersonates Chrome's TLS fingerprint (JA3/JA3S)
// so Cloudflare passes the request as if it came from a real browser.
type utlsRoundTripper struct {
	helloID utls.ClientHelloID
}

func (rt *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		// Non-TLS: fall back to plain transport
		return http.DefaultTransport.RoundTrip(req)
	}

	addr := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}

	tcpConn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, port), 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	tlsConn := utls.UClient(tcpConn, &utls.Config{
		ServerName: addr,
	}, rt.helloID)

	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}

	// Send HTTP/1.1 request over the uTLS connection
	if err := req.Write(tlsConn); err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("request write: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

// FetchHTML fetches HTML content from the given URL
func (c *HTMLClient) FetchHTML(targetURL string) (string, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Parse URL to extract domain for Referer
	parsedURL, _ := url.Parse(targetURL)
	referer := fmt.Sprintf("%s://%s/", parsedURL.Scheme, parsedURL.Host)

	// Set headers to mimic a real Chrome browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,th;q=0.8")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", referer)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("403 Forbidden: Access denied (anti-bot protection)")
		}
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Handle gzip encoding if server sends it
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Read response body
	body, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("📦 Fetched %d bytes from %s", len(body), targetURL)

	return string(body), nil
}

// FetchHTMLWithRetry fetches HTML with retry logic.
// If all retries fail with 403, it falls back to headless Chrome.
// After a successful browser bypass, CF clearance cookies are injected
// into this client's cookie jar so future HTTP requests skip the browser.
func (c *HTMLClient) FetchHTMLWithRetry(targetURL string, maxRetries int) (string, error) {
	var lastErr error
	got403 := false

	for i := 0; i < maxRetries; i++ {
		html, err := c.FetchHTML(targetURL)
		if err == nil {
			return html, nil
		}

		lastErr = err

		// Track if we got a 403 (anti-bot protection)
		if strings.Contains(err.Error(), "403 Forbidden") {
			got403 = true
			break // No point retrying with HTTP — need browser
		}

		if i < maxRetries-1 {
			waitTime := time.Duration(1<<uint(i)) * time.Second
			time.Sleep(waitTime)
		}
	}

	// Fallback to headless Chrome if we got 403
	if got403 {
		log.Printf("🔄 Got 403, falling back to headless Chrome for: %s", targetURL)
		browserTimeout := 60 * time.Second
		result, err := FetchHTMLWithBrowser(targetURL, browserTimeout)
		if err != nil {
			return "", fmt.Errorf("browser fallback also failed: %w (original: %v)", err, lastErr)
		}

		// Inject CF clearance cookies from browser into HTTP cookie jar
		// so future HTTP requests to the same domain skip the browser entirely
		if result.Cookies != nil && c.cookieJar != nil {
			parsedURL, parseErr := url.Parse(targetURL)
			if parseErr == nil {
				c.cookieJar.SetCookies(parsedURL, result.Cookies)
				log.Printf("🍪 Injected %d CF cookies into HTTP jar", len(result.Cookies))
			}
		}

		return result.Content, nil
	}

	return "", fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}
