package siteops

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

func IsLoopbackRemoteAddr(remoteAddr string) bool {
	host, port, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	_, err = strconv.ParseUint(port, 10, 16)
	return err == nil && (host == "127.0.0.1" || host == "::1")
}

// IsPollingAllowed P0–P2 阶段不要求强开 auth.enabled，跟随站点实例自身的 enabled 状态运行。
func IsPollingAllowed(_ bool, _ config.SiteOpsConfig) bool {
	return true
}

// RequireAccessMiddleware 保留供 P3 高危写操作端点复用（要求 auth.enabled=true）。
func RequireAccessMiddleware(authActive func() bool, cfgProvider func() config.SiteOpsConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if (authActive != nil && authActive()) || (cfgProvider != nil && cfgProvider().AllowUnauthenticatedLocalOnly &&
			(c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) &&
			IsLoopbackRemoteAddr(c.Request.RemoteAddr)) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": ErrCodeAuthRequired, "error": ErrAuthRequired.Error()})
	}
}

type origin struct {
	scheme string
	host   string
	port   string
}

type outboundPolicy struct {
	allowLoopback bool
	origins       map[origin]struct{}
	cidrs         []netip.Prefix
}

type OutboundGuard struct {
	mu       sync.RWMutex
	policy   outboundPolicy
	lookupIP func(context.Context, string, string) ([]netip.Addr, error)
	dial     net.Dialer
}

var _ OutboundValidator = (*OutboundGuard)(nil)

func NewOutboundGuard(allowLoopback bool, allowedOrigins, allowedCIDRs []string) *OutboundGuard {
	g := &OutboundGuard{lookupIP: net.DefaultResolver.LookupNetIP}
	g.UpdatePolicy(allowLoopback, allowedOrigins, allowedCIDRs)
	return g
}

func isLocalhostName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func parseBaseURL(raw string) (*url.URL, origin, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.HasSuffix(u.Host, ":") || strings.Contains(u.Hostname(), "%") {
		return nil, origin{}, ErrOriginNotAllowed
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.Unmap().String()
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	} else {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return nil, origin{}, ErrOriginNotAllowed
		}
		port = strconv.FormatUint(value, 10)
	}
	if u.Port() != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return u, origin{u.Scheme, host, port}, nil
}

func (g *OutboundGuard) UpdatePolicy(allowLoopback bool, allowedOrigins, allowedCIDRs []string) {
	policy := outboundPolicy{
		allowLoopback: allowLoopback,
		origins:       make(map[origin]struct{}, len(allowedOrigins)),
	}
	for _, raw := range allowedOrigins {
		if _, key, err := parseBaseURL(raw); err == nil {
			policy.origins[key] = struct{}{}
		}
	}
	for _, raw := range allowedCIDRs {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
			}
			policy.cidrs = append(policy.cidrs, prefix.Masked())
		}
	}
	g.mu.Lock()
	g.policy = policy
	g.mu.Unlock()
}

func (g *OutboundGuard) currentPolicy() outboundPolicy {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.policy
}

func (p outboundPolicy) allowsCIDR(ip netip.Addr) bool {
	for _, prefix := range p.cidrs {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// hardBlockedRanges 永久硬拦截地址段：云元数据、链路本地、未指定与受限广播，任何配置均不可豁免。
var hardBlockedRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

// restrictedDialRanges 默认拦截的非公网/保留地址段（可通过显式 allowedOrigins / allowedCIDRs 定向豁免）。
var restrictedDialRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

func hardBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range hardBlockedRanges {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func publicDialIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if hardBlockedIP(ip) || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range restrictedDialRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (p outboundPolicy) allowsDialIP(ip netip.Addr, isLoopbackHost, originAllowed bool) bool {
	ip = ip.Unmap()
	if hardBlockedIP(ip) {
		return false
	}
	if publicDialIP(ip) {
		return true
	}
	if ip.IsLoopback() && p.allowLoopback && isLoopbackHost {
		return true
	}
	return originAllowed || p.allowsCIDR(ip)
}

func (g *OutboundGuard) ValidateBaseURL(rawURL string) (string, error) {
	u, key, err := parseBaseURL(rawURL)
	if err != nil {
		return "", err
	}
	p := g.currentPolicy()
	_, originAllowed := p.origins[key]
	if ip, ipErr := netip.ParseAddr(key.host); ipErr == nil {
		ip = ip.Unmap()
		if !p.allowsDialIP(ip, ip.IsLoopback(), originAllowed) {
			return "", ErrOriginNotAllowed
		}
		return strings.TrimRight(u.String(), "/"), nil
	}
	if isLocalhostName(key.host) {
		if !p.allowLoopback && !originAllowed {
			return "", ErrOriginNotAllowed
		}
		return strings.TrimRight(u.String(), "/"), nil
	}
	// 无点号的单标签内部主机名（如 sub2api、metadata）默认视为内网名，需显式配置 allowedOrigins 或 allowedCIDRs。
	if !strings.Contains(strings.TrimSuffix(key.host, "."), ".") && !originAllowed && len(p.cidrs) == 0 {
		return "", ErrOriginNotAllowed
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (g *OutboundGuard) resolveTarget(ctx context.Context, network, addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrOriginNotAllowed
	}
	host = strings.ToLower(host)
	ip, literalErr := netip.ParseAddr(host)
	isLoopbackHost := false
	if literalErr == nil {
		if ip.Zone() != "" {
			return nil, ErrOriginNotAllowed
		}
		ip = ip.Unmap()
		host = ip.String()
		isLoopbackHost = ip.IsLoopback()
	} else {
		isLoopbackHost = isLocalhostName(host)
	}
	p := g.currentPolicy()
	_, httpAllowed := p.origins[origin{"http", host, port}]
	_, httpsAllowed := p.origins[origin{"https", host, port}]
	originAllowed := httpAllowed || httpsAllowed
	if literalErr != nil {
		if isLoopbackHost && !p.allowLoopback && !originAllowed {
			return nil, ErrOriginNotAllowed
		}
		if !isLoopbackHost && !strings.Contains(strings.TrimSuffix(host, "."), ".") && !originAllowed && len(p.cidrs) == 0 {
			return nil, ErrOriginNotAllowed
		}
	}
	resolved := []netip.Addr{ip}
	if literalErr != nil {
		resolved, err = g.lookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	var targets []string
	for _, candidate := range resolved {
		candidate = candidate.Unmap()
		if (network == "tcp4" && !candidate.Is4()) || (network == "tcp6" && !candidate.Is6()) {
			continue
		}
		if p.allowsDialIP(candidate, isLoopbackHost, originAllowed) {
			targets = append(targets, net.JoinHostPort(candidate.String(), port))
		}
	}
	if len(targets) == 0 {
		return nil, ErrOriginNotAllowed
	}
	return targets, nil
}

func (g *OutboundGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	targets, err := g.resolveTarget(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		var conn net.Conn
		conn, err = g.dial.DialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}

func validMailHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (g *OutboundGuard) ValidateSMTPHostPort(host string, port int) error {
	if port < 1 || port > 65535 {
		return ErrMailSMTPConfigBlocked
	}
	p := g.currentPolicy()
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() == "" && p.allowsDialIP(ip, ip.Unmap().IsLoopback(), false) {
			return nil
		}
		return ErrMailSMTPConfigBlocked
	}
	if !validMailHostname(host) || (isLocalhostName(host) && !p.allowLoopback) || (!strings.Contains(host, ".") && len(p.cidrs) == 0 && !(p.allowLoopback && isLocalhostName(host))) {
		return ErrMailSMTPConfigBlocked
	}
	return nil
}

func (g *OutboundGuard) DialSMTPContext(ctx context.Context, host string, port int) (net.Conn, error) {
	if err := g.ValidateSMTPHostPort(host, port); err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	addresses := []netip.Addr{ip}
	local := ip.IsValid() && ip.Unmap().IsLoopback() || isLocalhostName(host)
	if err != nil {
		addresses, err = g.lookupIP(ctx, "ip", host)
		if err != nil {
			return nil, ErrMailSMTPUnavailable
		}
	}
	p := g.currentPolicy()
	var last error = ErrMailSMTPConfigBlocked
	for _, candidate := range addresses {
		if candidate.Zone() != "" || !p.allowsDialIP(candidate, local, false) {
			continue
		}
		conn, err := g.dial.DialContext(ctx, "tcp", net.JoinHostPort(candidate.Unmap().String(), strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		last = ErrMailSMTPUnavailable
	}
	return nil, last
}
