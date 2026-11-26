// 密钥 IP 黑白名单校验。
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

func (svc *Service) checkIPAccess(ip, whitelistJSON, blacklistJSON string) error {
	blacklist, err := parseIPRules(blacklistJSON)
	if err != nil {
		return errors.New("ip access rule invalid")
	}
	whitelist, err := parseIPRules(whitelistJSON)
	if err != nil {
		return errors.New("ip access rule invalid")
	}
	if len(blacklist) == 0 && len(whitelist) == 0 {
		return nil
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || addr.Zone() != "" {
		return errors.New("ip not allowed")
	}
	addr = addr.Unmap()
	if matchIPPrefixes(addr, blacklist) {
		return errors.New("ip blocked")
	}
	if len(whitelist) > 0 && !matchIPPrefixes(addr, whitelist) {
		return errors.New("ip not allowed")
	}
	return nil
}

// ValidateIPRuleListJSON 校验并规范化 IP 白/黑名单 JSON 字符串数组（支持 IPv4/IPv6 与 CIDR）。
func ValidateIPRuleListJSON(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return "", nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return "", errors.New("ip rule list must be a JSON string array")
	}
	seen := make(map[string]struct{}, len(list))
	normalized := make([]string, 0, len(list))
	for _, item := range list {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		_, norm, err := parseIPRule(s)
		if err != nil {
			return "", err
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		normalized = append(normalized, norm)
	}
	if len(normalized) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func parseIPRules(raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, errors.New("ip rule list must be a JSON string array")
	}
	out := make([]netip.Prefix, 0, len(list))
	for _, item := range list {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		prefix, _, err := parseIPRule(s)
		if err != nil {
			return nil, err
		}
		out = append(out, prefix)
	}
	return out, nil
}

func parseIPRule(item string) (netip.Prefix, string, error) {
	if strings.Contains(item, "/") {
		prefix, err := netip.ParsePrefix(item)
		if err != nil || prefix.Addr().Zone() != "" {
			return netip.Prefix{}, "", fmt.Errorf("invalid CIDR %q", item)
		}
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefix = prefix.Masked()
		return prefix, prefix.String(), nil
	}
	addr, err := netip.ParseAddr(item)
	if err != nil || addr.Zone() != "" {
		return netip.Prefix{}, "", fmt.Errorf("invalid IP address %q", item)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), addr.String(), nil
}

func matchIPPrefixes(addr netip.Addr, rules []netip.Prefix) bool {
	for _, prefix := range rules {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
