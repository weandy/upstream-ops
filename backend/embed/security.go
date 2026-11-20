// Package embed 提供零 JS 纯服务端渲染的外部可嵌入性能看板（/embed）与公开只读状态 API。
package embed

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// PelicanSandboxCSP 单张 AI 生成 SVG 端点的严格沙箱 CSP（与外层 <iframe sandbox=""> 形成双层隔离）。
	PelicanSandboxCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'; sandbox"
)

var (
	// hostSourcePattern 允许标准 Origin 或带子域通配符的 CSP host-source（如 https://*.example.com:8443）。
	hostSourcePattern = regexp.MustCompile(`^https?://(\*\.)?[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)*(:\d{1,5})?$`)
)

// SanitizeOriginList 校验并去重规范化 Origin 白名单列表，拒绝任何含空白/分号/控制字符的非法输入以防头注入。
func SanitizeOriginList(raw []string, allowWildcard bool) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		if s == "*" && allowWildcard {
			if _, ok := seen["*"]; !ok {
				seen["*"] = struct{}{}
				out = append(out, "*")
			}
			continue
		}
		norm, err := normalizeOrigin(s, allowWildcard)
		if err != nil {
			return nil, fmt.Errorf("invalid origin %q: %w", item, err)
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out, nil
}

func normalizeOrigin(raw string, allowSubdomainWildcard bool) (string, error) {
	if strings.ContainsAny(raw, " \t\r\n;'\"") {
		return "", errors.New("contains forbidden characters")
	}
	trimmed := strings.TrimRight(raw, "/")
	if allowSubdomainWildcard && hostSourcePattern.MatchString(trimmed) {
		return strings.ToLower(trimmed), nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("scheme must be http or https")
	}
	if u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("origin must only contain scheme, host and optional port")
	}
	if !allowSubdomainWildcard && strings.Contains(u.Host, "*") {
		return "", errors.New("wildcard not allowed here")
	}
	if !hostSourcePattern.MatchString(scheme + "://" + u.Host) {
		return "", errors.New("malformed host")
	}
	return strings.ToLower(scheme + "://" + u.Host), nil
}

// BuildEmbedCSP 生成严格零脚本 + 动态 frame-ancestors 白名单的 Content-Security-Policy。
func BuildEmbedCSP(allowedOrigins []string) string {
	ancestors := []string{"'self'"}
	seen := map[string]struct{}{"'self'": {}}
	for _, origin := range allowedOrigins {
		norm, err := normalizeOrigin(strings.TrimSpace(origin), true)
		if err != nil {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		ancestors = append(ancestors, norm)
	}
	return "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors " + strings.Join(ancestors, " ")
}

// ApplyEmbedHeaders 为 /embed SSR 页面统一写入防缓存、防嗅探与动态 CSP 响应头。
func ApplyEmbedHeaders(c *gin.Context, allowedOrigins []string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", BuildEmbedCSP(allowedOrigins))
}

// ApplyPelicanSandboxHeaders 为单张 Pelican SVG 隔离端点写入最高级别沙箱头。
func ApplyPelicanSandboxHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", PelicanSandboxCSP)
}

// MatchCORSOrigin 判断请求 Origin 是否命中公开接口允许的 CORS 列表。
func MatchCORSOrigin(reqOrigin string, allowed []string) (string, bool) {
	req := strings.ToLower(strings.TrimRight(strings.TrimSpace(reqOrigin), "/"))
	if req == "" || len(allowed) == 0 {
		return "", false
	}
	hasStar := false
	for _, item := range allowed {
		s := strings.TrimSpace(item)
		if s == "*" {
			hasStar = true
			continue
		}
		if strings.ToLower(strings.TrimRight(s, "/")) == req {
			return reqOrigin, true
		}
	}
	if hasStar {
		return "*", true
	}
	return "", false
}

// ApplyCORSHeaders 为公开只读 JSON API 写入精确的 CORS 响应头。
func ApplyCORSHeaders(c *gin.Context, allowed []string) {
	c.Header("Vary", "Origin")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	origin := c.GetHeader("Origin")
	if matched, ok := MatchCORSOrigin(origin, allowed); ok {
		c.Header("Access-Control-Allow-Origin", matched)
		c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Accept, Content-Type")
		c.Header("Access-Control-Max-Age", "600")
	}
}
