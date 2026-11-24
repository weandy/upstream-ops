package siteops

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMailAccessModes(t *testing.T) {
	tests := []struct {
		name, method, remote, host, origin, fetch, content, subject, header string
		active                                                              bool
		want                                                                int
	}{
		{"local", "POST", "127.0.0.1:123", "localhost:3010", "http://[::1]:3010", "", "application/json; charset=utf-8", "", "", false, 200},
		{"ipv6", "GET", "[::1]:11", "[::1]", "", "", "", "", "", false, 200},
		{"head", "HEAD", "127.0.0.1:42", "127.0.0.1", "", "", "", "", "", false, 200},
		{"remote", "GET", "192.0.2.1:123", "localhost", "", "", "", "", "", false, 403},
		{"host", "GET", "127.0.0.1:123", "public.example", "", "", "", "", "", false, 403},
		{"origin", "GET", "127.0.0.1:123", "localhost", "https://localhost", "", "", "", "", false, 403},
		{"origin path", "GET", "127.0.0.1:123", "localhost", "http://localhost/", "", "", "", "", false, 403},
		{"origin port", "GET", "127.0.0.1:123", "localhost", "http://localhost:70000", "", "", "", "", false, 403},
		{"cross-site", "GET", "127.0.0.1:123", "localhost", "", "cross-site", "", "", "", false, 403},
		{"simple form", "POST", "127.0.0.1:123", "localhost", "", "", "text/plain", "", "", false, 403},
		{"auth required", "GET", "127.0.0.1:123", "localhost", "", "", "", "", "", true, 401},
		{"authenticated", "POST", "192.0.2.1:123", "public.example", "", "", "", "admin", "Forwarded", true, 200},
		{"auth switched off", "POST", "192.0.2.1:123", "public.example", "", "", "", "admin", "", false, 200},
	}
	for _, key := range strings.Fields("Forwarded X-Forwarded-For X-Forwarded-Host X-Forwarded-Proto X-Real-IP X-Client-IP CF-Connecting-IP True-Client-IP Via X-Forwarded-Port") {
		tc := tests[0]
		tc.name = key
		tc.header = key
		tc.want = 403
		tests = append(tests, tc)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := gin.New()
			g.Use(func(c *gin.Context) { c.Set("authSubject", tc.subject) }, RequireMailAccessMiddleware(func() bool { return tc.active }))
			g.Any("/mail", func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest(tc.method, "/mail", nil)
			req.RemoteAddr = tc.remote
			req.Host = tc.host
			for k, v := range map[string]string{"Origin": tc.origin, "Sec-Fetch-Site": tc.fetch, "Content-Type": tc.content} {
				if v != "" {
					req.Header.Set(k, v)
				}
			}
			if tc.header != "" {
				req.Header.Add(tc.header, "")
				req.Header.Add(tc.header, "remote")
			}
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d", rec.Code)
			}
			if tc.want == 200 {
				want := string(MailSecurityModeLocalLoopbackOnly)
				if tc.subject != "" {
					want = string(MailSecurityModeAuthenticated)
				}
				if rec.Header().Get(MailAuthModeHeader) != want {
					t.Fatal("wrong mode")
				}
			}
		})
	}
}
func TestMailRecoveryRedactsRequest(t *testing.T) {
	var log bytes.Buffer
	g := gin.New()
	g.Use(gin.RecoveryWithWriter(&log), MailRecovery)
	g.POST("/mail", func(c *gin.Context) { panic(c.GetHeader("X-Canary")) })
	req := httptest.NewRequest(http.MethodPost, "/mail", strings.NewReader("body-canary"))
	req.Header.Set("X-Canary", "header-canary")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 500 || log.Len() != 0 || strings.Contains(rec.Body.String(), "canary") {
		t.Fatal("panic exposed request")
	}
}
