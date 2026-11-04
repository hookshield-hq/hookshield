// Package eventtype detects the event name of a webhook (e.g. "invoice.paid",
// "push") so logs can be filtered by event in the dashboard.
//
// Detection order: well-known provider headers first, then the top-level
// "type", "event_type", "event" or "topic" string field of a JSON body.
package eventtype

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// MaxLen matches payload_logs.event_type (VARCHAR(200)).
const MaxLen = 200

// headerKeys are checked in order (canonical form).
var headerKeys = []string{
	"X-Github-Event",  // GitHub
	"X-Gitlab-Event",  // GitLab
	"X-Shopify-Topic", // Shopify
	"X-Event-Key",     // Bitbucket
	"X-Event-Type",    // generic
	"X-Webhook-Event", // generic
	"X-Hookshield-Event",
}

var bodyKeys = []string{"type", "event_type", "event", "topic"}

// Detect returns the event name, or "" when none is found.
func Detect(headers map[string][]string, body []byte) string {
	for _, k := range headerKeys {
		for hk, v := range headers {
			if http.CanonicalHeaderKey(hk) == k && len(v) > 0 && strings.TrimSpace(v[0]) != "" {
				return clip(strings.TrimSpace(v[0]))
			}
		}
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ""
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return ""
	}
	for _, k := range bodyKeys {
		var s string
		if raw, ok := top[k]; ok && json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return clip(strings.TrimSpace(s))
		}
	}
	return ""
}

func clip(s string) string {
	// Control characters would break the X-HookShield-Event header (every
	// delivery would fail) and alert messages: replace them with spaces.
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) <= MaxLen {
		return s
	}
	// Do not cut a multi-byte UTF-8 sequence in half.
	cut := MaxLen
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
