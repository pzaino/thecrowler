package mail

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var emailBoilerplateClasses = map[string]struct{}{
	"gmail_quote":  {},
	"yahoo_quoted": {},
}

var emailBoilerplateIDs = map[string]struct{}{
	"divrplyfwdmsg":   {},
	"replyforwardmsg": {},
}

var preheaderMarkers = map[string]struct{}{
	"mcnpreviewtext": {},
	"preheader":      {},
	"preview-text":   {},
	"preview_text":   {},
}

// cleanupEmailHTML removes only non-content nodes and narrowly recognized
// email artifacts from a temporary DOM used for extraction. Callers retain the
// original source separately so cleanup never mutates the archived HTML body.
func cleanupEmailHTML(source string) (string, error) {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("mail: parse HTML for cleanup: %w", err)
	}

	cleanupEmailNode(document)

	var cleaned strings.Builder
	if err := html.Render(&cleaned, document); err != nil {
		return "", fmt.Errorf("mail: render cleaned HTML: %w", err)
	}
	return cleaned.String(), nil
}

func cleanupEmailNode(parent *html.Node) {
	for node := parent.FirstChild; node != nil; {
		next := node.NextSibling
		if shouldRemoveEmailNode(node) {
			parent.RemoveChild(node)
		} else {
			cleanupEmailNode(node)
		}
		node = next
	}
}

func shouldRemoveEmailNode(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}

	if strings.EqualFold(node.Data, "script") || isHiddenPreheader(node) || isEmailClientBoilerplate(node) {
		return true
	}
	if isTrackingPixel(node) {
		return true
	}
	return isTrackingPixelLink(node)
}

func isHiddenPreheader(node *html.Node) bool {
	if !hasMarker(node, preheaderMarkers) {
		return false
	}
	if hasBooleanAttribute(node, "hidden") || strings.EqualFold(strings.TrimSpace(attribute(node, "aria-hidden")), "true") {
		return true
	}

	style := inlineDeclarations(attribute(node, "style"))
	return style["display"] == "none" ||
		style["visibility"] == "hidden" ||
		zeroCSSLength(style["font-size"]) ||
		zeroCSSLength(style["line-height"]) ||
		zeroCSSLength(style["max-height"]) ||
		zeroCSSLength(style["max-width"]) ||
		style["opacity"] == "0"
}

func isEmailClientBoilerplate(node *html.Node) bool {
	if hasClassToken(node, emailBoilerplateClasses) {
		return true
	}
	_, recognized := emailBoilerplateIDs[strings.ToLower(strings.TrimSpace(attribute(node, "id")))]
	return recognized
}

func isTrackingPixelLink(node *html.Node) bool {
	if !strings.EqualFold(node.Data, "a") {
		return false
	}

	foundPixel := false
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if strings.TrimSpace(child.Data) != "" {
				return false
			}
		case html.CommentNode:
			continue
		case html.ElementNode:
			if !isTrackingPixel(child) {
				return false
			}
			foundPixel = true
		default:
			return false
		}
	}
	return foundPixel
}

func isTrackingPixel(node *html.Node) bool {
	if !strings.EqualFold(node.Data, "img") {
		return false
	}

	width, widthKnown := elementDimension(node, "width")
	height, heightKnown := elementDimension(node, "height")
	return widthKnown && heightKnown && width <= 1 && height <= 1
}

func elementDimension(node *html.Node, name string) (float64, bool) {
	if value := strings.TrimSpace(attribute(node, name)); value != "" {
		return parseCSSLength(value)
	}
	return parseCSSLength(inlineDeclarations(attribute(node, "style"))[name])
}

func parseCSSLength(value string) (float64, bool) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important")))
	value = strings.TrimSuffix(value, "px")
	if value == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return number, err == nil
}

func zeroCSSLength(value string) bool {
	number, ok := parseCSSLength(value)
	return ok && number == 0
}

func inlineDeclarations(style string) map[string]string {
	declarations := make(map[string]string)
	for declaration := range strings.SplitSeq(style, ";") {
		property, value, found := strings.Cut(declaration, ":")
		if !found {
			continue
		}
		property = strings.ToLower(strings.TrimSpace(property))
		value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important")))
		if property != "" {
			declarations[property] = value
		}
	}
	return declarations
}

func hasMarker(node *html.Node, markers map[string]struct{}) bool {
	if _, found := markers[strings.ToLower(strings.TrimSpace(attribute(node, "id")))]; found {
		return true
	}
	return hasClassToken(node, markers)
}

func hasClassToken(node *html.Node, markers map[string]struct{}) bool {
	for token := range strings.FieldsSeq(attribute(node, "class")) {
		if _, found := markers[strings.ToLower(token)]; found {
			return true
		}
	}
	return false
}

func hasBooleanAttribute(node *html.Node, name string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return true
		}
	}
	return false
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
}
