package siteops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

func TestMailAddressAndScopeBinding(t *testing.T) {
	for _, s := range []string{" Jane@example.com", "Jane <a@example.com>", "a,b@example.com", "a..b@example.com", "a@example", "x@y..com", "x@y-.com", "\r\na@example.com", strings.Repeat("x", 65) + "@example.com"} {
		if _, err := NormalizeBareEmail(s); err == nil {
			t.Fatal("accepted invalid address")
		}
	}
	const addr = "Some.One+Tag@example.com"
	if v, e := NormalizeBareEmail("Some.One+Tag@EXAMPLE.COM"); e != nil || v != addr {
		t.Fatal("envelope local part")
	}
	c, _ := crypto.NewCipher(t.Name())
	s := storage.MailScopeKey{InstanceID: 1, TargetBinding: "scope-A"}
	scoped, e := NewMailScopeSeed(c, s)
	if e != nil {
		t.Fatal(e)
	}
	seed, e := OpenMailScopeSeed(c, s, scoped)
	if e != nil || len(seed) != 32 {
		t.Fatal(e)
	}
	seed2, _ := NewMailScopeSeed(c, s)
	other, _ := OpenMailScopeSeed(c, s, seed2)
	if bytes.Equal(seed, other) {
		t.Fatal("seed reused")
	}
	p, e := ProjectMailItem(c, s, seed, 3, 17, addr, MailPersonalVars{PlanName: "plan"})
	if e != nil {
		t.Fatal(e)
	}
	same, _ := MailEmailHash(seed, strings.ToUpper(addr))
	if same != p.EmailHash || strings.Contains(p.PayloadCipher, addr) || p.EmailMasked == addr {
		t.Fatal("unsafe projection")
	}
	row := storage.SiteOpsMailItem{BatchID: 3, UserID: 17, EmailHash: p.EmailHash, PayloadCipher: &p.PayloadCipher}
	v, e := OpenMailItem(c, s, seed, row)
	if e != nil || v.Email != addr || v.Vars.PlanName != "plan" {
		t.Fatal(e)
	}
	if _, e = OpenMailItem(c, s, other, row); e != ErrMailCryptoUnavailable {
		t.Fatal("seed mismatch")
	}
	row.UserID++
	if _, e = OpenMailItem(c, s, seed, row); e != ErrMailCryptoUnavailable {
		t.Fatal("item swap")
	}
	s.TargetBinding = "B"
	if _, e = OpenMailScopeSeed(c, s, scoped); e != ErrMailCryptoUnavailable {
		t.Fatal("scope swap")
	}
}
func TestMailSMTPSecretBinding(t *testing.T) {
	c, _ := crypto.NewCipher(t.Name())
	row := storage.SiteOpsMailSMTP{InstanceID: 1, TargetBinding: "a", Host: "smtp.example.com", Port: 465, Revision: 2}
	id := MailSMTPIdentity{"sender", "sender@example.com"}
	row.IdentityCipher, _ = SealMailSMTPIdentity(c, row, id)
	row.PasswordCipher, _ = SealMailSMTPPassword(c, row, id.Username, "fixture")
	if v, e := OpenMailSMTPIdentity(c, row); e != nil || v != id {
		t.Fatal(e)
	}
	if v, e := OpenMailSMTPPassword(c, row, id.Username); e != nil || v != "fixture" {
		t.Fatal(e)
	}
	if _, e := OpenMailSMTPPassword(c, row, "other"); e == nil {
		t.Fatal("username mismatch")
	}
	for _, change := range []func(*storage.SiteOpsMailSMTP){func(r *storage.SiteOpsMailSMTP) { r.InstanceID++ }, func(r *storage.SiteOpsMailSMTP) { r.TargetBinding += "b" }, func(r *storage.SiteOpsMailSMTP) { r.Host += "x" }, func(r *storage.SiteOpsMailSMTP) { r.Port++ }, func(r *storage.SiteOpsMailSMTP) { r.Revision++ }} {
		wrong := row
		change(&wrong)
		if _, e := OpenMailSMTPIdentity(c, wrong); e == nil {
			t.Fatal("identity context mismatch")
		}
		if _, e := OpenMailSMTPPassword(c, wrong, id.Username); e == nil {
			t.Fatal("password context mismatch")
		}
	}
}
