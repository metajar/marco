// Package notify delivers alerts by SMS (Textbelt) and by webhook.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"marco/internal/store"
)

// Message is a single notification about a device.
type Message struct {
	Event    string    `json:"event"` // alert, recovered, test
	Text     string    `json:"message"`
	Person   string    `json:"person,omitempty"`
	Device   string    `json:"device,omitempty"`
	IP       string    `json:"ip,omitempty"`
	Schedule string    `json:"schedule,omitempty"`
	Since    time.Time `json:"since,omitempty"`
	At       time.Time `json:"at"`
}

// Outcome summarizes one delivery attempt across every channel.
type Outcome struct {
	Delivered []string // who/what received it
	Errors    []error
}

func (o Outcome) Summary() string {
	var parts []string
	if len(o.Delivered) > 0 {
		parts = append(parts, "sent to "+strings.Join(o.Delivered, ", "))
	}
	if len(o.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d delivery problem(s), see Notification failed entries", len(o.Errors)))
	}
	if len(parts) == 0 {
		return "no notification channels configured"
	}
	return strings.Join(parts, "; ")
}

type Notifier struct {
	st     *store.Store
	client *http.Client
}

func New(st *store.Store) *Notifier {
	return &Notifier{st: st, client: &http.Client{Timeout: 15 * time.Second}}
}

// Send texts every admin who opted in and calls the webhook, if configured.
func (n *Notifier) Send(ctx context.Context, cfg store.Config, msg Message) Outcome {
	var out Outcome
	if msg.At.IsZero() {
		msg.At = time.Now()
	}
	if cfg.TextbeltKey != "" {
		recipients, err := n.st.AlertRecipients()
		if err != nil {
			out.Errors = append(out.Errors, err)
		}
		if len(recipients) == 0 {
			out.Errors = append(out.Errors, errors.New("no parents have a phone number with alerts enabled"))
		}
		for _, r := range recipients {
			if err := n.SMS(ctx, cfg, r.Phone, msg.Text); err != nil {
				out.Errors = append(out.Errors, fmt.Errorf("text to %s failed: %w", r.Name, err))
			} else {
				out.Delivered = append(out.Delivered, r.Name)
			}
		}
	}
	if cfg.WebhookURL != "" {
		if err := n.Webhook(ctx, cfg.WebhookURL, msg); err != nil {
			out.Errors = append(out.Errors, fmt.Errorf("webhook failed: %w", err))
		} else {
			out.Delivered = append(out.Delivered, "webhook")
		}
	}
	return out
}

// SMS sends a text through Textbelt (https://textbelt.com).
func (n *Notifier) SMS(ctx context.Context, cfg store.Config, phone, text string) error {
	form := url.Values{"phone": {phone}, "message": {text}, "key": {cfg.TextbeltKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TextbeltURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Success        bool   `json:"success"`
		Error          string `json:"error"`
		QuotaRemaining int    `json:"quotaRemaining"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("textbelt: HTTP %d", resp.StatusCode)
	}
	if !body.Success {
		if body.Error == "" {
			body.Error = "unknown error"
		}
		return fmt.Errorf("textbelt: %s", body.Error)
	}
	return nil
}

// Webhook POSTs the message as JSON, e.g. to Home Assistant, ntfy or Slack.
func (n *Notifier) Webhook(ctx context.Context, target string, msg Message) error {
	payload := struct {
		Message
		Text string `json:"text"` // Slack/Discord-style compatibility
	}{msg, msg.Text}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "marco")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Render fills a message template's {placeholders}.
func Render(tmpl string, loc *time.Location, msg Message, duration time.Duration) string {
	since := ""
	if !msg.Since.IsZero() {
		since = msg.Since.In(loc).Format("3:04 PM")
	}
	return strings.NewReplacer(
		"{person}", msg.Person,
		"{device}", msg.Device,
		"{ip}", msg.IP,
		"{schedule}", msg.Schedule,
		"{since}", since,
		"{duration}", HumanDuration(duration),
	).Replace(tmpl)
}

// HumanDuration formats durations like "1h 5m" or "45s".
func HumanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		days := int(d.Hours()) / 24
		h := int(d.Hours()) % 24
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, h)
	}
}
