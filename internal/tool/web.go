package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxFetchBytes = 5 << 20

type WebFetchTool struct {
	client *http.Client
}

func NewWebFetch() *WebFetchTool {
	return &WebFetchTool{client: &http.Client{Timeout: 30 * time.Second}}
}

func (t *WebFetchTool) Name() string { return "webfetch" }

func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return its content. HTML is converted to plain text."
}

func (t *WebFetchTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"url":    stringProp("URL to fetch"),
		"format": map[string]any{"type": "string", "enum": []string{"text", "markdown", "html"}, "description": "Output format (default markdown)"},
	}, "url")
}

type webFetchArgs struct {
	URL    string `json:"url"`
	Format string `json:"format"`
}

func (t *WebFetchTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args webFetchArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("webfetch: invalid arguments: %w", err)
	}
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return Result{}, fmt.Errorf("webfetch: url must start with http:// or https://")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("webfetch: %w", err)
	}
	req.Header.Set("User-Agent", "subcoder/1.0 (+https://github.com/arthurkushman/subcoder)")

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("webfetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return Result{}, fmt.Errorf("webfetch: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return Result{}, fmt.Errorf("webfetch: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	text := string(body)
	if strings.Contains(contentType, "text/html") && args.Format != "html" {
		text = htmlToText(text)
	}

	return Result{
		Title:  fmt.Sprintf("Fetch %s", args.URL),
		Output: text,
		Metadata: map[string]any{
			"url":         args.URL,
			"status":      resp.StatusCode,
			"contentType": contentType,
		},
	}, nil
}

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)[^>]*>.*?</\w+>`)
	blockTagRe    = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr|/h[1-6]|/table|/blockquote|/pre)[^>]*>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	spacesRe      = regexp.MustCompile(`[ \t]+`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(html string) string {
	html = scriptStyleRe.ReplaceAllString(html, "")
	html = blockTagRe.ReplaceAllString(html, "\n")
	html = tagRe.ReplaceAllString(html, "")
	html = strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&mdash;", "-",
		"&ndash;", "-",
	).Replace(html)
	html = strings.ReplaceAll(html, "\r\n", "\n")
	html = spacesRe.ReplaceAllString(html, " ")
	html = blankLinesRe.ReplaceAllString(html, "\n\n")
	return strings.TrimSpace(html)
}
