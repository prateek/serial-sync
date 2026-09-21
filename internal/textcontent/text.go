package textcontent

import (
	"html"
	"strings"
	"unicode/utf8"

	htmlparser "golang.org/x/net/html"

	"github.com/prateek/serial-sync/internal/domain"
)

func Visible(release domain.NormalizedRelease) string {
	if strings.TrimSpace(release.TextHTML) != "" {
		return FromHTML(release.TextHTML)
	}
	return normalize(html.UnescapeString(release.TextPlain))
}

func Count(release domain.NormalizedRelease) int { return utf8.RuneCountInString(Visible(release)) }

func FromHTML(source string) string {
	tokenizer := htmlparser.NewTokenizer(strings.NewReader(source))
	var text strings.Builder
	hidden := 0
	for {
		kind := tokenizer.Next()
		if kind == htmlparser.ErrorToken {
			break
		}
		token := tokenizer.Token()
		switch kind {
		case htmlparser.StartTagToken:
			if token.Data == "script" || token.Data == "style" {
				hidden++
			}
			if block(token.Data) {
				text.WriteByte(' ')
			}
		case htmlparser.EndTagToken:
			if token.Data == "script" || token.Data == "style" {
				if hidden > 0 {
					hidden--
				}
			}
			if block(token.Data) {
				text.WriteByte(' ')
			}
		case htmlparser.SelfClosingTagToken:
			if block(token.Data) {
				text.WriteByte(' ')
			}
		case htmlparser.TextToken:
			if hidden == 0 {
				text.WriteString(token.Data)
			}
		}
	}
	return normalize(text.String())
}

func block(tag string) bool {
	switch tag {
	case "p", "div", "br", "li", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "blockquote", "hr", "tr", "td", "pre":
		return true
	}
	return false
}
func normalize(text string) string { return strings.Join(strings.Fields(text), " ") }
