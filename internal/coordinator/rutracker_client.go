package coordinator

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// RutrackerClient handles fetching magnet links from rutracker
type RutrackerClient struct {
	httpClient *http.Client
}

// NewRutrackerClient creates a new rutracker client
func NewRutrackerClient() *RutrackerClient {
	return &RutrackerClient{
		httpClient: &http.Client{},
	}
}

// GetMagnetLink extracts the magnet link from a rutracker topic page
func (c *RutrackerClient) GetMagnetLink(topicURL string) (string, error) {
	req, err := http.NewRequest("GET", topicURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch topic page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	magnetLink := findMagnetLink(string(body))
	if magnetLink != "" {
		return magnetLink, nil
	}

	return "", fmt.Errorf("magnet link not found on page")
}

// findMagnetLink searches for a magnet link in HTML content
func findMagnetLink(content string) string {
	// Parse HTML to find magnet links in href attributes
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return findMagnetLinkByRegex(content)
	}

	var magnetLink string
	var findMagnet func(*html.Node)
	findMagnet = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.HasPrefix(attr.Val, "magnet:?xt=urn:btih:") {
					magnetLink = attr.Val
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findMagnet(c)
			if magnetLink != "" {
				return
			}
		}
	}
	findMagnet(doc)

	if magnetLink != "" {
		return magnetLink
	}

	// Fallback to regex
	return findMagnetLinkByRegex(content)
}

// findMagnetLinkByRegex searches for a magnet link using regex
func findMagnetLinkByRegex(content string) string {
	re := regexp.MustCompile(`magnet:\?xt=urn:btih:[a-zA-Z0-9&=%.+-]+`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 0 {
		// Decode HTML entities
		link := strings.ReplaceAll(matches[0], "&amp;", "&")
		return link
	}
	return ""
}
