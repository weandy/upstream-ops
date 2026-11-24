package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/gin-gonic/gin"
)

// The global authentication middleware may terminate before the mail group runs.
func mailCacheHeaders(c *gin.Context) {
	path := strings.SplitN(strings.TrimPrefix(c.Request.URL.Path, "/api/site-ops/instances/"), "/", 3)
	if strings.HasPrefix(c.Request.URL.Path, "/api/site-ops/instances/") && len(path) >= 2 && path[0] != "" && path[1] == "mail" {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		original := c.Writer
		c.Writer = &mailAuthResponseWriter{ResponseWriter: original}
		defer func() { c.Writer = original }()
	}
	c.Next()
}

type mailAuthResponseWriter struct{ gin.ResponseWriter }

func (w *mailAuthResponseWriter) Write(body []byte) (int, error) {
	if w.Status() == 401 {
		_, err := w.ResponseWriter.Write([]byte(`{"code":"SITE_OPS_MAIL_AUTH_REQUIRED","error":"需要管理员身份验证"}`))
		return len(body), err
	}
	return w.ResponseWriter.Write(body)
}

func mailFail(c *gin.Context, e error) {
	code := siteops.MailPublicErrorCode(e)
	status := http.StatusInternalServerError
	switch code {
	case siteops.ErrCodeMailInvalidInput, siteops.ErrCodeMailTemplateInvalid, siteops.ErrCodeMailTemplateBuiltinProtected:
		status = 400
	case siteops.ErrCodeMailAuthRequired:
		status = 403
	case siteops.ErrCodeMailResourceNotFound:
		status = 404
	case siteops.ErrCodeMailGenerationMismatch, siteops.ErrCodeMailRevisionMismatch, siteops.ErrCodeMailIdempotencyConflict, siteops.ErrCodeMailActiveBatchExists, siteops.ErrCodeMailRetryAlreadyExists, siteops.ErrCodeMailNoRetryableItems, siteops.ErrCodeMailTemplateLimitReached, siteops.ErrCodeMailInstanceDisabled, siteops.ErrCodeMailBatchNotCancellable, siteops.ErrCodeMailSMTPNotReady:
		status = 409
	case siteops.ErrCodeMailBudgetExceeded, siteops.ErrCodeMailRateLimited, siteops.ErrCodeMailTestRateLimited:
		status = 429
	case siteops.ErrCodeMailSMTPConfigBlocked, siteops.ErrCodeMailPrepLimitExceeded, siteops.ErrCodeMailPrepCoverageIncomplete, siteops.ErrCodeMailPrepPageDrift, siteops.ErrCodeMailPrepDeadlineExceeded:
		status = 422
	case siteops.ErrCodeMailCryptoUnavailable, siteops.ErrCodeMailSMTPUnavailable:
		status = 503
	}
	out := siteops.MailErrorResponse{Code: code, Error: string(code)}
	var detail *siteops.MailBatchError
	if errors.As(e, &detail) {
		out.ActiveBatchID = detail.ActiveBatchID
		out.RetryAfterSeconds = detail.RetryAfterSeconds
	}
	if out.RetryAfterSeconds > 0 {
		c.Header("Retry-After", strconv.Itoa(out.RetryAfterSeconds))
	}
	c.AbortWithStatusJSON(status, out)
}
func mailInvalid(c *gin.Context, field string) {
	c.AbortWithStatusJSON(400, siteops.MailErrorResponse{Code: siteops.ErrCodeMailInvalidInput, Error: "请求参数无效", Field: field})
}
func mailNumber(raw string, max uint64) (uint, bool) {
	if raw == "" || raw[0] < '0' || raw[0] > '9' {
		return 0, false
	}
	v, e := strconv.ParseUint(raw, 10, 53)
	return uint(v), e == nil && v >= 1 && v <= max
}

// Validate structural keys before decoding into Go structs (encoding/json accepts duplicate keys).
func checkMailValue(d *json.Decoder, t json.Token, depth int, root map[string]bool) bool {
	if depth > 32 {
		return false
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t != nil
	}
	if delim != '{' && delim != '[' {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			k, e := d.Token()
			s, ok := k.(string)
			if e != nil || !ok || s != strings.ToLower(s) || seen[s] {
				return false
			}
			seen[s] = true
			if depth == 0 {
				root[s] = true
			}
		}
		v, e := d.Token()
		if e != nil || !checkMailValue(d, v, depth+1, root) {
			return false
		}
	}
	_, e := d.Token()
	return e == nil
}
func mailBody[T any](c *gin.Context, limit int, required []string) (out T, ok bool) {
	r := http.MaxBytesReader(c.Writer, c.Request.Body, int64(limit))
	b, e := io.ReadAll(r)
	if e != nil {
		var size *http.MaxBytesError
		if errors.As(e, &size) {
			c.AbortWithStatusJSON(413, siteops.MailErrorResponse{Code: siteops.ErrCodeMailInvalidInput, Error: "请求体过大"})
			return
		}
		mailInvalid(c, "body")
		return
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	first, e := d.Token()
	root := map[string]bool{}
	if !utf8.Valid(b) || e != nil || first != json.Delim('{') || !checkMailValue(d, first, 0, root) {
		mailInvalid(c, "body")
		return
	}
	if _, e = d.Token(); e != io.EOF {
		mailInvalid(c, "body")
		return
	}
	for _, k := range required {
		if !root[k] {
			mailInvalid(c, k)
			return
		}
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&out); e != nil {
		mailInvalid(c, "body")
		return
	}
	return out, true
}

type mailEndpoint struct {
	c        *gin.Context
	m        *siteops.MailSite
	resource uint
}

func mailHandler(s *siteops.Service, fn func(mailEndpoint)) gin.HandlerFunc {
	return func(c *gin.Context) {
		var id, resource uint
		for _, p := range c.Params {
			v, ok := mailNumber(p.Value, siteops.MailMaxSafeID)
			if !ok {
				mailInvalid(c, p.Key)
				return
			}
			if p.Key == "id" {
				id = v
			} else {
				resource = v
			}
		}
		m, e := s.MailSite(c.Request.Context(), id)
		if e != nil {
			mailFail(c, e)
			return
		}
		fn(mailEndpoint{c, m, resource})
	}
}
func (h mailEndpoint) reply(status int, data any, err error) {
	if err != nil {
		mailFail(h.c, err)
		return
	}
	h.c.JSON(status, gin.H{"data": data})
}
func mailRead[V any](fn func(mailEndpoint) (V, error)) func(mailEndpoint) {
	return func(h mailEndpoint) { v, e := fn(h); h.reply(200, v, e) }
}
func mailWrite[T, V any](limit, status int, required []string, fn func(mailEndpoint, T) (V, error)) func(mailEndpoint) {
	return func(h mailEndpoint) {
		in, ok := mailBody[T](h.c, limit, required)
		if !ok {
			return
		}
		v, e := fn(h, in)
		h.reply(status, v, e)
	}
}
func (h mailEndpoint) number(key string, fallback uint, max uint64) (uint, bool) {
	vals, ok := h.c.Request.URL.Query()[key]
	if !ok && fallback > 0 {
		return fallback, true
	}
	if len(vals) == 1 {
		if n, ok := mailNumber(vals[0], max); ok {
			return n, true
		}
	}
	mailInvalid(h.c, key)
	return 0, false
}
func mailList[V any](limit int, statuses string, fn func(mailEndpoint, siteops.MailListQuery) (V, error)) func(mailEndpoint) {
	return func(h mailEndpoint) {
		n, ok := h.number("limit", 20, uint64(limit))
		if !ok {
			return
		}
		q := siteops.MailListQuery{Limit: int(n)}
		if h.c.Request.URL.Query().Has("cursor") {
			q.Cursor, ok = h.number("cursor", 0, siteops.MailMaxSafeID)
			if !ok {
				return
			}
		}
		if h.c.Request.URL.Query().Has("status") {
			q.Status = h.c.Query("status")
			allowed := false
			for _, v := range strings.Fields(statuses) {
				if v == q.Status {
					allowed = true
				}
			}
			if !allowed || len(h.c.Request.URL.Query()["status"]) != 1 {
				mailInvalid(h.c, "status")
				return
			}
		}
		v, e := fn(h, q)
		h.reply(200, v, e)
	}
}
func (h mailEndpoint) deleteScope() (siteops.MailScopeInput, bool) {
	gen, ok := h.number("generation", 0, siteops.MailMaxSafeID)
	if !ok {
		return siteops.MailScopeInput{}, false
	}
	cred, ok := h.number("cred_gen", 0, siteops.MailMaxSafeID)
	return siteops.MailScopeInput{Generation: int64(gen), CredGen: int64(cred)}, ok
}

func registerSiteOpsMail(sg *gin.RouterGroup, d *Deps) {
	g := sg.Group("/instances/:id/mail", siteops.MailRecovery, siteops.RequireMailAccessMiddleware(func() bool {
		if d.Runtime != nil {
			return d.Runtime.CurrentAuth() != nil
		}
		active, _ := d.SiteOps.AccessPolicy()
		return active
	}))
	route := func(method, path string, f func(mailEndpoint)) { g.Handle(method, path, mailHandler(d.SiteOps, f)) }
	const normal = siteops.MailMaxRequestBytes
	const template = siteops.MailMaxTemplateRequestBytes
	revision := []string{"expected_revision"}
	route("GET", "/overview", mailRead(func(h mailEndpoint) (siteops.MailOverview, error) {
		return h.m.Overview(siteops.MailSecurityMode(h.c.Writer.Header().Get(siteops.MailAuthModeHeader)))
	}))
	route("GET", "/smtp", mailRead(func(h mailEndpoint) (siteops.MailSMTPView, error) { return h.m.SMTP() }))
	route("PUT", "/smtp", mailWrite(normal, 200, revision, func(h mailEndpoint, in siteops.SaveMailSMTPInput) (siteops.MailSMTPView, error) {
		return h.m.SaveSMTP(in)
	}))
	route("POST", "/smtp/test", func(h mailEndpoint) {
		in, ok := mailBody[siteops.MailTestInput](h.c, normal, revision)
		if !ok {
			return
		}
		v, done, e := h.m.TestSMTP(in)
		status := 202
		if done {
			status = 200
		}
		h.reply(status, v, e)
	})
	route("POST", "/smtp/copy", mailWrite(normal, 200, revision, func(h mailEndpoint, in siteops.MailCopyInput) (siteops.MailSMTPView, error) { return h.m.CopySMTP(in) }))
	route("GET", "/templates", mailList(100, "", func(h mailEndpoint, q siteops.MailListQuery) (siteops.MailCursorPage[siteops.MailTemplateSummary], error) {
		return h.m.Templates(q)
	}))
	route("GET", "/templates/:tid", mailRead(func(h mailEndpoint) (siteops.MailTemplateView, error) { return h.m.Template(h.resource) }))
	save := func(h mailEndpoint, in siteops.SaveMailTemplateInput) (siteops.MailTemplateView, error) {
		return h.m.SaveTemplate(h.resource, in)
	}
	route("POST", "/templates", mailWrite(template, 201, revision, save))
	route("PUT", "/templates/:tid", mailWrite(template, 200, revision, save))
	route("DELETE", "/templates/:tid", func(h mailEndpoint) {
		sc, ok := h.deleteScope()
		if !ok {
			return
		}
		r, ok := h.number("expected_revision", 0, siteops.MailMaxSafeID)
		if !ok {
			return
		}
		v, e := h.m.DeleteTemplate(h.resource, sc, int64(r))
		h.reply(200, v, e)
	})
	route("POST", "/templates/copy", mailWrite(normal, 201, revision, func(h mailEndpoint, in siteops.MailCopyInput) (siteops.MailTemplateView, error) {
		return h.m.CopyTemplate(in)
	}))
	route("POST", "/templates/preview", mailWrite(template, 200, nil, func(h mailEndpoint, in siteops.MailTemplatePreviewInput) (siteops.MailTemplatePreview, error) {
		return h.m.PreviewTemplate(in)
	}))
	route("GET", "/candidates", func(h mailEndpoint) {
		p, ok := h.number("page", 1, 100)
		if !ok {
			return
		}
		v, e := h.m.Candidates(int(p))
		h.reply(200, v, e)
	})
	route("GET", "/groups", mailRead(func(h mailEndpoint) ([]siteops.MailGroupOption, error) { return h.m.Groups() }))
	route("POST", "/audience/preview", mailWrite(normal, 200, nil, func(h mailEndpoint, in siteops.MailAudiencePreviewInput) (siteops.MailAudiencePreview, error) {
		return h.m.PreviewAudience(in)
	}))
	route("POST", "/batches", func(h mailEndpoint) {
		in, ok := mailBody[siteops.CreateMailBatchInput](h.c, normal, nil)
		if !ok {
			return
		}
		v, replay, e := h.m.CreateBatch(in)
		h.batchReply(v, replay, e)
	})
	route("GET", "/batches", mailList(50, "preparing queued running cancelling cancelled completed completed_with_errors failed", func(h mailEndpoint, q siteops.MailListQuery) (siteops.MailCursorPage[siteops.MailBatchView], error) {
		return h.m.Batches(q)
	}))
	route("GET", "/batches/:bid", mailRead(func(h mailEndpoint) (siteops.MailBatchView, error) { return h.m.Batch(h.resource) }))
	route("GET", "/batches/:bid/items", mailList(100, "pending sending sent failed uncertain cancelled suppressed", func(h mailEndpoint, q siteops.MailListQuery) (siteops.MailCursorPage[siteops.MailItemView], error) {
		return h.m.Items(h.resource, q)
	}))
	route("POST", "/batches/:bid/cancel", mailWrite(normal, 200, nil, func(h mailEndpoint, in siteops.MailScopeInput) (siteops.MailBatchView, error) {
		return h.m.CancelBatch(h.resource, in)
	}))
	route("POST", "/batches/:bid/retry-failed", func(h mailEndpoint) {
		in, ok := mailBody[siteops.RetryMailBatchInput](h.c, normal, nil)
		if !ok {
			return
		}
		v, replay, e := h.m.RetryBatch(h.resource, in)
		h.batchReply(v, replay, e)
	})
	route("GET", "/suppressions", mailList(100, "", func(h mailEndpoint, q siteops.MailListQuery) (siteops.MailCursorPage[siteops.MailSuppressionView], error) {
		return h.m.Suppressions(q)
	}))
	route("POST", "/suppressions", func(h mailEndpoint) {
		in, ok := mailBody[siteops.AddMailSuppressionInput](h.c, normal, nil)
		if !ok {
			return
		}
		v, e := h.m.AddMailSuppressions(in)
		status := 200
		if v.Added > 0 {
			status = 201
		}
		h.reply(status, v, e)
	})
	route("DELETE", "/suppressions/:sid", func(h mailEndpoint) {
		sc, ok := h.deleteScope()
		if !ok {
			return
		}
		v, e := h.m.DeleteSuppression(h.resource, sc)
		h.reply(200, v, e)
	})
}
func (h mailEndpoint) batchReply(v siteops.MailBatchView, replay bool, e error) {
	status := 202
	if replay {
		status = 200
	}
	h.reply(status, v, e)
}
