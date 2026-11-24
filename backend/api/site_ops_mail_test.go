package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/siteops"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func mailRequest(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:8418"
	req.RemoteAddr = "127.0.0.1:3001"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
func TestMailGlobalCacheHeaders(t *testing.T) {
	for _, status := range []int{401, 403} {
		r := gin.New()
		g := r.Group("/api")
		g.Use(mailCacheHeaders)
		if status == 401 {
			g.Use(func(c *gin.Context) { c.AbortWithStatusJSON(401, gin.H{"error": "missing token"}) })
		}
		g.GET("/site-ops/instances/:id/mail/overview", siteops.RequireMailAccessMiddleware(func() bool { return false }), func(c *gin.Context) { c.Status(200) })
		req := httptest.NewRequest("GET", "/api/site-ops/instances/1/mail/overview", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != status || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" || !strings.Contains(rec.Body.String(), "SITE_OPS_MAIL_AUTH_REQUIRED") {
			t.Fatal(status, rec.Code, rec.Header(), rec.Body.String())
		}
	}
}
func TestMailHTTPContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users") {
			fmt.Fprint(w, `{"items":[{"id":1,"role":"user","status":"active","email":"api-recipient-canary@example.com"}],"has_more":false,"page":1,"total":1,"page_size":100}`)
			return
		}
		fmt.Fprint(w, "[]")
	}))
	defer server.Close()
	r, svc := setupSiteOpsTestRouter(t, false, config.SiteOpsConfig{AllowLoopback: true, PerInstanceMaxConcurrency: 4})
	// Drive preparation separately in worker integration tests. Keep the active slot stable here.
	svc.Stop()
	inst, e := svc.CreateInstance(context.Background(), siteops.CreateInstanceInput{Name: "Portal", APIKey: "fixture", BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	src, e := svc.CreateInstance(context.Background(), siteops.CreateInstanceInput{Name: "Source", APIKey: "fixture", BaseURL: server.URL + "/source"})
	if e != nil {
		t.Fatal(e)
	}
	root := fmt.Sprintf("/api/site-ops/instances/%d/mail", inst.ID)
	const scope = `"generation":1,"cred_gen":1`
	call := func(method, path, body string, status int, code string) json.RawMessage {
		t.Helper()
		rec := mailRequest(r, method, root+path, body)
		var res struct {
			Data        json.RawMessage
			Code, Error string
		}
		if rec.Code != status || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Code != code || (code != "" && res.Error == "") || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get(siteops.MailAuthModeHeader) != "local_loopback_only" {
			t.Fatalf("%s %s got %d %s", method, path, rec.Code, rec.Body.String())
		}
		for _, secret := range []string{"api-recipient-canary@example.com", "api-password-canary", "api-user-canary", "target_binding", "payload_cipher", "email_hash"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("HTTP response exposed a private field")
			}
		}
		return res.Data
	}
	var overview siteops.MailOverview
	json.Unmarshal(call("GET", "/overview", "", 200, ""), &overview)
	if overview.TemplateCount != 3 {
		t.Fatal(overview)
	}
	call("GET", "/smtp", "", 200, "")
	call("POST", "/smtp/test", `{`+scope+`,"expected_revision":1,"request_id":"`+uuid.NewString()+`"}`, 409, "MAIL_SMTP_NOT_READY")
	smtp := `{` + scope + `,"expected_revision":0,"host":"smtp.example.com","port":465,"tls_mode":"implicit","auth_mode":"plain","username":"api-user-canary","password":"api-password-canary","from_email":"sender@example.com"}`
	call("PUT", "/smtp", smtp, 200, "")
	call("PUT", "/smtp", smtp, 409, "MAIL_REVISION_MISMATCH")
	cp := fmt.Sprintf(`{%s,"expected_revision":1,"source_instance_id":%d,"source_generation":1,"source_cred_gen":1,"source_revision":1}`, scope, src.ID)
	call("POST", "/smtp/copy", cp, 404, "MAIL_RESOURCE_NOT_FOUND")
	body := `{` + scope + `,"expected_revision":0,"name":"Fixture","category":"marketing","subject":"Subject","html_body":"<p>Hello</p>"}`
	var tpl siteops.MailTemplateView
	json.Unmarshal(call("POST", "/templates", body, 201, ""), &tpl)
	tp := fmt.Sprintf("/templates/%d", tpl.ID)
	if b := call("GET", "/templates?limit=1", "", 200, ""); strings.Contains(string(b), "html_body") || !strings.Contains(string(b), "next_cursor") {
		t.Fatal("template list")
	}
	call("GET", tp, "", 200, "")
	call("PUT", tp, strings.Replace(body, `"expected_revision":0`, `"expected_revision":1`, 1), 200, "")
	call("POST", "/templates/copy", strings.TrimSuffix(cp, "}")+`,"source_template_id":1}`, 400, "MAIL_INVALID_INPUT")
	call("POST", "/templates/preview", `{`+scope+`,"subject":"Hello","html_body":"<p>Text</p>"}`, 200, "")
	call("GET", "/candidates?page=1", "", 200, "")
	call("GET", "/groups", "", 200, "")
	call("POST", "/audience/preview", `{`+scope+`,"audience":{"mode":"selected","user_ids":[1]}}`, 200, "")
	batch := fmt.Sprintf(`{%s,"request_id":"%s","smtp_revision":1,"template_id":%d,"template_revision":2,"audience":{"mode":"selected","user_ids":[1]}}`, scope, uuid.NewString(), tpl.ID)
	var b siteops.MailBatchView
	json.Unmarshal(call("POST", "/batches", batch, 202, ""), &b)
	call("POST", "/batches", batch, 200, "")
	call("POST", "/batches", strings.Replace(batch, "[1]", "[2]", 1), 409, "MAIL_IDEMPOTENCY_CONFLICT")
	bp := fmt.Sprintf("/batches/%d", b.ID)
	call("GET", "/batches?limit=1", "", 200, "")
	call("GET", bp, "", 200, "")
	call("GET", bp+"/items", "", 200, "")
	call("POST", bp+"/cancel", "{"+scope+"}", 200, "")
	call("POST", bp+"/retry-failed", `{`+scope+`,"smtp_revision":1,"request_id":"`+uuid.NewString()+`"}`, 409, "MAIL_NO_RETRYABLE_ITEMS")
	add := `{` + scope + `,"emails":["api-recipient-canary@example.com"]}`
	call("POST", "/suppressions", add, 201, "")
	call("POST", "/suppressions", add, 200, "")
	var p siteops.MailCursorPage[siteops.MailSuppressionView]
	json.Unmarshal(call("GET", "/suppressions", "", 200, ""), &p)
	if len(p.Items) != 1 {
		t.Fatal(p)
	}
	call("DELETE", fmt.Sprintf("/suppressions/%d?generation=1&cred_gen=1", p.Items[0].ID), "", 200, "")
	call("DELETE", tp+"?generation=1&cred_gen=1&expected_revision=2", "", 200, "")
	for _, invalid := range []string{`{"audience":{"mode":"all","mode":"selected"}}`, `{"generation":1,"\u0067eneration":1}`, `{"alien":1}`, `{} {}`, `null`} {
		call("POST", "/audience/preview", invalid, 400, "MAIL_INVALID_INPUT")
	}
	call("PUT", "/smtp", strings.Repeat(" ", siteops.MailMaxRequestBytes+1), 413, "MAIL_INVALID_INPUT")
	call("POST", "/templates/preview", strings.Repeat(" ", siteops.MailMaxTemplateRequestBytes+1), 413, "MAIL_INVALID_INPUT")
	for _, path := range []string{"/templates/9007199254740992", "/templates/0", "/templates?cursor=0", "/templates?limit=101", "/batches?limit=51", "/batches?status=bogus", "/candidates?page=101"} {
		call("GET", path, "", 400, "MAIL_INVALID_INPUT")
	}
}
