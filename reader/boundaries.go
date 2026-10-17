package reader

import "strings"

// Match whole structural tokens, never article text or arbitrary substrings.
// A code span named "comment" and prose about recommended articles are content.
func contentAttributes(tag, key, value string) (body, excluded bool) {
	if navigationAttribute(tag, key, value) {
		return false, true
	}
	if key == "itemprop" {
		for token := range strings.FieldsSeq(value) {
			if strings.EqualFold(token, "articleBody") {
				return true, false
			}
		}
		return false, false
	}
	switch tag {
	case "div", "section", "article", "main", "ul", "ol", "li":
	default:
		return false, false
	}
	markdown, content := false, false
	for token := range strings.FieldsSeq(value) {
		switch strings.ToLower(token) {
		case "article-body", "article-content", "article_content", "post-body", "post-content", "entry-content", "blogpost-body", "markdown-body":
			body = true
		case "content_views", "cnblogs_post_body":
			body = body || key == "id"
		case "markdown":
			markdown = true
		case "content":
			content = true
		case "related-posts", "related-articles", "recommended-posts", "recommend-box", "recommend-item-box", "article-recommend-card", "article-recommend-list", "comment-box", "comment-list", "article-comments", "post-comments":
			excluded = true
		case "toc", "table-of-contents":
			excluded = true // structural document navigation, not a prose section
		case "language-switcher", "language-selector", "language-menu", "mw-portlet-lang", "interlanguage-link", "interlanguage-link-target":
			excluded = true // alternate-language navigation is neither body nor source discovery
		}
	}
	return body || key == "class" && markdown && content, excluded
}

// MediaWiki emits navigation/category controls inside the article container.
// Exclude their exact structural markers, retaining infobox facts, references,
// prose about these names and inline/code elements that merely share a class.
func navigationAttribute(tag, key, value string) bool {
	if key != "id" && key != "class" {
		return false
	}
	if tag == "span" {
		for token := range strings.FieldsSeq(value) {
			if strings.EqualFold(token, "mw-editsection") {
				return true
			}
		}
		return false
	}
	switch tag {
	case "div", "section", "article", "main", "table", "ul", "ol", "li":
	default:
		return false
	}
	for token := range strings.FieldsSeq(value) {
		switch strings.ToLower(token) {
		case "navbox", "vertical-navbox", "catlinks", "mw-normal-catlinks", "mw-hidden-catlinks":
			return true
		case "footer", "footers", "mainfooter", "site-footer", "page-footer", "footer-content":
			return true // legacy sites often use divs instead of a footer landmark
		case "nav", "navigation", "navbar", "site-nav", "main-nav":
			return true // the same applies to explicit navigation containers
		}
	}
	return false
}
