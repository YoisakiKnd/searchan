package reader

import (
	"strings"
	"testing"
)

func TestExtractPreservesInlineCodeAndWhitespace(t *testing.T) {
	raw := `<main><p>Use <code>context.<b>Done</b>()</code>, or <em>Err</em>TooLarge.</p><p>中文<strong>标识</strong>符。 A
	 B</p><pre>if ok {
    stop()
}</pre><table><tr><th>Limit</th><th>Value</th></tr><tr><td>Timeout</td><td>8s</td></tr></table></main>`
	_, text, err := Extract(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Use context.Done(), or ErrTooLarge.", "中文标识符。 A B", "if ok {\n    stop()\n}", "Limit\tValue", "Timeout\t8s"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

func TestExtractKeepsShortArticleHeaderAndSkipsHiddenText(t *testing.T) {
	raw := `<header>Global menu</header><div role="navigation">Links</div><p>Outside noise.</p><article><header><h1>Quota</h1></header><p>20 requests.</p><div hidden>Wrong 1000.</div><span aria-hidden="true">Hidden 500.</span><template>Template 300.</template></article>`
	_, text, err := Extract(strings.NewReader(raw))
	if err != nil || text != "Quota\n\n20 requests." {
		t.Fatalf("short primary content corrupted: %q %v", text, err)
	}
}

func TestExtractOptionalEndTagsAndNestedLists(t *testing.T) {
	for _, raw := range []string{
		"<main>" + strings.Repeat("<p>Short fact.", 600) + "</main>",
		"<main><table>" + strings.Repeat("<tr><td>name<td>value", 600) + "</table></main>",
		"<main><ul><li>Outer<ul><li>Inner<li>Another</ul><li>Next</ul></main>",
	} {
		_, text, err := Extract(strings.NewReader(raw))
		if err != nil || text == "" {
			t.Fatalf("valid optional end tags rejected: %v", err)
		}
	}
}

func TestExtractSeparatesNestedWidgetsFromProse(t *testing.T) {
	raw := `<main><h1>Page chrome</h1><article><div class="article-content"><h2>Repository guide</h2>
<p>正文讨论相关推荐组件：推荐算法不是数据库功能。</p>
<div class="article-recommend-card"><article><h2>Unrelated Solr preview</h2><div class="article-content">Wrong module status.</div></article></div>
<section class="comment-box"><p>Wrong commenter claim.</p></section>
<p class="recommendation">推荐阅读原文；这是作者的正文建议。</p>
<div class="recommendation-analysis">Normal analysis stays.</div>
<pre><code><span class="comment">// recommended posts are optional</span>
repo.FindByName("Ada")</code></pre>
<table><tr><th>Module</th><th>Use</th></tr><tr><td>JPA</td><td>Persistence</td></tr></table>
<p>Important final condition.</p></div></article><div class="recommend-box">Other preview.</div></main>`
	_, text, err := Extract(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Repository guide", "正文讨论相关推荐组件", "推荐阅读原文", "Normal analysis stays.", "// recommended posts are optional\nrepo.FindByName(\"Ada\")", "Module\tUse", "JPA\tPersistence", "Important final condition."} {
		if !strings.Contains(text, want) {
			t.Errorf("lost prose %q in %q", want, text)
		}
	}
	for _, noise := range []string{"Page chrome", "Unrelated Solr", "Wrong module", "Wrong commenter", "Other preview"} {
		if strings.Contains(text, noise) {
			t.Errorf("widget leaked: %q", text)
		}
	}
}

func TestExtractPrefersExplicitBodyWithoutRequiringOne(t *testing.T) {
	for _, container := range []string{`id="content_views"`, `class="blogpost-body cnblogs-markdown"`, `itemprop="articleBody"`, `class="markdown content"`} {
		raw := `<div>Global links</div><article>Earlier promo.</article><div ` + container + `><p>Short definition.</p></div><article>Later promo.</article>`
		_, text, err := Extract(strings.NewReader(raw))
		if err != nil || text != "Short definition." {
			t.Fatalf("explicit body %s: %q %v", container, text, err)
		}
	}
	for _, raw := range []string{
		`<main>Useful fallback.<div class="article-content" hidden>Hidden fake.</div></main>`,
		`<main>Useful fallback.<div class="article-content">  </div></main>`,
		`<p>Useful fallback.</p>`,
	} {
		_, text, err := Extract(strings.NewReader(raw))
		if err != nil || text != "Useful fallback." {
			t.Fatalf("fallback lost: %q %v", text, err)
		}
	}
}

func TestExtractLargeDiscardedAssetsRemainBounded(t *testing.T) {
	for _, tag := range []string{"style", "script"} {
		raw := `<head><title>Reference</title><` + tag + `>` + strings.Repeat("x", 340<<10) + `</` + tag + `></head><div class="markdown content">Short useful definition.</div>`
		title, text, err := Extract(strings.NewReader(raw))
		if err != nil || title != "Reference" || text != "Short useful definition." {
			t.Fatalf("asset discarded page: %s %q %q %v", tag, title, text, err)
		}
	}
	for _, raw := range []string{
		`<style>` + strings.Repeat("x", MaxBody+2) + `</style>`,
		`<style>small</style><p>` + strings.Repeat("x", 300<<10) + `</p>`,
		`<div title="` + strings.Repeat("x", 300<<10) + `">Short.</div>`,
	} {
		if _, _, err := Extract(strings.NewReader(raw)); err == nil {
			t.Fatal("token bound bypassed")
		}
	}
}

func TestExtractToleratesRepeatedOrUnclosedHead(t *testing.T) {
	for _, page := range []string{
		// Observed on a documentation mirror: a second <head> inside the first.
		`<html><head><title>Docs</title><head><meta charset="utf-8"></head><body><main><article><p>Spring Data MongoDB 支持模板 API。</p></article></main></body></html>`,
		`<html><head><title>Docs</title><body><main><p>Spring Data MongoDB 支持模板 API。</p></main></body></html>`,
		`<html><head><title>Docs</title></head><body><body><p>Spring Data MongoDB 支持模板 API。</p></body></html>`,
	} {
		title, text, err := Extract(strings.NewReader(page))
		if err != nil || title != "Docs" || text != "Spring Data MongoDB 支持模板 API。" {
			t.Fatalf("body lost: %q %q %v", title, text, err)
		}
	}
}
