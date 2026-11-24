package siteops

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	ht "html/template"
	"mime/multipart"
	"net/textproto"
	"reflect"
	"regexp"
	"slices"
	"strings"
	tt "text/template"
	"text/template/parse"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bejix/upstream-ops/backend/storage"
)

type RenderedMailTemplate struct {
	MailTemplatePreview
	HTML string
}

var mailAction = regexp.MustCompile(`^\s*(\.[A-Za-z]+|if\s+\.[A-Za-z]+|else|end)\s*$`)

func validateMailActions(src string, used map[MailTemplateVar]bool) error {
	for tail := src; ; {
		_, after, ok := strings.Cut(tail, "{{")
		if !ok {
			break
		}
		action, rest, ok := strings.Cut(after, "}}")
		if !ok || !mailAction.MatchString(action) {
			return ErrMailTemplateInvalid
		}
		tail = rest
	}
	t, err := tt.New("mail").Parse(src)
	if err != nil {
		return ErrMailTemplateInvalid
	}
	return walkMailAST(t.Tree.Root, used)
}
func walkMailAST(list *parse.ListNode, used map[MailTemplateVar]bool) error {
	if list == nil {
		return nil
	}
	for _, n := range list.Nodes {
		var pipe *parse.PipeNode
		switch v := n.(type) {
		case *parse.TextNode:
			continue
		case *parse.ActionNode:
			pipe = v.Pipe
		case *parse.IfNode:
			pipe = v.Pipe
			if err := walkMailAST(v.List, used); err != nil {
				return err
			}
			if err := walkMailAST(v.ElseList, used); err != nil {
				return err
			}
		default:
			return ErrMailTemplateInvalid
		}
		if len(pipe.Decl) != 0 || len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 1 {
			return ErrMailTemplateInvalid
		}
		field, ok := pipe.Cmds[0].Args[0].(*parse.FieldNode)
		if !ok || len(field.Ident) != 1 {
			return ErrMailTemplateInvalid
		}
		if _, ok := reflect.TypeOf(MailTemplateVars{}).FieldByName(field.Ident[0]); !ok {
			return ErrMailTemplateInvalid
		}
		used[MailTemplateVar(field.Ident[0])] = true
	}
	return nil
}

type mailRenderBuffer struct{ bytes.Buffer }

func (b *mailRenderBuffer) Write(p []byte) (int, error) {
	if len(p) > MailMaxMIMEBytes-b.Len() {
		return 0, ErrMailTemplateInvalid
	}
	return b.Buffer.Write(p)
}
func mailHeaderSafe(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func ValidateMailTemplate(in MailTemplatePreviewInput) (MailTemplatePreviewInput, []MailTemplateVar, error) {
	if strings.TrimSpace(in.Subject) == "" || len(in.Subject) > 255 || !mailHeaderSafe(in.Subject) || len(in.HTMLBody) > MailMaxHTMLBytes || len(in.TextBody) > MailMaxTextBytes || !utf8.ValidString(in.HTMLBody) || !utf8.ValidString(in.TextBody) {
		return in, nil, ErrMailTemplateInvalid
	}
	used := map[MailTemplateVar]bool{}
	if err := validateMailActions(in.HTMLBody, used); err != nil {
		return in, nil, err
	}
	clear(used)
	root, err := parseCard(in.HTMLBody)
	if err != nil {
		return in, nil, err
	}
	in.HTMLBody = root.render(cardHTML)
	if len(in.HTMLBody) > MailMaxHTMLBytes || strings.TrimSpace(root.render(cardText)) == "" {
		return in, nil, ErrMailTemplateInvalid
	}
	for _, src := range []string{in.Subject, in.TextBody, in.HTMLBody} {
		if err := validateMailActions(src, used); err != nil {
			return in, nil, err
		}
	}
	t, err := ht.New("card").Parse(in.HTMLBody)
	if err != nil {
		return in, nil, ErrMailTemplateInvalid
	}
	// Execution also checks html/template's context transitions across conditional branches.
	if err = t.Execute(&mailRenderBuffer{}, MailTemplateVars{}); err != nil {
		return in, nil, ErrMailTemplateInvalid
	}
	vars := make([]MailTemplateVar, 0, len(used))
	for name := range used {
		vars = append(vars, name)
	}
	slices.Sort(vars)
	return in, vars, nil
}
func RenderMailTemplate(in MailTemplatePreviewInput, vars MailTemplateVars) (RenderedMailTemplate, error) {
	var out RenderedMailTemplate
	in, used, err := ValidateMailTemplate(in)
	if err != nil {
		return out, err
	}
	if slices.Contains(used, MailTemplateVarSiteURL) && vars.SiteURL != "" && !(validMailLink(vars.SiteURL) && strings.HasPrefix(vars.SiteURL, "https://")) {
		return out, ErrMailTemplateInvalid
	}
	renderText := func(src string) (string, error) {
		t, err := tt.New("text").Parse(src)
		if err != nil {
			return "", ErrMailTemplateInvalid
		}
		var b mailRenderBuffer
		if t.Execute(&b, vars) != nil {
			return "", ErrMailTemplateInvalid
		}
		return b.String(), nil
	}
	out.Subject, err = renderText(in.Subject)
	if err != nil || !mailHeaderSafe(out.Subject) || strings.TrimSpace(out.Subject) == "" {
		return out, ErrMailTemplateInvalid
	}
	t, err := ht.New("card").Parse(in.HTMLBody)
	var b mailRenderBuffer
	if err != nil || t.Execute(&b, vars) != nil {
		return out, ErrMailTemplateInvalid
	}
	root, err := parseCard(b.String())
	if err != nil {
		return out, err
	}
	out.HTML, out.PreviewHTML, out.Text = root.render(cardHTML), root.render(cardPreview), root.render(cardText)
	out.UsedVars = used
	if in.TextBody != "" {
		out.Text, err = renderText(in.TextBody)
	}
	return out, err
}
func BuiltinMailTemplates(scope storage.MailScopeKey) []storage.SiteOpsMailTemplate {
	defs := []struct{ key, name, category, subject, body string }{
		{"marketing_promo", "活动通知", "marketing", "{{.SiteName}} 活动通知", "<p>本期活动已上线，欢迎查看活动详情。</p>"},
		{"renewal_reminder", "订阅续费提醒", "renewal", "{{.SiteName}} 订阅到期提醒", "<p>您的 {{.PlanName}} 将于 {{.ExpiresDate}} 到期。</p>"},
		{"reactivation", "余额提醒", "reactivation", "{{.SiteName}} 账户余额提醒", "<p>您的账户余额为 {{.BalanceUSD}}。欢迎回来查看服务。</p>"},
	}
	rows := make([]storage.SiteOpsMailTemplate, 0, len(defs))
	for _, d := range defs {
		rows = append(rows, storage.SiteOpsMailTemplate{InstanceID: scope.InstanceID, TargetBinding: scope.TargetBinding, TemplateKey: "builtin:" + d.key, Revision: 1, Name: d.name, Category: d.category, Subject: d.subject, IsBuiltIn: true, HTMLBody: `<table role="presentation" width="100%"><tr><td style="padding: 24px; background-color: #f3f6fa"><h2>{{.SiteName}}</h2>` + d.body + `<a href="{{.SiteURL}}" style="color: #245abc">访问站点</a></td></tr></table>`})
	}
	return rows
}

// Forty-five bytes produce a 72-character encoded word, including its delimiters.
func mailEncodedWords(s string) string {
	var words []string
	for len(s) > 0 {
		n := min(45, len(s))
		for n < len(s) && !utf8.RuneStart(s[n]) {
			n--
		}
		words = append(words, "=?UTF-8?B?"+base64.StdEncoding.EncodeToString([]byte(s[:n]))+"?=")
		s = s[n:]
	}
	return strings.Join(words, "\r\n ")
}

type MailMessageHeaders struct {
	FromEmail, FromName, To, ReplyTo, UnsubscribeURL string
	Date                                             time.Time
}

func BuildMailMIME(h MailMessageHeaders, m RenderedMailTemplate) ([]byte, error) {
	from, err := NormalizeBareEmail(h.FromEmail)
	if err != nil {
		return nil, err
	}
	to, err := NormalizeBareEmail(h.To)
	if err != nil {
		return nil, err
	}
	if !mailHeaderSafe(h.FromName) || !mailHeaderSafe(m.Subject) || m.Subject == "" || len(h.FromName)+len(m.Subject)+len(m.HTML)+len(m.Text) > MailMaxMIMEBytes {
		return nil, ErrMailTemplateInvalid
	}
	if h.FromName != "" {
		from = mailEncodedWords(h.FromName) + " <" + from + ">"
	}
	if h.Date.IsZero() {
		h.Date = time.Now()
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, ErrMailCryptoUnavailable
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Date: %s\r\nMessage-ID: <%s@%s>\r\nFrom: %s\r\nTo: %s\r\nSubject: %s\r\n", h.Date.Format(time.RFC1123Z), hex.EncodeToString(id[:]), strings.Split(h.FromEmail, "@")[1], from, to, mailEncodedWords(m.Subject))
	if h.ReplyTo != "" {
		reply, err := NormalizeBareEmail(h.ReplyTo)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "Reply-To: %s\r\n", reply)
	}
	if h.UnsubscribeURL != "" {
		if len(h.UnsubscribeURL) > 512 || !validMailLink(h.UnsubscribeURL) {
			return nil, ErrMailInvalidInput
		}
		fmt.Fprintf(&b, "List-Unsubscribe: <%s>\r\n", h.UnsubscribeURL)
	}
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\nAuto-Submitted: auto-generated\r\nPrecedence: bulk\r\nContent-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", w.Boundary())
	for _, part := range []struct{ kind, src string }{{"plain", m.Text}, {"html", m.HTML}} {
		p, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/" + part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"base64"}})
		if err != nil {
			return nil, ErrMailTemplateInvalid
		}
		enc := base64.StdEncoding.EncodeToString([]byte(part.src))
		for len(enc) > 0 {
			n := min(len(enc), 76)
			fmt.Fprintf(p, "%s\r\n", enc[:n])
			enc = enc[n:]
		}
	}
	if w.Close() != nil || b.Len() > MailMaxMIMEBytes {
		return nil, ErrMailTemplateInvalid
	}
	return b.Bytes(), nil
}
