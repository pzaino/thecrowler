package scraper

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// HTMLNode represents the stable JSON shape produced from HTML.
type HTMLNode struct {
	Tag        string            `json:"tag,omitempty"`
	Text       string            `json:"text,omitempty"`
	URL        string            `json:"url,omitempty"`
	Comment    string            `json:"comment,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Children   []HTMLNode        `json:"children,omitempty"`
}

// HTMLConversionRequest describes HTML text conversion.
type HTMLConversionRequest struct{ HTML string }

// HTMLConversionResult contains both the parsed document and extracted data.
type HTMLConversionResult struct {
	Document *html.Node
	Data     HTMLNode
}

// ConvertHTML parses HTML and converts it to the scraper's stable data shape.
func ConvertHTML(req HTMLConversionRequest) (HTMLConversionResult, error) {
	doc, err := ParseHTML(req.HTML)
	if err != nil {
		return HTMLConversionResult{}, err
	}
	return HTMLConversionResult{Document: doc, Data: ExtractHTMLData(doc)}, nil
}

// ParseHTML parses text into an HTML document.
func ParseHTML(text string) (*html.Node, error) {
	doc, err := html.Parse(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}
	return doc, nil
}

// ExtractHTMLData extracts relevant data from an HTML node.
func ExtractHTMLData(n *html.Node) HTMLNode {
	var node HTMLNode
	if n == nil {
		return node
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		if isSkippedTag(tag) {
			return HTMLNode{}
		}
		node.Tag = n.Data
		node.Attributes = make(map[string]string)
		isParentSpan := n.Parent != nil && strings.EqualFold(n.Parent.Data, "span")
		var childText strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.TextNode {
				continue
			}
			text := strings.TrimSpace(child.Data)
			if text == "" {
				continue
			}
			if isParentSpan {
				childText.WriteByte(' ')
				childText.WriteString(text)
			} else if node.Text == "" {
				node.Text = text
			} else {
				node.Text += " " + text
			}
		}
		if isParentSpan && childText.Len() > 0 {
			return HTMLNode{Text: childText.String()}
		}
		if text := strings.TrimSpace(childText.String()); text != "" {
			if node.Text != "" {
				node.Text += " "
			}
			node.Text += text
		}
		for _, attr := range n.Attr {
			key := strings.ToLower(attr.Key)
			if key == "href" || key == "src" || key == "action" {
				if ignoredURL(attr.Val) {
					return HTMLNode{}
				}
				node.URL = attr.Val
			} else if !ignoredAttribute(key) {
				node.Attributes[key] = attr.Val
			}
		}
	} else if n.Type == html.CommentNode {
		node.Comment = strings.TrimSpace(n.Data)
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		extracted := ExtractHTMLData(child)
		if strings.EqualFold(n.Data, "span") && strings.EqualFold(extracted.Tag, "span") {
			if extracted.Text != "" {
				node.Text += " " + extracted.Text
			}
		} else if extracted.Tag != "" || extracted.Text != "" || extracted.URL != "" || extracted.Comment != "" || len(extracted.Attributes) > 0 {
			node.Children = append(node.Children, extracted)
		}
	}
	return node
}

func isSkippedTag(tag string) bool {
	switch tag {
	case "script", "noscript", "iframe", "svg", "img", "base", "input", "button", "select", "option", "textarea", "form", "style":
		return true
	default:
		return false
	}
}

func ignoredAttribute(key string) bool {
	ignoredAttributes := map[string]bool{
		"style": true,
		//"class": true, // Only enable for testing purposes or reducing noise
		//"id":    true, // Only enable for testing purposes or reducing noise
		"onabort":              true,
		"onafterprint":         true,
		"onanimationend":       true,
		"onanimationiteration": true,
		"onanimationstart":     true,
		"onauxclick":           true,
		"onbeforecopy":         true,
		"onbeforecut":          true,
		"onbeforepaste":        true,
		"onbeforeprint":        true,
		"onbeforeunload":       true,
		"onblur":               true,
		"oncanplay":            true,
		"oncanplaythrough":     true,
		"onchange":             true,
		"onclick":              true,
		"onclose":              true,
		"oncontextmenu":        true,
		"oncopy":               true,
		"oncuechange":          true,
		"oncut":                true,
		"ondblclick":           true,
		"ondrag":               true,
		"ondragend":            true,
		"ondragenter":          true,
		"ondragleave":          true,
		"ondragover":           true,
		"ondragstart":          true,
		"ondrop":               true,
		"ondurationchange":     true,
		"onemptied":            true,
		"onended":              true,
		"onerror":              true,
		"onfocus":              true,
		"onformdata":           true,
		"onfullscreenchange":   true,
		"onfullscreenerror":    true,
		"ongotpointercapture":  true,
		"oninput":              true,
		"oninvalid":            true,
		"onkeydown":            true,
		"onkeypress":           true,
		"onkeyup":              true,
		"onload":               true,
		"onloadeddata":         true,
		"onloadedmetadata":     true,
		"onloadstart":          true,
		"onlostpointercapture": true,
		"onmousedown":          true,
		"onmouseenter":         true,
		"onmouseleave":         true,
		"onmousemove":          true,
		"onmouseout":           true,
		"onmouseover":          true,
		"onmouseup":            true,
		"onmousewheel":         true,
		"onpaste":              true,
		"onpause":              true,
		"onplay":               true,
		"onplaying":            true,
		"onpointercancel":      true,
		"onpointerdown":        true,
		"onpointerenter":       true,
		"onpointerleave":       true,
		"onpointermove":        true,
		"onpointerout":         true,
		"onpointerover":        true,
		"onpointerup":          true,
		"onprogress":           true,
		"onratechange":         true,
		"onreset":              true,
		"onresize":             true,
		"onscroll":             true,
		"onsearch":             true,
		"onseeked":             true,
		"onseeking":            true,
		"onselect":             true,
		"onshow":               true,
		"onsort":               true,
		"onstalled":            true,
		"onsubmit":             true,
		"onsuspend":            true,
		"ontimeupdate":         true,
		"ontoggle":             true,
		"onvolumechange":       true,
		"onwaiting":            true,
		"onwheel":              true,
		"nonce":                true, "integrity": true, "crossorigin": true, "referrerpolicy": true,
		"loading": true, "decoding": true, "fetchpriority": true,
		"align": true, "bgcolor": true, "border": true, "cellpadding": true, "cellspacing": true,
		"width": true, "height": true,
		"autocapitalize": true, "autocorrect": true, "spellcheck": true,
		"enterkeyhint": true, "inputmode": true,
	}
	return ignoredAttributes[key]
}

func ignoredURL(url string) bool {
	return url == "" || strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "javascript:") || strings.HasPrefix(url, "mailto:") || strings.HasPrefix(url, "tel:")
}
