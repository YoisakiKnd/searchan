// Package platform adapts outbound messages and authenticated inbound webhooks.
package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"searchan/internal/httpx"

	"searchan/render"
)

var defaultClient = httpx.NewClient(30 * time.Second)

type Target struct {
	Platform  string `json:"platform"`
	ChatID    string `json:"chat_id"`
	UserID    string `json:"user_id"`
	MessageID string `json:"message_id"`
	ThreadID  int64  `json:"thread_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
}
type Event struct {
	ID     string
	Target Target
	Text   string
}
type Receipt struct {
	MessageID string `json:"message_id"`
}
type Sender interface {
	SendText(context.Context, Target, string, int) (Receipt, error)
	SendImage(context.Context, Target, render.Artifact, string, int) (Receipt, error)
}

// Definite errors prove no message was accepted. Other errors are ambiguous and
// must not be automatically resent after a crash/timeout.
type RejectedError struct{ Code int }

func (e *RejectedError) Error() string { return fmt.Sprintf("platform rejected request (%d)", e.Code) }
func Definite(err error) bool {
	var e *RejectedError
	var rate *RateLimitError
	return errors.As(err, &e) || errors.As(err, &rate)
}

type Command struct{ Name, Argument string }

func ParseCommand(text string) (Command, bool) {
	text = strings.TrimSpace(text)
	// QQ @ markers precede command text in group events.
	for strings.HasPrefix(text, "<@") {
		i := strings.Index(text, ">")
		if i < 0 {
			return Command{}, false
		}
		text = strings.TrimSpace(text[i+1:])
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, false
	}
	name := strings.SplitN(fields[0], "@", 2)[0]
	switch name {
	case "/start", "/help", "/search", "/ask", "/deep", "/cancel", "/status", "/sources":
	default:
		return Command{}, false
	}
	argument := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	return Command{strings.TrimPrefix(name, "/"), argument}, true
}
