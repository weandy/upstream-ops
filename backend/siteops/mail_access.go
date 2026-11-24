package siteops

import (
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const MailAuthModeHeader = "X-SiteOps-Mail-Auth-Mode"

var localMailAuthority = regexp.MustCompile(`^(localhost|127\.0\.0\.1|\[::1\])(:([0-9]+))?$`)

func mailLocalAuthority(host string) bool {
	parts := localMailAuthority.FindStringSubmatch(strings.ToLower(host))
	if parts == nil {
		return false
	}
	if parts[3] == "" {
		return true
	}
	port, err := strconv.ParseUint(parts[3], 10, 16)
	return err == nil && port > 0
}

func mailLocalRequest(r *http.Request) bool {
	if !IsLoopbackRemoteAddr(r.RemoteAddr) || !mailLocalAuthority(r.Host) {
		return false
	}
	for key, values := range r.Header {
		key = strings.ToLower(key)
		if strings.HasPrefix(key, "x-forwarded-") || key == "forwarded" || key == "x-real-ip" || key == "x-client-ip" || key == "cf-connecting-ip" || key == "true-client-ip" || key == "via" {
			for _, v := range values {
				if v != "" {
					return false
				}
			}
		}
	}
	if origins, ok := r.Header["Origin"]; ok {
		if len(origins) != 1 || !strings.HasPrefix(origins[0], "http://") || !mailLocalAuthority(strings.TrimPrefix(origins[0], "http://")) {
			return false
		}
	}
	for _, v := range r.Header.Values("Sec-Fetch-Site") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "cross-site") {
				return false
			}
		}
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if len(r.Header.Values("Content-Type")) != 1 {
		return false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

func RequireMailAccessMiddleware(authActive func() bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := MailSecurityModeAuthenticated
		if strings.TrimSpace(c.GetString("authSubject")) == "" {
			status := http.StatusForbidden
			if authActive != nil && authActive() {
				status = http.StatusUnauthorized
			} else if mailLocalRequest(c.Request) {
				status = 0
			}
			if status != 0 {
				c.AbortWithStatusJSON(status, MailErrorResponse{Code: ErrCodeMailAuthRequired, Error: "marketing mail requires auth.enabled=true or direct local loopback access"})
				return
			}
			mode = MailSecurityModeLocalLoopbackOnly
		}
		c.Header(MailAuthModeHeader, string(mode))
		c.Next()
	}
}

func MailRecovery(c *gin.Context) {
	defer func() {
		if recover() != nil {
			c.AbortWithStatusJSON(500, MailErrorResponse{Code: ErrCodeMailInternal, Error: "internal error"})
		}
	}()
	c.Next()
}
