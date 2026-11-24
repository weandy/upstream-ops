package siteops

import (
	"encoding/xml"
	"html"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

type cardNode struct {
	tag, text string
	attrs     []xml.Attr
	children  []*cardNode
}

var cardTags = strings.Fields("table thead tbody tfoot tr td th div span p br hr h1 h2 h3 h4 strong b em i u ul ol li a")
var cardBlocks = strings.Fields("table thead tbody tfoot tr div p br hr h1 h2 h3 h4 ul ol li")
var cssLength = regexp.MustCompile(`^(0|[0-9]{1,4}([.][0-9]{1,2})?(px|em|rem|pt|%))$`)
var cssColor = regexp.MustCompile(`^(#[0-9a-f]{3}|#[0-9a-f]{6}|black|white|gray|grey|red|green|blue|navy|teal|silver|transparent)$`)
var cssFont = regexp.MustCompile(`^[a-z][a-z0-9 -]{0,30}(, *[a-z][a-z0-9 -]{0,30}){0,4}$`)
var cardNumber = regexp.MustCompile(`^[0-9]{1,4}%?$`)

func validMailLink(s string) bool {
	if len(s) > 2048 || strings.ContainsAny(s, "<>\\\"{}") || strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Hostname() != "" && strings.HasPrefix(s, "https://")
	case "mailto":
		address, err := url.PathUnescape(u.Opaque)
		if err != nil || strings.ContainsAny(address, "?#") {
			return false
		}
		_, err = NormalizeBareEmail(address)
		return err == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
	}
	return false
}
func safeCardCSS(prop, v string) bool {
	parts := strings.Fields(v)
	if len(parts) == 0 {
		return false
	}
	switch prop {
	case "color", "background-color", "border-color":
		return cssColor.MatchString(v)
	case "font-family":
		return cssFont.MatchString(v)
	case "font-weight":
		return slices.Contains(strings.Fields("normal bold 100 200 300 400 500 600 700 800 900"), v)
	case "font-style":
		return v == "italic" || v == "normal"
	case "text-decoration":
		return v == "none" || v == "underline"
	case "text-align":
		return slices.Contains(strings.Fields("left center right justify"), v)
	case "vertical-align":
		return slices.Contains(strings.Fields("top middle bottom baseline"), v)
	case "display":
		return slices.Contains(strings.Fields("block inline inline-block table"), v)
	case "border-collapse":
		return v == "collapse" || v == "separate"
	case "border", "border-top", "border-bottom", "border-left", "border-right":
		return v == "0" || len(parts) == 3 && cssLength.MatchString(parts[0]) && slices.Contains([]string{"solid", "dashed", "dotted"}, parts[1]) && cssColor.MatchString(parts[2])
	case "line-height":
		return cssLength.MatchString(v) || regexpUnitless.MatchString(v)
	case "font-size", "width", "height", "max-width", "padding-top", "padding-bottom", "padding-left", "padding-right", "margin-top", "margin-bottom", "margin-left", "margin-right":
		return cssLength.MatchString(v)
	case "margin", "padding", "border-radius":
		if len(parts) > 4 {
			return false
		}
		for _, p := range parts {
			if !cssLength.MatchString(p) {
				return false
			}
		}
		return true
	}
	return false
}

var regexpUnitless = regexp.MustCompile(`^[0-9]([.][0-9]{1,2})?$`)

func cardAttr(tag, key, v string) string {
	switch key {
	case "style":
		var out []string
		for _, s := range strings.Split(strings.ToLower(v), ";") {
			prop, val, ok := strings.Cut(s, ":")
			prop, val = strings.TrimSpace(prop), strings.TrimSpace(val)
			if ok && safeCardCSS(prop, val) {
				out = append(out, prop+": "+val)
			}
		}
		return strings.Join(out, "; ")
	case "align":
		if slices.Contains([]string{"left", "center", "right"}, v) {
			return v
		}
	case "valign":
		if slices.Contains([]string{"top", "middle", "bottom"}, v) {
			return v
		}
	case "width", "cellpadding", "cellspacing", "border", "colspan", "rowspan":
		if cardNumber.MatchString(v) {
			return v
		}
	case "role":
		if tag == "table" && v == "presentation" {
			return v
		}
	case "href":
		if tag == "a" && (v == "{{.SiteURL}}" || validMailLink(v)) {
			return v
		}
	}
	return ""
}

// Decode only a bounded fragment. Unknown elements and their entire subtrees are discarded.
// The serializer emits fresh tag/attribute literals and always escapes character data.
func parseCard(src string) (*cardNode, error) {
	d := xml.NewDecoder(strings.NewReader("<mailroot>" + src + "</mailroot>"))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	root := &cardNode{}
	stack := []*cardNode{root}
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrMailTemplateInvalid
		}
		parent := stack[len(stack)-1]
		switch t := t.(type) {
		case xml.StartElement:
			if len(stack) > 100 {
				return nil, ErrMailTemplateInvalid
			}
			tag := strings.ToLower(t.Name.Local)
			var n *cardNode
			if len(stack) == 1 && tag == "mailroot" {
				n = root
			} else if parent != nil && t.Name.Space == "" && slices.Contains(cardTags, tag) {
				n = &cardNode{tag: tag}
				seen := map[string]bool{}
				for _, a := range t.Attr {
					k := strings.ToLower(a.Name.Local)
					if v := cardAttr(tag, k, a.Value); v != "" && a.Name.Space == "" && !seen[k] {
						n.attrs = append(n.attrs, xml.Attr{Name: xml.Name{Local: k}, Value: v})
						seen[k] = true
					}
				}
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) < 2 {
				return nil, ErrMailTemplateInvalid
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if parent != nil {
				parent.children = append(parent.children, &cardNode{text: string(t)})
			}
		}
	}
	return root, nil
}

type cardOutput int

const (
	cardHTML cardOutput = iota
	cardPreview
	cardText
)

func (n *cardNode) write(b *strings.Builder, mode cardOutput) {
	if n.text != "" {
		if mode == cardText {
			b.WriteString(n.text)
		} else {
			b.WriteString(html.EscapeString(n.text))
		}
		return
	}
	tag := n.tag
	if mode == cardPreview && tag == "a" {
		tag = "span"
	}
	href := ""
	if mode != cardText && tag != "" {
		b.WriteString("<" + tag)
	}
	for _, a := range n.attrs {
		if a.Name.Local == "href" {
			href = a.Value
			if mode == cardPreview {
				continue
			}
		}
		if mode != cardText {
			b.WriteString(" " + a.Name.Local + "=\"" + html.EscapeString(a.Value) + "\"")
		}
	}
	if mode != cardText && tag != "" {
		if tag == "a" && href != "" {
			b.WriteString(` rel="noopener noreferrer"`)
		}
		b.WriteByte('>')
	}
	if mode == cardText && slices.Contains(cardBlocks, tag) {
		b.WriteByte('\n')
	}
	for _, c := range n.children {
		c.write(b, mode)
	}
	if mode == cardText {
		if href != "" {
			b.WriteString(" (" + href + ")")
		}
		if slices.Contains(cardBlocks, tag) {
			b.WriteByte('\n')
		} else if tag == "td" || tag == "th" {
			b.WriteByte(' ')
		}
	} else if tag != "" && tag != "br" && tag != "hr" {
		b.WriteString("</" + tag + ">")
	}
}
func (n *cardNode) render(mode cardOutput) string {
	var b strings.Builder
	n.write(&b, mode)
	if mode != cardText {
		return b.String()
	}
	lines := []string{}
	for _, line := range strings.Split(b.String(), "\n") {
		if l := strings.Join(strings.Fields(line), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
