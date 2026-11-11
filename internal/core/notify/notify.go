// Package notify delivers notifications. The desktop channel (in-app + native
// OS notification through the UI) is always on; Discord, Telegram and e-mail
// are opt-in and configured explicitly by the operator.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// Config is persisted in settings. Secret fields hold sealed values.
type Config struct {
	MinSeverity string `json:"min_severity"` // warning | critical
	Discord     struct {
		Enabled       bool   `json:"enabled"`
		WebhookSealed string `json:"webhook_sealed,omitempty"`
	} `json:"discord"`
	Telegram struct {
		Enabled     bool   `json:"enabled"`
		TokenSealed string `json:"token_sealed,omitempty"`
		ChatID      string `json:"chat_id"`
	} `json:"telegram"`
	Email struct {
		Enabled        bool   `json:"enabled"`
		Host           string `json:"host"`
		Port           int    `json:"port"`
		Username       string `json:"username"`
		PasswordSealed string `json:"password_sealed,omitempty"`
		From           string `json:"from"`
		To             string `json:"to"`
	} `json:"email"`
}

type Notifier struct {
	st   *store.Store
	bus  *events.Bus
	prot secrets.Protector
	log  *log.Logger
	hc   *http.Client

	mu  sync.RWMutex
	cfg Config
}

func New(st *store.Store, bus *events.Bus, prot secrets.Protector, logger *log.Logger) *Notifier {
	n := &Notifier{st: st, bus: bus, prot: prot, log: logger, hc: &http.Client{Timeout: 15 * time.Second}}
	n.cfg.MinSeverity = "warning"
	_, _ = st.GetSetting(context.Background(), "notifications", &n.cfg)
	return n
}

func (n *Notifier) Config() Config { n.mu.RLock(); defer n.mu.RUnlock(); return n.cfg }

func (n *Notifier) SetConfig(ctx context.Context, c Config) error {
	if c.MinSeverity != "warning" && c.MinSeverity != "critical" {
		return fmt.Errorf("min_severity must be warning or critical")
	}
	if c.Email.Enabled && (c.Email.Host == "" || c.Email.From == "" || c.Email.To == "") {
		return fmt.Errorf("e-mail requires host, from and to")
	}
	if c.Telegram.Enabled && (c.Telegram.TokenSealed == "" || c.Telegram.ChatID == "") {
		return fmt.Errorf("telegram requires a bot token and chat id")
	}
	if c.Discord.Enabled && c.Discord.WebhookSealed == "" {
		return fmt.Errorf("discord requires a webhook URL")
	}
	if err := n.st.PutSetting(ctx, "notifications", c); err != nil {
		return err
	}
	n.mu.Lock()
	n.cfg = c
	n.mu.Unlock()
	return nil
}

func (n *Notifier) Seal(plain string) (string, error) { return secrets.Seal(n.prot, []byte(plain)) }

func (n *Notifier) open(sealed string) (string, error) {
	b, err := secrets.Open(n.prot, sealed)
	return string(b), err
}

func rank(s string) int {
	switch s {
	case "critical":
		return 2
	case "warning":
		return 1
	}
	return 0
}

// Notify records the notification, pushes it to the desktop and fans out to
// enabled external channels. Delivery results are stored, never discarded.
func (n *Notifier) Notify(ctx context.Context, note *store.Notification) {
	if err := n.st.AddNotification(ctx, note); err != nil {
		n.log.Printf("store notification: %v", err)
	}
	n.bus.Publish("notification", note)
	cfg := n.Config()
	if rank(note.Severity) < rank(cfg.MinSeverity) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		results := map[string]string{}
		if cfg.Discord.Enabled {
			results["discord"] = errStr(n.discord(ctx, cfg, note))
		}
		if cfg.Telegram.Enabled {
			results["telegram"] = errStr(n.telegram(ctx, cfg, note))
		}
		if cfg.Email.Enabled {
			results["email"] = errStr(n.email(cfg, note))
		}
		if len(results) > 0 {
			b, _ := json.Marshal(results)
			_ = n.st.SetNotificationDeliveries(ctx, note.ID, string(b))
			for ch, r := range results {
				if r != "ok" {
					n.log.Printf("notification via %s failed: %s", ch, r)
				}
			}
		}
	}()
}

// Test sends a test message through one channel.
func (n *Notifier) Test(ctx context.Context, channel string) error {
	cfg := n.Config()
	note := &store.Notification{Severity: "warning", Kind: "test", Title: "NEXUS test notification", Body: "If you can read this, the " + channel + " channel works.", Time: time.Now()}
	switch channel {
	case "discord":
		return n.discord(ctx, cfg, note)
	case "telegram":
		return n.telegram(ctx, cfg, note)
	case "email":
		return n.email(cfg, note)
	case "desktop":
		n.bus.Publish("notification", note)
		return nil
	}
	return fmt.Errorf("unknown channel %q", channel)
}

func errStr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func (n *Notifier) post(ctx context.Context, u string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Notifier) discord(ctx context.Context, cfg Config, note *store.Notification) error {
	hook, err := n.open(cfg.Discord.WebhookSealed)
	if err != nil {
		return err
	}
	if u, err := url.Parse(hook); err != nil || u.Scheme != "https" {
		return fmt.Errorf("discord webhook must be an https URL")
	}
	color := 0xE0A030
	if note.Severity == "critical" {
		color = 0xE5484D
	}
	return n.post(ctx, hook, map[string]any{
		"username": "NEXUS",
		"embeds":   []map[string]any{{"title": note.Title, "description": trim(note.Body, 3900), "color": color, "timestamp": note.Time.Format(time.RFC3339)}},
	})
}

func (n *Notifier) telegram(ctx context.Context, cfg Config, note *store.Notification) error {
	tok, err := n.open(cfg.Telegram.TokenSealed)
	if err != nil {
		return err
	}
	return n.post(ctx, "https://api.telegram.org/bot"+url.PathEscape(tok)+"/sendMessage", map[string]any{
		"chat_id": cfg.Telegram.ChatID, "text": trim(strings.ToUpper(note.Severity)+" — "+note.Title+"\n\n"+note.Body, 4000), "disable_web_page_preview": true,
	})
}

func (n *Notifier) email(cfg Config, note *store.Notification) error {
	pw := ""
	if cfg.Email.PasswordSealed != "" {
		var err error
		if pw, err = n.open(cfg.Email.PasswordSealed); err != nil {
			return err
		}
	}
	port := cfg.Email.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(cfg.Email.Host, fmt.Sprint(port))
	var auth smtp.Auth
	if cfg.Email.Username != "" {
		auth = smtp.PlainAuth("", cfg.Email.Username, pw, cfg.Email.Host)
	}
	msg := "From: " + cfg.Email.From + "\r\nTo: " + cfg.Email.To + "\r\nSubject: [NEXUS " + strings.ToUpper(note.Severity) + "] " + strings.ReplaceAll(note.Title, "\n", " ") +
		"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + note.Body + "\r\n"
	// net/smtp upgrades with STARTTLS when offered and refuses PLAIN auth over cleartext.
	return smtp.SendMail(addr, auth, cfg.Email.From, strings.Split(cfg.Email.To, ","), []byte(msg))
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
