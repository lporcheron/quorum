// Package mail sends outgoing email. M2 keeps it minimal and
// synchronous (plain-text messages only); the M4 job queue will wrap
// it with retries and HTML templates.
package mail

import (
	"context"
	"errors"
	"fmt"

	gomail "github.com/wneessen/go-mail"

	"github.com/lporcheron/quorum/internal/config"
)

// ErrDisabled is returned when no SMTP server is configured. The UI
// says so instead of pretending to send.
var ErrDisabled = errors.New("email is disabled on this instance")

// Mailer sends one message. Implementations must be safe for
// concurrent use.
type Mailer interface {
	Enabled() bool
	Send(ctx context.Context, to, subject, textBody string) error
}

// New picks the implementation matching the configuration.
func New(cfg config.SMTP) Mailer {
	if !cfg.Enabled() {
		return disabled{}
	}
	return &smtpMailer{cfg: cfg}
}

type disabled struct{}

func (disabled) Enabled() bool                                      { return false }
func (disabled) Send(context.Context, string, string, string) error { return ErrDisabled }

type smtpMailer struct {
	cfg config.SMTP
}

func (m *smtpMailer) Enabled() bool { return true }

func (m *smtpMailer) Send(ctx context.Context, to, subject, textBody string) error {
	msg := gomail.NewMsg()
	if err := msg.From(m.cfg.From); err != nil {
		return fmt.Errorf("mail from %q: %w", m.cfg.From, err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("mail to %q: %w", to, err)
	}
	msg.Subject(subject)
	msg.SetBodyString(gomail.TypeTextPlain, textBody)

	opts := []gomail.Option{
		gomail.WithPort(m.cfg.Port),
		gomail.WithTLSPortPolicy(gomail.TLSOpportunistic),
	}
	if m.cfg.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(m.cfg.Username),
			gomail.WithPassword(m.cfg.Password),
		)
	}
	client, err := gomail.NewClient(m.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send mail to %s: %w", to, err)
	}
	return nil
}
