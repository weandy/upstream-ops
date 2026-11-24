package siteops

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

// NormalizeBareEmail preserves the envelope local part. Only the HMAC folds it.
func NormalizeBareEmail(raw string) (string, error) {
	local, domain, ok := strings.Cut(raw, "@")
	if !ok || len(raw) > 254 || len(local) == 0 || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") || !strings.Contains(domain, ".") || !validMailHostname(domain) {
		return "", ErrMailInvalidInput
	}
	for _, r := range local {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-/=?^_\x60{|}~.", r)) {
			return "", ErrMailInvalidInput
		}
	}
	email := local + "@" + strings.ToLower(domain)
	mask := MaskEmailStrict(email)
	if mask == nil || strings.EqualFold(*mask, email) || len(*mask) > 160 {
		return "", ErrMailInvalidInput
	}
	return email, nil
}

func MailEmailHash(seed []byte, email string) (string, error) {
	email, err := NormalizeBareEmail(email)
	if err != nil {
		return "", err
	}
	if len(seed) != 32 {
		return "", ErrMailCryptoUnavailable
	}
	h := hmac.New(sha256.New, seed)
	h.Write([]byte("mail-addr-v1\x00" + strings.ToLower(email)))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Bindings come from trusted rows. Credentials and item addresses never form part of an error.
type mailBinding struct {
	V             int    `json:"v"`
	Purpose       string `json:"purpose"`
	InstanceID    uint   `json:"instance_id"`
	TargetBinding string `json:"target_binding"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Revision      int64  `json:"revision,omitempty"`
	BatchID       uint   `json:"batch_id,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	EmailHash     string `json:"email_hash,omitempty"`
}
type sealedMailObject struct {
	mailBinding
	Seed      []byte            `json:"seed_b64,omitempty"`
	Username  string            `json:"username,omitempty"`
	FromEmail string            `json:"from_email,omitempty"`
	Password  string            `json:"password,omitempty"`
	Email     string            `json:"email,omitempty"`
	Vars      *MailPersonalVars `json:"vars,omitempty"`
}

func scopeBinding(s storage.MailScopeKey, purpose string) mailBinding {
	return mailBinding{V: 1, Purpose: purpose, InstanceID: s.InstanceID, TargetBinding: s.TargetBinding}
}
func smtpBinding(row storage.SiteOpsMailSMTP, purpose string) mailBinding {
	return mailBinding{V: 1, Purpose: purpose, InstanceID: row.InstanceID, TargetBinding: row.TargetBinding, Host: row.Host, Port: row.Port, Revision: row.Revision}
}
func itemBinding(s storage.MailScopeKey, batch uint, user int64, hash string) mailBinding {
	b := scopeBinding(s, "mail_item_payload")
	b.BatchID, b.UserID, b.EmailHash = batch, user, hash
	return b
}
func sealMail(c *crypto.Cipher, b mailBinding, object sealedMailObject) (string, error) {
	if c == nil || b.InstanceID == 0 || b.TargetBinding == "" {
		return "", ErrMailCryptoUnavailable
	}
	object.mailBinding = b
	raw, err := json.Marshal(object)
	if err != nil {
		return "", ErrMailCryptoUnavailable
	}
	out, err := c.Encrypt(string(raw))
	if err != nil {
		return "", ErrMailCryptoUnavailable
	}
	return out, nil
}
func openMail(c *crypto.Cipher, b mailBinding, ct string) (sealedMailObject, error) {
	var zero sealedMailObject
	if c == nil || ct == "" || b.InstanceID == 0 || b.TargetBinding == "" {
		return zero, ErrMailCryptoUnavailable
	}
	raw, err := c.Decrypt(ct)
	var env sealedMailObject
	if err != nil || json.Unmarshal([]byte(raw), &env) != nil || env.mailBinding != b {
		return zero, ErrMailCryptoUnavailable
	}
	return env, nil
}
func NewMailScopeSeed(c *crypto.Cipher, s storage.MailScopeKey) (string, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return "", ErrMailCryptoUnavailable
	}
	return sealMail(c, scopeBinding(s, "mail_scope_seed"), sealedMailObject{Seed: seed})
}
func OpenMailScopeSeed(c *crypto.Cipher, s storage.MailScopeKey, ct string) ([]byte, error) {
	env, err := openMail(c, scopeBinding(s, "mail_scope_seed"), ct)
	if err != nil || len(env.Seed) != 32 {
		return nil, ErrMailCryptoUnavailable
	}
	return env.Seed, nil
}

type MailSMTPIdentity struct{ Username, FromEmail string }

func SealMailSMTPIdentity(c *crypto.Cipher, row storage.SiteOpsMailSMTP, id MailSMTPIdentity) (string, error) {
	var err error
	id.FromEmail, err = NormalizeBareEmail(id.FromEmail)
	if err != nil {
		return "", err
	}
	return sealMail(c, smtpBinding(row, "mail_smtp_identity"), sealedMailObject{Username: id.Username, FromEmail: id.FromEmail})
}
func OpenMailSMTPIdentity(c *crypto.Cipher, row storage.SiteOpsMailSMTP) (MailSMTPIdentity, error) {
	env, err := openMail(c, smtpBinding(row, "mail_smtp_identity"), row.IdentityCipher)
	if _, e := NormalizeBareEmail(env.FromEmail); err != nil || e != nil {
		return MailSMTPIdentity{}, ErrMailCryptoUnavailable
	}
	return MailSMTPIdentity{env.Username, env.FromEmail}, nil
}
func SealMailSMTPPassword(c *crypto.Cipher, row storage.SiteOpsMailSMTP, username, password string) (string, error) {
	b := smtpBinding(row, "mail_smtp_password")
	return sealMail(c, b, sealedMailObject{Username: username, Password: password})
}
func OpenMailSMTPPassword(c *crypto.Cipher, row storage.SiteOpsMailSMTP, username string) (string, error) {
	b := smtpBinding(row, "mail_smtp_password")
	env, err := openMail(c, b, row.PasswordCipher)
	if err != nil || env.Username != username {
		return "", ErrMailCryptoUnavailable
	}
	return env.Password, nil
}

type MailPersonalVars struct {
	BalanceUSD    string `json:"balance_usd,omitempty"`
	PlanName      string `json:"plan_name,omitempty"`
	ExpiresDate   string `json:"expires_date,omitempty"`
	ExpiresInDays string `json:"expires_in_days,omitempty"`
}
type MailItemPayload struct {
	Email string
	Vars  MailPersonalVars
}

func ProjectMailItem(c *crypto.Cipher, s storage.MailScopeKey, seed []byte, batch uint, user int64, email string, vars MailPersonalVars) (MailAddressProjection, error) {
	addr, err := NormalizeBareEmail(email)
	if err != nil {
		return MailAddressProjection{}, err
	}
	hash, err := MailEmailHash(seed, addr)
	if err != nil {
		return MailAddressProjection{}, err
	}
	out := MailAddressProjection{EmailHash: hash, EmailMasked: *MaskEmailStrict(addr)}
	if batch != 0 {
		out.PayloadCipher, err = sealMail(c, itemBinding(s, batch, user, hash), sealedMailObject{Email: addr, Vars: &vars})
	}
	return out, err
}
func OpenMailItem(c *crypto.Cipher, s storage.MailScopeKey, seed []byte, row storage.SiteOpsMailItem) (MailItemPayload, error) {
	if row.PayloadCipher == nil {
		return MailItemPayload{}, ErrMailCryptoUnavailable
	}
	env, err := openMail(c, itemBinding(s, row.BatchID, row.UserID, row.EmailHash), *row.PayloadCipher)
	if err != nil || env.Vars == nil {
		return MailItemPayload{}, ErrMailCryptoUnavailable
	}
	payload := MailItemPayload{env.Email, *env.Vars}
	hash, err := MailEmailHash(seed, payload.Email)
	if err != nil || !hmac.Equal([]byte(hash), []byte(row.EmailHash)) {
		return MailItemPayload{}, ErrMailCryptoUnavailable
	}
	return payload, nil
}
