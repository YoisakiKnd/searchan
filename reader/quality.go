package reader

import (
	"strings"
	"unicode/utf8"
)

// ContentError is a stable diagnostic code, never an excerpt from a remote page.
type ContentError string

func (e ContentError) Error() string { return "unusable page content: " + string(e) }

// CheckContent rejects clear interstitials across HTTP, caches and supplied
// extracts. It does not certify relevance or truth, and does not penalize a
// concise definition merely for being short.
func CheckContent(title, text string) error {
	if !utf8.ValidString(text) {
		return ContentError("invalid_utf8")
	}
	if strings.TrimSpace(text) == "" {
		return ContentError("empty_text")
	}
	t := strings.ToLower(strings.TrimSpace(title))
	for _, marker := range []string{"just a moment", "access denied", "403 forbidden", "404 not found", "502 bad gateway", "503 service unavailable", "500 internal server error", "verify you are human", "attention required!", "captcha"} {
		if t == marker || strings.HasPrefix(t, marker+" |") || strings.HasPrefix(t, marker+" -") || strings.HasPrefix(t, marker+"…") || strings.HasPrefix(t, marker+"...") {
			return ContentError("interstitial")
		}
	}
	// Inspect a bounded prefix. Quoting an error later in a tutorial must not
	// make the tutorial itself an error page.
	lead := strings.ToLower(strings.Join(strings.Fields(Clip(text, 768)), " "))
	lead = strings.TrimLeft(lead, "# *\t\n")
	for _, marker := range []string{"verify you are human", "checking your browser", "please enable javascript to continue", "enable javascript and cookies to continue", "sign in to continue", "log in to continue", "please log in to view", "请先登录后查看", "登录后查看全文", "请完成安全验证"} {
		if strings.HasPrefix(lead, marker) {
			return ContentError("interstitial")
		}
	}
	first, _, _ := strings.Cut(strings.ToLower(Clip(strings.TrimSpace(text), 768)), "\n")
	first = strings.Trim(first, "# *\t\r")
	for _, marker := range []string{"403 forbidden", "404 not found", "502 bad gateway", "503 service unavailable", "500 internal server error"} {
		if first == marker || first == marker+" nginx" {
			return ContentError("interstitial")
		}
	}
	return nil
}
