package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/lporcheron/quorum/internal/config"
)

// fakeSMTP is a minimal SMTP server: enough of the protocol for one
// message, optional STARTTLS, and AUTH PLAIN accepted blindly. It
// reports each delivered message body on got.
type fakeSMTP struct {
	addr     *net.TCPAddr
	got      chan string
	implicit bool // TLS from the first byte (port 465 style)
}

// newFakeSMTP listens on 127.0.0.1. cert is the httptest certificate:
// valid for 127.0.0.1 but signed by a CA nobody trusts, like a
// self-signed relay certificate.
func newFakeSMTP(t *testing.T, startTLS, implicit bool) *fakeSMTP {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	tlsCfg := &tls.Config{Certificates: ts.TLS.Certificates}
	ts.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().(*net.TCPAddr), got: make(chan string, 1), implicit: implicit}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, tlsCfg, startTLS)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn, tlsCfg *tls.Config, startTLS bool) {
	defer func() { _ = conn.Close() }()
	encrypted := false
	if f.implicit {
		conn = tls.Server(conn, tlsCfg)
		encrypted = true
	}
	r := bufio.NewReader(conn)
	say := func(lines ...string) { _, _ = conn.Write([]byte(strings.Join(lines, "\r\n") + "\r\n")) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if startTLS && !encrypted {
				say("250-fake", "250-STARTTLS", "250 AUTH PLAIN")
			} else {
				say("250-fake", "250 AUTH PLAIN")
			}
		case cmd == "STARTTLS" && startTLS:
			say("220 go ahead")
			tc := tls.Server(conn, tlsCfg)
			if tc.Handshake() != nil {
				return // client refused the certificate
			}
			conn, r, encrypted = tc, bufio.NewReader(tc), true
		case strings.HasPrefix(cmd, "AUTH"):
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"),
			cmd == "RSET", cmd == "NOOP":
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			f.got <- body.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unsupported")
		}
	}
}

func send(t *testing.T, cfg config.SMTP) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return New(cfg, nil).Send(ctx, Message{To: "guest@example.com", Subject: "Hello", Text: "Ballot counted."})
}

func TestSMTPTLSPolicies(t *testing.T) {
	cases := []struct {
		name     string
		startTLS bool
		user     string
		insecure bool
		wantSent bool
	}{
		// An untrusted certificate fails the send by default…
		{name: "untrusted cert rejected", startTLS: true, wantSent: false},
		// …and QUORUM_SMTP_INSECURE accepts it, still over TLS.
		{name: "untrusted cert with insecure", startTLS: true, insecure: true, wantSent: true},
		// Credentials make STARTTLS mandatory: no offer, no send.
		{name: "credentials without STARTTLS", user: "quorum", insecure: true, wantSent: false},
		{name: "credentials over STARTTLS", startTLS: true, user: "quorum", insecure: true, wantSent: true},
		// An anonymous relay without STARTTLS still delivers.
		{name: "anonymous plaintext relay", wantSent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeSMTP(t, tc.startTLS, false)
			err := send(t, config.SMTP{
				Host: "127.0.0.1", Port: srv.addr.Port, From: "polls@example.com",
				Username: tc.user, Password: "secret", Insecure: tc.insecure,
			})
			if tc.wantSent {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				if body := <-srv.got; !strings.Contains(body, "Ballot counted.") {
					t.Errorf("delivered body:\n%s", body)
				}
			} else if err == nil {
				t.Errorf("send succeeded, want refusal")
			} else {
				t.Logf("refused as expected: %v", err)
			}
		})
	}
}

// TestSMTPImplicitTLS checks the port-465 rule end to end: the options
// chosen for port 465 must speak TLS from the first byte. The fake
// cannot bind 465, so the port is redirected after the TLS options.
func TestSMTPImplicitTLS(t *testing.T) {
	srv := newFakeSMTP(t, false, true)
	opts := append(tlsOptions(config.SMTP{Host: "127.0.0.1", Port: 465, Insecure: true}),
		gomail.WithPort(srv.addr.Port))
	client, err := gomail.NewClient("127.0.0.1", opts...)
	if err != nil {
		t.Fatal(err)
	}
	msg := gomail.NewMsg()
	if err := msg.From("polls@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := msg.To("guest@example.com"); err != nil {
		t.Fatal(err)
	}
	msg.SetBodyString(gomail.TypeTextPlain, "Over TLS.")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		t.Fatalf("implicit TLS send: %v", err)
	}
	<-srv.got
}
