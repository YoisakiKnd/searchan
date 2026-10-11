package reader

import "strings"

// Match whole structural tokens, never article text or arbitrary substrings.
// A code span named "comment" and prose about recommended articles are content.
func contentAttributes(tag, key, value string) (body, excluded bool) {
	if key == "itemprop" {
		for token := range strings.FieldsSeq(value) {
			if strings.EqualFold(token, "articleBody") {
				return true, false
			}
		}
		return false, false
	}
	switch tag {
	case "div", "section", "article", "main", "ul", "ol":
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
		}
	}
	return body || key == "class" && markdown && content, excluded
}
