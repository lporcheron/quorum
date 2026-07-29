// Package mail sends outgoing email. Sending is synchronous here; the
// job queue (internal/job) provides retries for notification mail.
package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	gomail "github.com/wneessen/go-mail"

	"github.com/lporcheron/quorum/internal/config"
)

// ErrDisabled is returned when no SMTP server is configured. The UI
// says so instead of pretending to send.
var ErrDisabled = errors.New("email is disabled on this instance")

// Attachment is one file joined to a message.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Message is one outgoing email. Text is mandatory; HTML is an
// optional alternative part.
type Message struct {
	To          string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// Mailer sends one message. Implementations must be safe for
// concurrent use.
type Mailer interface {
	Enabled() bool
	Send(ctx context.Context, msg Message) error
}

// New picks the implementation matching the configuration.
func New(cfg config.SMTP) Mailer {
	if !cfg.Enabled() {
		return disabled{}
	}
	return &smtpMailer{cfg: cfg}
}

type disabled struct{}

func (disabled) Enabled() bool                       { return false }
func (disabled) Send(context.Context, Message) error { return ErrDisabled }

type smtpMailer struct {
	cfg config.SMTP
}

func (m *smtpMailer) Enabled() bool { return true }

func (m *smtpMailer) Send(ctx context.Context, message Message) error {
	msg := gomail.NewMsg()
	if err := msg.From(m.cfg.From); err != nil {
		return fmt.Errorf("mail from %q: %w", m.cfg.From, err)
	}
	if err := msg.To(message.To); err != nil {
		return fmt.Errorf("mail to %q: %w", message.To, err)
	}
	msg.Subject(message.Subject)
	msg.SetBodyString(gomail.TypeTextPlain, message.Text)
	if message.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, message.HTML)
	}
	for _, a := range message.Attachments {
		opts := []gomail.FileOption{}
		if a.ContentType != "" {
			opts = append(opts, gomail.WithFileContentType(gomail.ContentType(a.ContentType)))
		}
		if err := msg.AttachReader(a.Filename, bytes.NewReader(a.Content), opts...); err != nil {
			return fmt.Errorf("attach %s: %w", a.Filename, err)
		}
	}

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
		return fmt.Errorf("send mail to %s: %w", message.To, err)
	}
	return nil
}
