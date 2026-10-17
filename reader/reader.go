// Package reader reads bounded page text without building a whole-page DOM.
package reader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"searchan/internal/httpx"
)

const MaxText = 64 << 10
const MaxBody = 2 << 20

type Document struct {
	URL        string      `json:"url"`
	Title      string      `json:"title"`
	Text       string      `json:"text"`
	FetchedAt  time.Time   `json:"fetched_at"`
	Hash       string      `json:"hash"`
	Method     string      `json:"method"`
	Truncated  bool        `json:"truncated,omitempty"`
	Headings   []Heading   `json:"headings,omitempty"`
	CodeBlocks []CodeBlock `json:"code_blocks,omitempty"`
	Links      []Link      `json:"links,omitempty"` // bounded discovery hints, never evidence
}

// Heading locates visible h1–h6 text inside Document.Text by byte offset.
// Offsets are hints for passage boundaries, not statements of authority.
type Heading struct {
	Start int `json:"start"`
	End   int `json:"end"`
	Level int `json:"level"`
}

// CodeBlock locates visible preformatted text by byte offsets in Document.Text.
// It describes layout, never the language, correctness or authority of code.
type CodeBlock struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Link struct {
	URL  string `json:"url"`
	Text string `json:"text"`
}

type Reader interface {
	Read(context.Context, string) (Document, error)
}

type HTTP struct{ client *http.Client }

// NewHTTP uses direct public Internet connections. A supplied client is a
// trusted testing/transport override and must enforce its own destination policy.
func NewHTTP(client *http.Client) *HTTP {
	if client == nil {
		client = httpx.NewClient(10 * time.Second)
		tr := client.Transport.(*http.Transport)
		tr.Proxy = nil // A proxy could bypass IP validation; use an explicit trusted reader instead.
		tr.DialContext = publicDial
	}
	copy := *client
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many page redirects")
		}
		return ValidateURL(req.URL.String())
	}
	return &HTTP{&copy}
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("invalid page URL")
	}
	if p := u.Port(); p != "" && p != "443" && p != "80" {
		return errors.New("page port is not allowed")
	}
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil && !PublicIP(addr) {
		return errors.New("non-public page address")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("non-public page hostname")
	}
	return nil
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
}

func PublicIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	// DNS and every address attempt share one budget, as with net.Dialer.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return dialPublicIPs(ctx, network, port, ips, dialer.DialContext, 300*time.Millisecond)
}

func (r *HTTP) Read(ctx context.Context, raw string) (Document, error) {
	if err := ValidateURL(raw); err != nil {
		return Document{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return Document{}, err
	}
	req.Header.Set("User-Agent", "searchan/0.2 (+web research)")
	req.Header.Set("Accept", "text/html,text/plain;q=0.8")
	res, err := r.client.Do(req)
	if err != nil {
		return Document{}, httpx.TransportError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return Document{}, &httpx.StatusError{Code: res.StatusCode}
	}
	if res.ContentLength > MaxBody {
		return Document{}, errors.New("page exceeds 2 MiB")
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "application/xhtml+xml") {
		return Document{}, errors.New("unsupported page content type")
	}
	lr := &io.LimitedReader{R: res.Body, N: MaxBody + 1}
	var title, text string
	var truncated bool
	var links []Link
	var headings []Heading
	var codeBlocks []CodeBlock
	if strings.Contains(ct, "text/plain") {
		data, readErr := io.ReadAll(io.LimitReader(lr, MaxText+1))
		if readErr != nil {
			return Document{}, readErr
		}
		text = Clip(strings.TrimSpace(string(data)), MaxText)
		truncated = len(data) > MaxText
	} else {
		title, text, truncated, links, headings, codeBlocks, err = extractDocumentPage(lr, res.Request.URL.String())
		if err != nil {
			return Document{}, err
		}
	}
	if lr.N <= 0 {
		return Document{}, errors.New("page exceeds 2 MiB")
	}
	if err := CheckContent(title, text); err != nil {
		return Document{}, err
	}
	doc := MakeDocument(res.Request.URL.String(), title, text, "http")
	doc.Truncated = truncated
	doc.Links = links
	doc.Headings = headings
	doc.CodeBlocks = codeBlocks
	if utf8.RuneCountInString(text) < 40 {
		// A sparse project landing page may still link to its documentation.
		// Return metadata with the error; callers must not accept this as evidence.
		return doc, ContentError("insufficient_text")
	}
	return doc, nil
}

func MakeDocument(raw, title, text, method string) Document {
	truncated := len(text) > MaxText
	text = Clip(text, MaxText)
	hash := sha256.Sum256([]byte(text))
	return Document{URL: raw, Title: Clip(title, 512), Text: text, FetchedAt: time.Now().UTC(), Hash: hex.EncodeToString(hash[:]), Method: method, Truncated: truncated}
}

func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Jina is a separate Reader integration, not the Jina Search adapter.
type Jina struct {
	Endpoint, APIKey string
	Client           *http.Client
}

func (r *Jina) Read(ctx context.Context, raw string) (Document, error) {
	if err := ValidateURL(raw); err != nil {
		return Document{}, err
	}
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "https://r.jina.ai/"
	}
	client := r.Client
	if client == nil {
		client = httpx.NewClient(15 * time.Second)
	}
	headers := map[string]string{"X-Retain-Images": "none", "X-Max-Tokens": "12000"}
	if r.APIKey != "" {
		headers["Authorization"] = "Bearer " + r.APIKey
	}
	var wire struct {
		Code int
		Data struct{ Title, URL, Content string }
	}
	if err := httpx.JSON(ctx, client, "POST", endpoint, headers, map[string]string{"url": raw}, &wire, MaxBody); err != nil {
		return Document{}, err
	}
	if wire.Code != 0 && wire.Code != 200 {
		return Document{}, errors.New("Jina Reader application error")
	}
	if err := CheckContent(wire.Data.Title, wire.Data.Content); err != nil {
		return Document{}, err
	}
	if len(wire.Data.Content) < 40 {
		return Document{}, ContentError("insufficient_text")
	}
	if wire.Data.URL == "" {
		wire.Data.URL = raw
	}
	return MakeDocument(wire.Data.URL, wire.Data.Title, wire.Data.Content, "jina"), nil
}
