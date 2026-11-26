package gateway

import (
	"testing"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

func TestCheckIPAccessNormalizedAndFailClosed(t *testing.T) {
	svc := &Service{}

	// 1. 空规则直接放行。
	for _, empty := range []string{"", "[]", "null", "  "} {
		if err := svc.checkIPAccess("198.51.100.1", empty, empty); err != nil {
			t.Fatalf("empty rule %q unexpected error: %v", empty, err)
		}
	}

	// 2. IPv4-mapped IPv6 与 IPv6 缩写归一化匹配。
	wl := `["203.0.113.0/24","2001:db8::1"]`
	if err := svc.checkIPAccess("::ffff:203.0.113.42", wl, ""); err != nil {
		t.Fatalf("IPv4-mapped client IP should match IPv4 CIDR: %v", err)
	}
	if err := svc.checkIPAccess("2001:0db8:0000:0000:0000:0000:0000:0001", wl, ""); err != nil {
		t.Fatalf("expanded IPv6 should match compressed rule: %v", err)
	}
	if err := svc.checkIPAccess("198.51.100.2", wl, ""); err == nil {
		t.Fatalf("non-whitelisted IP must be denied")
	}

	// 3. 黑名单优先于白名单。
	bl := `["203.0.113.42","::ffff:203.0.113.99"]`
	if err := svc.checkIPAccess("203.0.113.42", wl, bl); err == nil || err.Error() != "ip blocked" {
		t.Fatalf("expected ip blocked, got %v", err)
	}
	if err := svc.checkIPAccess("203.0.113.99", wl, bl); err == nil || err.Error() != "ip blocked" {
		t.Fatalf("expected IPv4-mapped blacklist hit, got %v", err)
	}

	// 4. 非法规则必须 fail-closed（绝不能回退为空列表放行）。
	for _, bad := range []string{"{not-json", `["not-an-ip"]`, `["10.0.0.0/99"]`, `["fe80::1%eth0"]`} {
		if err := svc.checkIPAccess("203.0.113.42", bad, ""); err == nil {
			t.Fatalf("malformed whitelist %q must fail closed", bad)
		}
		if err := svc.checkIPAccess("203.0.113.42", "", bad); err == nil {
			t.Fatalf("malformed blacklist %q must fail closed", bad)
		}
	}
}

func TestAdminCreateAndUpdateKeyValidatesIPRules(t *testing.T) {
	db := openGatewayTestDB(t)
	groupsRepo := storage.NewGatewayGroups(db)
	keysRepo := storage.NewGatewayKeys(db)
	routesRepo := storage.NewGatewayRoutes(db)
	cipher, err := crypto.NewCipher("test-secret-ip-rules")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	g := &storage.GatewayGroup{Name: "ip-check-group", Status: storage.GatewayGroupStatusActive}
	if err := groupsRepo.Create(g); err != nil {
		t.Fatalf("create group: %v", err)
	}
	admin := NewService(groupsRepo, keysRepo, routesRepo, nil, nil, nil, nil, cipher, nil)

	if _, err := admin.CreateKey(CreateKeyInput{
		GroupID:         g.ID,
		Name:            "bad-wl",
		IPWhitelistJSON: `["not-an-ip"]`,
	}); err == nil {
		t.Fatalf("CreateKey should reject invalid IPWhitelistJSON")
	}
	if _, err := admin.CreateKey(CreateKeyInput{
		GroupID:         g.ID,
		Name:            "bad-bl",
		IPBlacklistJSON: `{broken`,
	}); err == nil {
		t.Fatalf("CreateKey should reject invalid IPBlacklistJSON")
	}

	created, err := admin.CreateKey(CreateKeyInput{
		GroupID:         g.ID,
		Name:            "norm-key",
		IPWhitelistJSON: `[" ::ffff:192.0.2.5 ", "2001:0db8::1", "192.0.2.5"]`,
	})
	if err != nil {
		t.Fatalf("CreateKey valid rules: %v", err)
	}
	wantNorm := `["192.0.2.5","2001:db8::1"]`
	if created.Key.IPWhitelistJSON != wantNorm {
		t.Fatalf("normalized IPWhitelistJSON = %q, want %q", created.Key.IPWhitelistJSON, wantNorm)
	}

	bad := `["256.1.1.1"]`
	if _, err := admin.UpdateKey(created.Key.ID, UpdateKeyInput{IPBlacklistJSON: &bad}); err == nil {
		t.Fatalf("UpdateKey should reject invalid IPBlacklistJSON")
	}
	emptyArr := `[]`
	updated, err := admin.UpdateKey(created.Key.ID, UpdateKeyInput{IPWhitelistJSON: &emptyArr})
	if err != nil {
		t.Fatalf("UpdateKey clear whitelist: %v", err)
	}
	if updated.IPWhitelistJSON != "" {
		t.Fatalf("expected empty normalized rule, got %q", updated.IPWhitelistJSON)
	}
}
