package platform

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type telegramEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// Entity positions use UTF-16 code units, not byte offsets. Only inspect the
// leading position; ordinary conversation mentions cannot initiate paid work.
func telegramInput(text, username string, entities []telegramEntity) (string, bool) {
	leading := strings.TrimLeftFunc(text, unicode.IsSpace)
	offset := 0
	for _, r := range text[:len(text)-len(leading)] {
		offset++
		if r > 0xffff {
			offset++
		}
	}
	mentioned := false
	for _, e := range entities {
		if e.Offset < 0 || e.Offset > offset || e.Length <= offset-e.Offset {
			continue
		}
		switch e.Type {
		case "code", "pre", "blockquote", "expandable_blockquote":
			return "", false // A quoted command/example is not a new request.
		case "mention":
			mentioned = mentioned || e.Offset == offset && e.Length == len(username)+1
		}
	}
	// Telegram usernames are ASCII. Validate before using their byte length as
	// the entity length; reject prefix matches to a different bot's username.
	if len(username) == 0 || len(username) > 32 {
		return text, true
	}
	for i := range len(username) {
		if !telegramUsernameByte(username[i]) {
			return text, true
		}
	}
	end := len(username) + 1
	if len(leading) < end || leading[0] != '@' || !strings.EqualFold(leading[1:end], username) || len(leading) > end && telegramUsernameByte(leading[end]) {
		return text, true
	}
	rest := leading[end:]
	if len(entities) == 0 {
		// Preserve the previous whitespace-delimited form for minimal updates.
		// Adjacent Chinese/punctuation requires an actual mention entity.
		r, _ := utf8.DecodeRuneInString(rest)
		mentioned = rest == "" || unicode.IsSpace(r)
	}
	if !mentioned {
		return text, true
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "/help", true
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/search " + rest
	}
	return rest, true
}

func telegramUsernameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
