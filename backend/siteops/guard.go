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

func IsPollingAllowed(authActive bool, cfg config.SiteOpsConfig) bool {
	return authActive || cfg.AllowUnauthenticatedLocalOnly
}

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
	origins map[origin]struct{}
	cidrs   []netip.Prefix
}

type OutboundGuard struct {
	mu       sync.RWMutex
	policy   outboundPolicy
	lookupIP func(context.Context, string, string) ([]netip.Addr, error)
	dial     net.Dialer
}

var _ OutboundValidator = (*OutboundGuard)(nil)

func NewOutboundGuard(allowedOrigins, allowedCIDRs []string) *OutboundGuard {
	g := &OutboundGuard{lookupIP: net.DefaultResolver.LookupNetIP}
	g.UpdatePolicy(allowedOrigins, allowedCIDRs)
	return g
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

func (g *OutboundGuard) UpdatePolicy(allowedOrigins, allowedCIDRs []string) {
	policy := outboundPolicy{origins: make(map[origin]struct{}, len(allowedOrigins))}
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

func (p outboundPolicy) allowsIP(ip netip.Addr) bool {
	for _, prefix := range p.cidrs {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (g *OutboundGuard) ValidateBaseURL(rawURL string) (string, error) {
	u, key, err := parseBaseURL(rawURL)
	if err != nil {
		return "", err
	}
	p := g.currentPolicy()
	_, allowedOrigin := p.origins[key]
	ip, _ := netip.ParseAddr(key.host)
	if !allowedOrigin && !p.allowsIP(ip) {
		return "", ErrOriginNotAllowed
	}
	return strings.TrimRight(u.String(), "/"), nil
}

var restrictedDialRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
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

func publicDialIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, prefix := range restrictedDialRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func (g *OutboundGuard) resolveTarget(ctx context.Context, network, addr string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrOriginNotAllowed
	}
	host = strings.ToLower(host)
	ip, literalErr := netip.ParseAddr(host)
	if literalErr == nil {
		if ip.Zone() != "" {
			return nil, ErrOriginNotAllowed
		}
		host = ip.Unmap().String()
	}
	p := g.currentPolicy()
	_, httpAllowed := p.origins[origin{"http", host, port}]
	_, httpsAllowed := p.origins[origin{"https", host, port}]
	originAllowed := httpAllowed || httpsAllowed
	if literalErr != nil && !originAllowed {
		return nil, ErrOriginNotAllowed
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
		if p.allowsIP(candidate) || (originAllowed && (literalErr == nil || publicDialIP(candidate))) {
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
