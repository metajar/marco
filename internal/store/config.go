package store

import (
	"strconv"
	"time"
)

// Config holds the tunable monitoring and notification settings.
type Config struct {
	CheckInterval    int    // seconds between check rounds
	OfflineAfter     int    // consecutive failed checks before a device is considered offline
	ProbeTimeout     int    // seconds each probe may take
	Ports            string // TCP ports probed on every device
	UseARP           bool   // treat ARP/neighbor-table answers as presence
	TrackAlways      bool   // keep checking outside of schedules (for history/charts)
	RealertMinutes   int    // repeat alerts while still offline; 0 disables
	NotifyRecovered  bool   // text when a device comes back after an alert
	TextbeltKey      string
	TextbeltURL      string
	WebhookURL       string
	AlertTemplate    string
	RecoverTemplate  string
	KidAlertTemplate string // texted to the kid when their device drops off
	Timezone         string
	RetentionDays    int
}

func DefaultConfig() Config {
	return Config{
		CheckInterval:    60,
		OfflineAfter:     4,
		ProbeTimeout:     4,
		Ports:            "62078,80,443,7000,8080",
		UseARP:           true,
		TrackAlways:      true,
		RealertMinutes:   30,
		NotifyRecovered:  true,
		TextbeltURL:      "https://textbelt.com/text",
		AlertTemplate:    "Marco: {person}'s {device} left the home Wi-Fi at {since} during \"{schedule}\".",
		RecoverTemplate:  "Polo! {person}'s {device} is back on the home Wi-Fi after {duration}.",
		KidAlertTemplate: "Hey {person}, looks like your {device} went offline at {since}. This has been logged, and if you're supposed to be on the Wi-Fi, you'd better get back on it.",
		RetentionDays:    90,
	}
}

// Location returns the configured timezone, falling back to the server's.
func (c Config) Location() *time.Location {
	if c.Timezone != "" {
		if loc, err := time.LoadLocation(c.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

func (s *Store) Config() (Config, error) {
	c := DefaultConfig()
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return c, err
		}
		n, _ := strconv.Atoi(v)
		b := v == "1"
		switch k {
		case "check_interval":
			c.CheckInterval = n
		case "offline_after":
			c.OfflineAfter = n
		case "probe_timeout":
			c.ProbeTimeout = n
		case "ports":
			c.Ports = v
		case "use_arp":
			c.UseARP = b
		case "track_always":
			c.TrackAlways = b
		case "realert_minutes":
			c.RealertMinutes = n
		case "notify_recovered":
			c.NotifyRecovered = b
		case "textbelt_key":
			c.TextbeltKey = v
		case "textbelt_url":
			c.TextbeltURL = v
		case "webhook_url":
			c.WebhookURL = v
		case "alert_template":
			c.AlertTemplate = v
		case "recover_template":
			c.RecoverTemplate = v
		case "kid_alert_template":
			c.KidAlertTemplate = v
		case "timezone":
			c.Timezone = v
		case "retention_days":
			c.RetentionDays = n
		}
	}
	c.clamp()
	return c, rows.Err()
}

func (c *Config) clamp() {
	d := DefaultConfig()
	if c.CheckInterval < 15 {
		c.CheckInterval = 15
	}
	if c.OfflineAfter < 1 {
		c.OfflineAfter = 1
	}
	if c.ProbeTimeout < 1 {
		c.ProbeTimeout = 1
	}
	if c.ProbeTimeout > 30 {
		c.ProbeTimeout = 30
	}
	if c.RealertMinutes < 0 {
		c.RealertMinutes = 0
	}
	if c.RetentionDays < 1 {
		c.RetentionDays = d.RetentionDays
	}
	if c.TextbeltURL == "" {
		c.TextbeltURL = d.TextbeltURL
	}
	if c.AlertTemplate == "" {
		c.AlertTemplate = d.AlertTemplate
	}
	if c.RecoverTemplate == "" {
		c.RecoverTemplate = d.RecoverTemplate
	}
	if c.KidAlertTemplate == "" {
		c.KidAlertTemplate = d.KidAlertTemplate
	}
}

func (s *Store) SaveConfig(c Config) error {
	c.clamp()
	bs := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	vals := map[string]string{
		"check_interval":     strconv.Itoa(c.CheckInterval),
		"offline_after":      strconv.Itoa(c.OfflineAfter),
		"probe_timeout":      strconv.Itoa(c.ProbeTimeout),
		"ports":              c.Ports,
		"use_arp":            bs(c.UseARP),
		"track_always":       bs(c.TrackAlways),
		"realert_minutes":    strconv.Itoa(c.RealertMinutes),
		"notify_recovered":   bs(c.NotifyRecovered),
		"textbelt_key":       c.TextbeltKey,
		"textbelt_url":       c.TextbeltURL,
		"webhook_url":        c.WebhookURL,
		"alert_template":     c.AlertTemplate,
		"recover_template":   c.RecoverTemplate,
		"kid_alert_template": c.KidAlertTemplate,
		"timezone":           c.Timezone,
		"retention_days":     strconv.Itoa(c.RetentionDays),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for k, v := range vals {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
