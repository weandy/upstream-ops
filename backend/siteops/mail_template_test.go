package siteops

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestMailCardOutputs(t *testing.T) {
	in := MailTemplatePreviewInput{Subject: "{{.SiteName}}", HTMLBody: `<div onclick="steal()" style="color:#102030; background-image:url(x); position:fixed"><style>evil</style><script>evil</script><iframe>evil</iframe><form>evil</form><object>evil</object><img src="x"><p>{{if .SiteName}}{{.SiteName}}{{end}}</p><a href="{{.SiteURL}}">详情</a><a href="javascript:evil()">非法</a></div>`}
	v := MailTemplateVars{SiteURL: "https://example.com/", SiteName: "示例<script>名称</script>"}
	out, e := RenderMailTemplate(in, v)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"evil", "<script", "<iframe", "<img", "onclick", "url(", "fixed", "javascript:"} {
		if strings.Contains(out.HTML, bad) {
			t.Fatalf("unsafe HTML: %s", bad)
		}
	}
	if !strings.Contains(out.HTML, "color: #102030") || !strings.Contains(out.HTML, v.SiteURL) || strings.Contains(out.PreviewHTML, "href=") || strings.Contains(out.PreviewHTML, "<a ") || !strings.Contains(out.Text, "详情 ("+v.SiteURL+")") || !strings.Contains(out.Text, v.SiteName) {
		t.Fatalf("unexpected outputs %#v", out)
	}
	for _, s := range []string{"{{.Unknown}}", "{{.SiteName | printf}}", "{{range .SiteName}}{{end}}", "{{define \"x\"}}oops{{end}}", "{{$x := .UserID}}", "{{.SiteURL.Path}}"} {
		in.HTMLBody = "<p>" + s + "</p>"
		if _, _, e := ValidateMailTemplate(in); e == nil {
			t.Fatal("accepted forbidden action", s)
		}
	}
	for _, row := range BuiltinMailTemplates(storage.MailScopeKey{InstanceID: 1, TargetBinding: "x"}) {
		if _, e := RenderMailTemplate(MailTemplatePreviewInput{Subject: row.Subject, HTMLBody: row.HTMLBody}, v); e != nil {
			t.Fatal(row.TemplateKey, e)
		}
	}
}
func TestMailMIME(t *testing.T) {
	subject := strings.Repeat("中😀A", 31)
	h := MailMessageHeaders{FromEmail: "sender@example.com", FromName: "发送者", To: "user@example.com", ReplyTo: "reply@example.com", UnsubscribeURL: "https://example.com/unsubscribe"}
	v := RenderedMailTemplate{MailTemplatePreview: MailTemplatePreview{Subject: subject, Text: "plain"}, HTML: "<p>正文</p>"}
	raw, e := BuildMailMIME(h, v)
	if e != nil {
		t.Fatal(e)
	}
	msg, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if e != nil || decoded != subject {
		t.Fatal("encoded subject")
	}
	for _, word := range strings.Fields(msg.Header.Get("Subject")) {
		chunk, e := new(mime.WordDecoder).DecodeHeader(word)
		if e != nil || !utf8.ValidString(chunk) || len(word) > 75 {
			t.Fatal("word boundary")
		}
	}
	if msg.Header.Get("List-Unsubscribe") != "<"+h.UnsubscribeURL+">" || msg.Header.Get("List-Unsubscribe-Post") != "" {
		t.Fatal("unsubscribe headers")
	}
	_, p, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	parts := multipart.NewReader(msg.Body, p["boundary"])
	for _, expected := range []string{v.Text, v.HTML} {
		part, e := parts.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if e != nil || string(body) != expected {
			t.Fatal("MIME payload")
		}
	}
	if _, e := parts.NextPart(); e != io.EOF {
		t.Fatal("extra part")
	}
	h.FromName = "x\r\nBcc: x"
	if _, e := BuildMailMIME(h, v); e == nil {
		t.Fatal("header injection")
	}
	h.FromName = ""
	h.UnsubscribeURL = "mailto:a@example.com?bcc=bad"
	if _, e := BuildMailMIME(h, v); e == nil {
		t.Fatal("unsafe mailto")
	}
	h.UnsubscribeURL = ""
	v.HTML = strings.Repeat("x", MailMaxMIMEBytes)
	if _, e := BuildMailMIME(h, v); e == nil {
		t.Fatal("size limit")
	}
}
