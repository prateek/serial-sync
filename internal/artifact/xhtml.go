package artifact

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

type xhtmlDocument struct {
	Content string
}

func buildXHTMLDocumentForEPUBVersionWithViewportAndFileName(title, bodyHTML, epubVersion, viewport, fileName string) (xhtmlDocument, error) {
	nodes, err := parseHTMLBodyNodes(bodyHTML)
	if err != nil {
		return xhtmlDocument{}, err
	}

	linkTargets := collectXHTMLLinkTargets(nodes, epubVersion)
	usedIDs := map[string]struct{}{}
	var body strings.Builder
	for _, node := range nodes {
		renderXHTMLNode(&body, node, epubVersion, fileName, "", false, linkTargets, usedIDs)
	}
	bodyContent := strings.TrimSpace(body.String())
	if bodyContent == "" {
		bodyContent = "<p></p>"
	}

	var document strings.Builder
	document.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	document.WriteByte('\n')
	document.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml"><head>`)
	if !strings.HasPrefix(strings.TrimSpace(epubVersion), "2") {
		document.WriteString(`<meta charset="utf-8" />`)
	}
	if strings.TrimSpace(viewport) != "" {
		document.WriteString(`<meta name="viewport" content="`)
		document.WriteString(escapeHTML(viewport))
		document.WriteString(`" />`)
	}
	document.WriteString(`<title>`)
	document.WriteString(escapeHTML(title))
	document.WriteString(`</title></head><body>`)
	document.WriteString(bodyContent)
	document.WriteString(`</body></html>`)
	return xhtmlDocument{Content: document.String()}, nil
}

func parseHTMLBodyNodes(input string) ([]*html.Node, error) {
	document, err := html.Parse(strings.NewReader(strings.TrimSpace(input)))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	body := findHTMLElement(document, "body")
	if body == nil {
		return nil, fmt.Errorf("parsed html document missing body")
	}
	var nodes []*html.Node
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		nodes = append(nodes, child)
	}
	return nodes, nil
}

func findHTMLElement(node *html.Node, name string) *html.Node {
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func collectXHTMLLinkTargets(nodes []*html.Node, epubVersion string) map[string]struct{} {
	targets := map[string]struct{}{}
	for _, node := range nodes {
		collectXHTMLLinkTargetIDs(targets, node, epubVersion)
	}
	return targets
}

func collectXHTMLLinkTargetIDs(targets map[string]struct{}, node *html.Node, epubVersion string) {
	if node.Type != html.ElementNode {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectXHTMLLinkTargetIDs(targets, child, epubVersion)
		}
		return
	}

	tag := strings.ToLower(strings.TrimSpace(node.Data))
	if tag == "" || droppedXHTMLElement(tag) {
		return
	}
	if tag == "html" || tag == "head" || tag == "body" || !validXMLName(tag) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectXHTMLLinkTargetIDs(targets, child, epubVersion)
		}
		return
	}
	if embeddedRemoteResourceTag(tag) {
		return
	}
	tag = epubCompatibleXHTMLTag(tag, epubVersion)
	if !allowedEPUBXHTMLTag(tag) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collectXHTMLLinkTargetIDs(targets, child, epubVersion)
		}
		return
	}
	for _, attr := range node.Attr {
		name, ok := xhtmlAttributeName(attr)
		if ok && name == "id" && safeXHTMLID(attr.Val) {
			targets[strings.TrimSpace(attr.Val)] = struct{}{}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectXHTMLLinkTargetIDs(targets, child, epubVersion)
	}
}

func renderXHTMLNode(builder *strings.Builder, node *html.Node, epubVersion, fileName, parentTag string, inAnchor bool, linkTargets, usedIDs map[string]struct{}) {
	switch node.Type {
	case html.TextNode:
		if wrapper := xhtmlRequiredParentChildWrapper(parentTag, ""); wrapper != "" && strings.TrimSpace(node.Data) != "" {
			builder.WriteByte('<')
			builder.WriteString(wrapper)
			builder.WriteByte('>')
			builder.WriteString(escapeHTML(node.Data))
			builder.WriteString("</")
			builder.WriteString(wrapper)
			builder.WriteByte('>')
			return
		}
		builder.WriteString(escapeHTML(node.Data))
	case html.ElementNode:
		renderXHTMLElement(builder, node, epubVersion, fileName, parentTag, inAnchor, linkTargets, usedIDs)
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderXHTMLNode(builder, child, epubVersion, fileName, parentTag, inAnchor, linkTargets, usedIDs)
		}
	}
}

func renderXHTMLElement(builder *strings.Builder, node *html.Node, epubVersion, fileName, parentTag string, inAnchor bool, linkTargets, usedIDs map[string]struct{}) {
	tag := strings.ToLower(strings.TrimSpace(node.Data))
	if tag == "" || droppedXHTMLElement(tag) {
		return
	}
	if tag == "html" || tag == "head" || tag == "body" {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderXHTMLNode(builder, child, epubVersion, fileName, parentTag, inAnchor, linkTargets, usedIDs)
		}
		return
	}
	if !validXMLName(tag) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderXHTMLNode(builder, child, epubVersion, fileName, parentTag, inAnchor, linkTargets, usedIDs)
		}
		return
	}
	tag = epubCompatibleXHTMLTag(tag, epubVersion)
	tag = contentCompatibleXHTMLTag(tag, parentTag)
	if wrapper := xhtmlRequiredParentChildWrapper(parentTag, tag); wrapper != "" {
		builder.WriteByte('<')
		builder.WriteString(wrapper)
		builder.WriteByte('>')
		renderXHTMLElement(builder, node, epubVersion, fileName, wrapper, inAnchor, linkTargets, usedIDs)
		builder.WriteString("</")
		builder.WriteString(wrapper)
		builder.WriteByte('>')
		return
	}
	if embeddedRemoteResourceTag(tag) {
		renderEmbeddedRemoteResourceLink(builder, node, inAnchor)
		return
	}
	if !allowedEPUBXHTMLTag(tag) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderXHTMLNode(builder, child, epubVersion, fileName, parentTag, inAnchor, linkTargets, usedIDs)
		}
		return
	}
	childInAnchor := inAnchor || tag == "a"

	builder.WriteByte('<')
	builder.WriteString(tag)
	for _, attr := range node.Attr {
		name, ok := xhtmlAttributeName(attr)
		if !ok || !allowedXHTMLAttribute(tag, name, attr.Val, epubVersion, fileName, linkTargets) {
			continue
		}
		if name == "id" {
			id := strings.TrimSpace(attr.Val)
			if !safeXHTMLID(id) {
				continue
			}
			if _, exists := usedIDs[id]; exists {
				continue
			}
			usedIDs[id] = struct{}{}
		}
		builder.WriteByte(' ')
		builder.WriteString(name)
		builder.WriteString(`="`)
		builder.WriteString(escapeHTML(attr.Val))
		builder.WriteByte('"')
	}
	if emptyXHTMLElement(tag) && node.FirstChild == nil {
		builder.WriteString(" />")
		return
	}
	builder.WriteByte('>')
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		renderXHTMLNode(builder, child, epubVersion, fileName, tag, childInAnchor, linkTargets, usedIDs)
	}
	builder.WriteString("</")
	builder.WriteString(tag)
	builder.WriteByte('>')
}

func xhtmlRequiredParentChildWrapper(parentTag, childTag string) string {
	switch parentTag {
	case "ol", "ul":
		if childTag != "li" {
			return "li"
		}
	case "dl":
		if childTag != "dd" && childTag != "dt" {
			return "dd"
		}
	}
	return ""
}

func epubCompatibleXHTMLTag(tag, epubVersion string) string {
	if !strings.HasPrefix(strings.TrimSpace(epubVersion), "2") {
		return tag
	}
	switch tag {
	case "article", "aside", "details", "figcaption", "figure", "footer", "header", "hgroup", "main", "nav", "section", "summary":
		return "div"
	case "bdi", "data", "mark", "time":
		return "span"
	case "wbr":
		return "br"
	default:
		return tag
	}
}

func contentCompatibleXHTMLTag(tag, parentTag string) string {
	switch tag {
	case "li":
		if parentTag == "ol" || parentTag == "ul" {
			return tag
		}
		return "div"
	case "dd", "dt":
		if parentTag == "dl" {
			return tag
		}
		return "div"
	default:
		return tag
	}
}

func allowedEPUBXHTMLTag(tag string) bool {
	switch tag {
	case "a", "abbr", "address", "article", "aside", "b", "bdi", "bdo", "blockquote", "br", "caption", "cite", "code", "col", "colgroup", "data", "dd", "del", "details", "dfn", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "ins", "kbd", "li", "main", "mark", "ol", "p", "pre", "q", "rp", "rt", "ruby", "s", "samp", "section", "small", "span", "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th", "thead", "time", "tr", "u", "ul", "var", "wbr":
		return true
	default:
		return false
	}
}

func droppedXHTMLElement(tag string) bool {
	switch tag {
	case "script", "style", "iframe", "object", "embed", "picture", "source", "canvas", "form", "input", "button", "textarea", "select", "option", "link", "meta":
		return true
	default:
		return false
	}
}

func emptyXHTMLElement(tag string) bool {
	switch tag {
	case "base", "br", "col", "hr", "img", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func xhtmlAttributeName(attr html.Attribute) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(attr.Key))
	if key == "" || key == "xmlns" || strings.HasPrefix(key, "xmlns:") {
		return "", false
	}
	if attr.Namespace == "xml" {
		key = "xml:" + key
	}
	if strings.Contains(key, ":") && !strings.HasPrefix(key, "xml:") {
		return "", false
	}
	if !validXMLName(key) {
		return "", false
	}
	return key, true
}

func allowedXHTMLAttribute(tag, name, value, epubVersion, fileName string, linkTargets map[string]struct{}) bool {
	if strings.HasPrefix(name, "on") {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(epubVersion), "2") && !allowedEPUB2XHTMLAttribute(tag, name) {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(epubVersion), "2") && !allowedEPUB3XHTMLAttribute(tag, name) {
		return false
	}
	switch name {
	case "href":
		return tag == "a" && safeEPUBURL(value, fileName, linkTargets)
	case "src":
		return embeddedRemoteResourceTag(tag) && safeRemoteResourceURL(value)
	case "srcset":
		return false
	default:
		return true
	}
}

func allowedEPUB3XHTMLAttribute(tag, name string) bool {
	switch name {
	case "id", "class", "title", "lang", "xml:lang", "dir", "role", "hidden":
		return true
	case "href":
		return tag == "a"
	case "colspan", "rowspan":
		return tag == "td" || tag == "th"
	case "scope":
		return tag == "th"
	case "datetime":
		return tag == "del" || tag == "ins" || tag == "time"
	default:
		return strings.HasPrefix(name, "aria-")
	}
}

func allowedEPUB2XHTMLAttribute(tag, name string) bool {
	switch name {
	case "id", "class", "title", "xml:lang":
		return true
	case "href":
		return tag == "a"
	case "colspan", "rowspan":
		return tag == "td" || tag == "th"
	case "scope":
		return tag == "th"
	case "datetime":
		return tag == "del" || tag == "ins"
	default:
		return false
	}
}

func embeddedRemoteResourceTag(tag string) bool {
	switch tag {
	case "audio", "img", "video":
		return true
	default:
		return false
	}
}

func renderEmbeddedRemoteResourceLink(builder *strings.Builder, node *html.Node, inAnchor bool) {
	src, ok := remoteSrc(node)
	if !ok {
		return
	}
	label := firstNonEmptyString(
		htmlAttributeValue(node, "alt"),
		htmlAttributeValue(node, "title"),
		htmlAttributeValue(node, "aria-label"),
		src,
	)
	if inAnchor {
		builder.WriteString(escapeHTML(label))
		return
	}
	builder.WriteString(`<a href="`)
	builder.WriteString(escapeHTML(src))
	builder.WriteString(`">`)
	builder.WriteString(escapeHTML(label))
	builder.WriteString(`</a>`)
}

func remoteSrc(node *html.Node) (string, bool) {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, "src") && safeRemoteResourceURL(attr.Val) {
			return strings.TrimSpace(attr.Val), true
		}
	}
	return "", false
}

func htmlAttributeValue(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func safeEPUBURL(value, fileName string, linkTargets map[string]struct{}) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		return safeRelativeEPUBURL(parsed, fileName, linkTargets)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto", "tel":
		return true
	default:
		return false
	}
}

func safeRelativeEPUBURL(parsed *url.URL, fileName string, linkTargets map[string]struct{}) bool {
	if parsed == nil || parsed.Host != "" || parsed.Opaque != "" {
		return false
	}
	if parsed.Fragment != "" {
		if _, ok := linkTargets[parsed.Fragment]; !ok {
			return false
		}
	}
	if parsed.Path == "" {
		return parsed.Fragment != ""
	}
	if strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "../") || strings.Contains(parsed.Path, "/../") {
		return false
	}
	if strings.TrimSpace(fileName) == "" {
		return false
	}
	return path.Clean(parsed.Path) == path.Clean(fileName)
}

func safeRemoteResourceURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}

func safeXHTMLID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && validXMLName(value)
}

func validXMLName(value string) bool {
	for idx, r := range value {
		if idx == 0 {
			if r == '_' || r == ':' || unicode.IsLetter(r) {
				continue
			}
			return false
		}
		if r == '_' || r == ':' || r == '-' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return value != ""
}
