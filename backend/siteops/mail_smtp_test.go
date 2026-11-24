package siteops

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mailFixture struct {
	cfg                          MailSMTPConnectionConfig
	sender                       *smtpSender
	messages, connections, auths atomic.Int32
}

func newMailFixture(t *testing.T, mode MailTLSMode, auth MailAuthMode, behavior string) *mailFixture {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, p, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(p)
	fixture := &mailFixture{cfg: MailSMTPConnectionConfig{host, port, mode, auth, "fixture-user", "fixture-pass"}, sender: &smtpSender{guard: NewOutboundGuard(true, nil, nil), roots: roots}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			fixture.connections.Add(1)
			go fixture.serve(conn, cert, behavior)
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	return fixture
}
func (f *mailFixture) serve(conn net.Conn, cert tls.Certificate, behavior string) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	secure := f.cfg.TLSMode == MailTLSModeImplicit
	if secure {
		conn = tls.Server(conn, cfg)
	}
	r := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 fixture\r\n")
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "EHLO "):
			ext := "HELP"
			if secure {
				ext = "AUTH LOGIN PLAIN"
			} else if behavior != "no-upgrade" {
				ext = "STARTTLS"
			}
			fmt.Fprintf(conn, "250-fixture\r\n250 %s\r\n", ext)
		case line == "STARTTLS":
			fmt.Fprint(conn, "220 begin TLS\r\n")
			conn = tls.Server(conn, cfg)
			r = bufio.NewReader(conn)
			secure = true
		case strings.HasPrefix(line, "AUTH PLAIN "):
			if !secure {
				return
			}
			f.auths.Add(1)
			fmt.Fprint(conn, "235 OK\r\n")
		case line == "AUTH LOGIN":
			if !secure {
				return
			}
			for _, prompt := range []string{"VXNlcm5hbWU6", "UGFzc3dvcmQ6"} {
				fmt.Fprintf(conn, "334 %s\r\n", prompt)
				if _, err := r.ReadString('\n'); err != nil {
					return
				}
			}
			f.auths.Add(1)
			fmt.Fprint(conn, "235 OK\r\n")
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"), line == "RSET":
			fmt.Fprint(conn, "250 OK\r\n")
		case line == "DATA":
			if behavior == "no-data" {
				fmt.Fprint(conn, "554 fixture detail\r\n")
				continue
			}
			fmt.Fprint(conn, "354 write\r\n")
			if _, err := io.Copy(io.Discard, textproto.NewReader(r).DotReader()); err != nil {
				return
			}
			f.messages.Add(1)
			switch behavior {
			case "lost":
				return
			case "rejected":
				fmt.Fprint(conn, "550 fixture detail\r\n")
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		default:
			return
		}
	}
}
func TestMailSMTPProtocol(t *testing.T) {
	for _, mode := range []MailTLSMode{MailTLSModeImplicit, MailTLSModeStarttls} {
		for _, auth := range []MailAuthMode{MailAuthModePlain, MailAuthModeLogin, MailAuthModeAuto, MailAuthModeNone} {
			t.Run(string(mode)+"/"+string(auth), func(t *testing.T) {
				fixture := newMailFixture(t, mode, auth, "")
				lane := NewMailDeliveryLane(fixture.sender, fixture.cfg, 2)
				defer lane.Close()
				for i := 0; i < 4; i++ {
					permit := i > 0
					allowed, err := lane.Deliver(context.Background(), "from@example.com", "to@example.com", []byte("hello\r\n"), func(context.Context) (bool, error) { return permit, nil })
					if err != nil || allowed != permit {
						t.Fatal(allowed, err)
					}
				}
				if fixture.messages.Load() != 3 || fixture.connections.Load() != 2 {
					t.Fatal("incorrect permission or connection reuse")
				}
				if auth != MailAuthModeNone && fixture.auths.Load() != 2 {
					t.Fatal("authentication was skipped")
				}
			})
		}
	}
}
func TestMailSMTPEvidence(t *testing.T) {
	for _, tc := range []struct {
		behavior string
		stage    MailSMTPStage
		status   MailItemStatus
		code     int
	}{{"lost", MailSMTPStageDataCommit, MailItemStatusUncertain, 0}, {"rejected", MailSMTPStageDataCommit, MailItemStatusFailed, 550}, {"no-data", MailSMTPStageDataCmd, MailItemStatusFailed, 554}} {
		fixture := newMailFixture(t, MailTLSModeImplicit, MailAuthModeNone, tc.behavior)
		lane := NewMailDeliveryLane(fixture.sender, fixture.cfg, 4)
		auth, err := lane.Deliver(context.Background(), "from@example.com", "to@example.com", []byte("hello"), func(context.Context) (bool, error) { return true, nil })
		lane.Close()
		status, f := ClassifyMailSMTPFailure(err, auth)
		if f.Stage != tc.stage || f.SMTPCode != tc.code || status != tc.status || strings.Contains(err.Error(), "fixture detail") {
			t.Fatal(status, f)
		}
	}
	for _, behavior := range []string{"no-upgrade", "untrusted"} {
		fixture := newMailFixture(t, MailTLSModeStarttls, MailAuthModeAuto, behavior)
		if behavior == "untrusted" {
			fixture.sender.roots = x509.NewCertPool()
		}
		_, err := fixture.sender.Connect(context.Background(), fixture.cfg)
		var f *MailSMTPFailure
		if !errors.As(err, &f) || f.Stage != MailSMTPStageTLS || fixture.auths.Load() != 0 {
			t.Fatal("TLS downgrade or missing verification", err)
		}
	}
}
func TestMailSMTPAddressPolicy(t *testing.T) {
	guard := NewOutboundGuard(true, []string{"https://10.1.2.3:465"}, []string{"0.0.0.0/0", "::/0"})
	for _, h := range []string{"::", "::ffff:169.254.1.1", "100.100.100.200", "fd00:ec2::254", "224.0.0.1", "fe80::2", "::1%eth0", "a@example.com", "https://smtp.example.com", "a\nb"} {
		if guard.ValidateSMTPHostPort(h, 465) == nil {
			t.Fatal("accepted host", h)
		}
	}
	guard.UpdatePolicy(false, []string{"https://10.1.2.3:465"}, nil)
	if guard.ValidateSMTPHostPort("10.1.2.3", 465) == nil {
		t.Fatal("HTTP exception inherited")
	}
}

func TestMailSMTPProcessCapAndRate(t *testing.T) {
	fixture := newMailFixture(t, MailTLSModeImplicit, MailAuthModeNone, "")
	var sessions []MailSMTPSession
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()
	for i := 0; i < MailMaxProcessConnections; i++ {
		s, err := fixture.sender.Connect(context.Background(), fixture.cfg)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if s, err := fixture.sender.Connect(ctx, fixture.cfg); err == nil {
		s.Close()
		t.Fatal("exceeded process connection cap")
	}
	sessions[0].Close()
	s, err := fixture.sender.Connect(context.Background(), fixture.cfg)
	if err != nil {
		t.Fatal("connection slot not released", err)
	}
	s.Close()
	var bucket MailRateLimiter
	if err = bucket.Wait(context.Background(), 300); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err = bucket.Wait(ctx, 300); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("token bucket exceeded burst capacity", err)
	}
}
