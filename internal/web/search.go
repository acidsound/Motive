package web

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Result struct { Title, URL, Snippet string }

var linkRE = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
var snippetRE = regexp.MustCompile(`(?s)<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
var tagRE = regexp.MustCompile(`<[^>]+>`)

func Search(query string) (string, error) {
	if strings.TrimSpace(query) == "" { return "", fmt.Errorf("query is required") }
	u := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil { return "", err }
	req.Header.Set("User-Agent", "Motive/0.1 (+https://github.com/acidsound/Motive)")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return "", fmt.Errorf("web search: %w", err) }
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil { return "", err }
	if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("search returned %s", resp.Status) }

	matches := linkRE.FindAllStringSubmatch(string(body), 8)
	var out strings.Builder
	for i, m := range matches {
		title := clean(m[2])
		link := html.UnescapeString(m[1])
		if strings.Contains(link, "uddg=") {
			if parsed, err := url.Parse(link); err == nil { if target := parsed.Query().Get("uddg"); target != "" { link = target } }
		}
		out.WriteString(fmt.Sprintf("%d. %s\n%s\n", i+1, title, link))
	}
	if out.Len() == 0 { return "No search results found.", nil }
	return strings.TrimSpace(out.String()), nil
}

func clean(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
