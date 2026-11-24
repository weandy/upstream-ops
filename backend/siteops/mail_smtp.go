package siteops

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"time"
)

var smtpConnectionSlots = make(chan struct{}, MailMaxProcessConnections)

type smtpSender struct {
	guard SMTPOutboundValidator
	roots *x509.CertPool
}

func NewMailSMTPSender(g SMTPOutboundValidator) MailSMTPSender { return &smtpSender{guard: g} }

type smtpSession struct {
	client *smtp.Client
	conn   net.Conn
	once   sync.Once
}

func (s *smtpSession) Close() error {
	var err error
	s.once.Do(func() { err = s.conn.Close(); <-smtpConnectionSlots })
	return err
}

func smtpFailure(stage MailSMTPStage, err error) *MailSMTPFailure {
	f := &MailSMTPFailure{Stage: stage, Code: ErrCodeMailSMTPUnavailable}
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code >= 400 && reply.Code < 600 {
		f.SMTPCode = reply.Code
		f.Code = ErrCodeMailSMTPRejected
	}
	switch stage {
	case MailSMTPStageConfig, MailSMTPStageGuard, MailSMTPStageAuth, MailSMTPStageTLS:
		f.Code = ErrCodeMailSMTPConfigBlocked
	case MailSMTPStageDataWrite, MailSMTPStageDataCommit:
		if f.SMTPCode == 0 {
			f.Code = ErrCodeMailDeliveryUncertain
		}
	}
	return f
}
func ClassifyMailSMTPFailure(err error, authorized bool) (MailItemStatus, MailSMTPFailure) {
	if err == nil && authorized {
		return MailItemStatusSent, MailSMTPFailure{Stage: MailSMTPStageDataCommit, SMTPCode: 250}
	}
	var f *MailSMTPFailure
	if !errors.As(err, &f) {
		if authorized {
			f = smtpFailure(MailSMTPStageDataCommit, err)
		} else {
			f = smtpFailure(MailSMTPStageConfig, err)
		}
	}
	if authorized && f.SMTPCode == 0 && (f.Stage == MailSMTPStageDataWrite || f.Stage == MailSMTPStageDataCommit) {
		return MailItemStatusUncertain, *f
	}
	return MailItemStatusFailed, *f
}

type loginAuth struct {
	host, user, password string
	step                 int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS || server.Name != a.host {
		return "", nil, ErrMailSMTPConfigBlocked
	}
	a.step = 0
	return "LOGIN", nil, nil
}
func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		if a.step != 2 {
			return nil, ErrMailSMTPConfigBlocked
		}
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(challenge)))
	if a.step == 0 && prompt == "username:" {
		a.step++
		return []byte(a.user), nil
	}
	if a.step == 1 && prompt == "password:" {
		a.step++
		return []byte(a.password), nil
	}
	return nil, ErrMailSMTPConfigBlocked
}
func smtpAuth(client *smtp.Client, cfg MailSMTPConnectionConfig) error {
	if cfg.AuthMode == MailAuthModeNone {
		return nil
	}
	ok, ext := client.Extension("AUTH")
	if !ok || cfg.Username == "" || cfg.Password == "" || strings.ContainsRune(cfg.Username+cfg.Password, '\x00') {
		return ErrMailSMTPConfigBlocked
	}
	modes := strings.Fields(strings.ToUpper(ext))
	if slices.Contains(modes, "PLAIN") && (cfg.AuthMode == MailAuthModePlain || cfg.AuthMode == MailAuthModeAuto) {
		return client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host))
	}
	if slices.Contains(modes, "LOGIN") && (cfg.AuthMode == MailAuthModeLogin || cfg.AuthMode == MailAuthModeAuto) {
		return client.Auth(&loginAuth{host: cfg.Host, user: cfg.Username, password: cfg.Password})
	}
	return ErrMailSMTPConfigBlocked
}
func (s *smtpSender) Connect(ctx context.Context, cfg MailSMTPConnectionConfig) (MailSMTPSession, error) {
	if s.guard == nil || s.guard.ValidateSMTPHostPort(cfg.Host, cfg.Port) != nil {
		return nil, smtpFailure(MailSMTPStageGuard, ErrMailSMTPConfigBlocked)
	}
	if cfg.TLSMode != MailTLSModeImplicit && cfg.TLSMode != MailTLSModeStarttls {
		return nil, smtpFailure(MailSMTPStageConfig, ErrMailSMTPConfigBlocked)
	}
	ctx, cancel := context.WithTimeout(ctx, MailDialTimeout)
	defer cancel()
	select {
	case smtpConnectionSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, smtpFailure(MailSMTPStageDial, ctx.Err())
	}
	conn, err := s.guard.DialSMTPContext(ctx, cfg.Host, cfg.Port)
	if err != nil {
		<-smtpConnectionSlots
		stage := MailSMTPStageDial
		if errors.Is(err, ErrMailSMTPConfigBlocked) {
			stage = MailSMTPStageGuard
		}
		return nil, smtpFailure(stage, err)
	}
	session := &smtpSession{conn: conn}
	good := false
	defer func() {
		if !good {
			session.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, smtpFailure(MailSMTPStageDial, err)
	}
	cfgTLS := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host, RootCAs: s.roots}
	if cfg.TLSMode == MailTLSModeImplicit {
		tc := tls.Client(conn, cfgTLS)
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, smtpFailure(MailSMTPStageTLS, err)
		}
		conn = tc
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return nil, smtpFailure(MailSMTPStageEHLO, err)
	}
	session.client = client
	if err = client.Hello("localhost"); err != nil {
		return nil, smtpFailure(MailSMTPStageEHLO, err)
	}
	if cfg.TLSMode == MailTLSModeStarttls {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return nil, smtpFailure(MailSMTPStageTLS, ErrMailSMTPConfigBlocked)
		}
		if err = client.StartTLS(cfgTLS); err != nil {
			return nil, smtpFailure(MailSMTPStageTLS, err)
		}
	}
	if state, ok := client.TLSConnectionState(); !ok || len(state.VerifiedChains) == 0 {
		return nil, smtpFailure(MailSMTPStageTLS, ErrMailSMTPConfigBlocked)
	}
	if err = smtpAuth(client, cfg); err != nil {
		return nil, smtpFailure(MailSMTPStageAuth, err)
	}
	if !stop() {
		return nil, smtpFailure(MailSMTPStageDial, ctx.Err())
	}
	good = true
	return session, nil
}
func (s *smtpSession) bound(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	end := time.Now().Add(MailSendTimeout)
	if t, ok := ctx.Deadline(); ok && t.Before(end) {
		end = t
	}
	if err := s.conn.SetDeadline(end); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	return func() { stop() }, nil
}
func (s *smtpSession) Envelope(ctx context.Context, from, to string) error {
	from, err := NormalizeBareEmail(from)
	if err != nil {
		return smtpFailure(MailSMTPStageConfig, err)
	}
	to, err = NormalizeBareEmail(to)
	if err != nil {
		return smtpFailure(MailSMTPStageConfig, err)
	}
	stop, err := s.bound(ctx)
	if err != nil {
		return smtpFailure(MailSMTPStageMailFrom, err)
	}
	defer stop()
	if err = s.client.Mail(from); err != nil {
		return smtpFailure(MailSMTPStageMailFrom, err)
	}
	if err = s.client.Rcpt(to); err != nil {
		return smtpFailure(MailSMTPStageRCPTTo, err)
	}
	return nil
}
func (s *smtpSession) SendData(ctx context.Context, msg []byte) error {
	if len(msg) == 0 || len(msg) > MailMaxMIMEBytes {
		return smtpFailure(MailSMTPStageRender, ErrMailTemplateInvalid)
	}
	stop, err := s.bound(ctx)
	if err != nil {
		return smtpFailure(MailSMTPStageDataCmd, err)
	}
	defer stop()
	tp := s.client.Text
	id, err := tp.Cmd("DATA")
	if err != nil {
		return smtpFailure(MailSMTPStageDataCmd, err)
	}
	tp.StartResponse(id)
	_, _, err = tp.ReadResponse(354)
	tp.EndResponse(id)
	if err != nil {
		return smtpFailure(MailSMTPStageDataCmd, err)
	}
	writer := tp.DotWriter()
	if _, err = writer.Write(msg); err != nil {
		return smtpFailure(MailSMTPStageDataWrite, err)
	}
	if err = writer.Close(); err != nil {
		return smtpFailure(MailSMTPStageDataCommit, err)
	}
	if _, _, err = tp.ReadResponse(250); err != nil {
		return smtpFailure(MailSMTPStageDataCommit, err)
	}
	return nil
}
func (s *smtpSession) Reset(ctx context.Context) error {
	stop, err := s.bound(ctx)
	if err != nil {
		return smtpFailure(MailSMTPStageEHLO, err)
	}
	defer stop()
	if err = s.client.Reset(); err != nil {
		return smtpFailure(MailSMTPStageEHLO, err)
	}
	return nil
}

// Share one bucket across the site's workers. Wait before opening the claim transaction.
type MailRateLimiter struct {
	mu   sync.Mutex
	next time.Time
}

func (l *MailRateLimiter) Wait(ctx context.Context, rate int) error {
	if rate < 1 || rate > MailMaxRatePerMinute {
		return ErrMailInvalidInput
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		now := time.Now()
		delay := l.next.Sub(now)
		if delay <= 0 {
			l.next = now.Add(time.Minute / time.Duration(rate))
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// Each batch worker owns one lane for its immutable SMTP revision. Deliver is sequential.
// Close can run concurrently to interrupt a blocked command.
type MailDeliveryLane struct {
	sender    MailSMTPSender
	cfg       MailSMTPConnectionConfig
	reconnect int
	mu        sync.Mutex
	session   MailSMTPSession
	idle      *time.Timer
	count     int
	closed    bool
}

func NewMailDeliveryLane(sender MailSMTPSender, cfg MailSMTPConnectionConfig, reconnect int) *MailDeliveryLane {
	if reconnect < 1 || reconnect > MailMaxReconnectEvery {
		reconnect = MailDefaultReconnectEvery
	}
	return &MailDeliveryLane{sender: sender, cfg: cfg, reconnect: reconnect}
}
func (l *MailDeliveryLane) discardLocked() {
	if l.idle != nil {
		l.idle.Stop()
		l.idle = nil
	}
	if l.session != nil {
		l.session.Close()
		l.session = nil
	}
	l.count = 0
}
func (l *MailDeliveryLane) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.discardLocked()
}
func (l *MailDeliveryLane) take(ctx context.Context) (MailSMTPSession, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, smtpFailure(MailSMTPStageConfig, context.Canceled)
	}
	if l.idle != nil {
		l.idle.Stop()
		l.idle = nil
	}
	s := l.session
	l.mu.Unlock()
	if s != nil {
		return s, nil
	}
	s, err := l.sender.Connect(ctx, l.cfg)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		s.Close()
		return nil, smtpFailure(MailSMTPStageConfig, context.Canceled)
	}
	l.session = s
	return s, nil
}
func (l *MailDeliveryLane) finish(reuse, sent bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sent {
		l.count++
	}
	if !reuse || l.count >= l.reconnect || l.closed {
		l.discardLocked()
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(MailIdleTimeout, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.idle == timer {
			l.discardLocked()
		}
	})
	l.idle = timer
}

// A denied permission already has a durable disposition. A permission error must be
// reconciled by the caller. Neither case issues DATA.
func (l *MailDeliveryLane) Deliver(ctx context.Context, from, to string, msg []byte, permit func(context.Context) (bool, error)) (authorized bool, err error) {
	if permit == nil || len(msg) == 0 || len(msg) > MailMaxMIMEBytes {
		return false, smtpFailure(MailSMTPStageRender, ErrMailTemplateInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, MailSendTimeout)
	defer cancel()
	s, err := l.take(ctx)
	if err != nil {
		return false, err
	}
	reuse, sent := false, false
	defer func() { l.finish(reuse, sent) }()
	if err = s.Envelope(ctx, from, to); err != nil {
		var f *MailSMTPFailure
		if errors.As(err, &f) && f.SMTPCode >= 400 && f.SMTPCode != 421 {
			reuse = s.Reset(ctx) == nil
		}
		return false, err
	}
	ok, err := permit(ctx)
	if err != nil || !ok {
		reuse = s.Reset(ctx) == nil
		return false, err
	}
	err = s.SendData(ctx, msg)
	sent = err == nil
	reuse = sent
	return true, err
}
