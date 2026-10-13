package reader

import (
	"strings"
	"testing"
)

func TestExtractDocumentTitleExcludesWidgetTitles(t *testing.T) {
	for _, test := range []struct{ name, raw, title string }{
		{"svg-after-head", `<html><head><title>Atlas reference</title></head><body><svg><title>Expand sidebar</title><text>Icon</text></svg><main>Useful definition.</main></body></html>`, "Atlas reference"},
		{"svg-without-head", `<svg><title>Expand sidebar</title></svg><main>Useful definition.</main>`, ""},
		{"hidden-before-title", `<nav><title>Navigation title</title></nav><title>Atlas reference</title><main>Useful definition.</main>`, "Atlas reference"},
		{"widget-with-fake-head", `<svg><foreignObject><head><title>Icon title</title></head></foreignObject></svg><title>Atlas reference</title><main>Useful definition.</main>`, "Atlas reference"},
		{"duplicate-title", `<head><title>Atlas reference</title><title>Other title</title></head><main>Useful definition.</main>`, "Atlas reference"},
		{"body-title", `<body><title>Widget title</title><main>Useful definition.</main></body>`, ""},
		{"fragment-title", `<title>Atlas &amp; reference</title><main>Useful definition.</main>`, "Atlas & reference"},
		{"implicit-head", `<html><title>Atlas reference</title><main>Useful definition.</main></html>`, "Atlas reference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			title, text, err := Extract(strings.NewReader(test.raw))
			if err != nil || title != test.title || text != "Useful definition." {
				t.Fatalf("widget contaminated metadata or body: title=%q text=%q err=%v", title, text, err)
			}
		})
	}
}
