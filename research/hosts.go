package research

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"searchan/internal/httpx"
	"searchan/reader"
)

// hostHealth remembers sites that refused automated reads (login walls,
// anti-bot challenges, rate limits) so later tasks spend their page budget on
// readable candidates. It never retries or works around the refusal.
type hostHealth struct {
	mu    sync.Mutex
	until map[string]time.Time
	ttl   time.Duration
	now   func() time.Time
}

const maxBlockedHosts = 1024

func newHostHealth() *hostHealth {
	return &hostHealth{until: map[string]time.Time{}, ttl: 30 * time.Minute, now: time.Now}
}

func (h *hostHealth) blocked(host string) bool {
	if h == nil || host == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	until, ok := h.until[host]
	if ok && !h.now().Before(until) {
		delete(h.until, host)
		return false
	}
	return ok
}

func (h *hostHealth) observe(host string, err error) {
	if h == nil || host == "" || !refusal(err) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if len(h.until) >= maxBlockedHosts {
		for k, until := range h.until {
			if !now.Before(until) {
				delete(h.until, k)
			}
		}
		if len(h.until) >= maxBlockedHosts {
			return
		}
	}
	h.until[host] = now.Add(h.ttl)
}

// refusal reports site-wide refusals. Read timeouts, 404s and transient 5xx
// are page- or moment-specific and do not block the host; a failed TCP connect
// or an invalid certificate affects every page of the site.
func refusal(err error) bool {
	var dial *net.OpError
	if errors.As(err, &dial) && dial.Op == "dial" {
		return true
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return true
	}
	var status *httpx.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case 401, 403, 429, 451, 521:
			return true
		}
		return false
	}
	var content reader.ContentError
	return errors.As(err, &content) && (content == "interstitial" || content == "script_challenge")
}
