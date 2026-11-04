package alert

import (
	"fmt"
	"strings"
)

// Supported locales (users.locale). English is the fallback.
const (
	LocaleEN   = "en"
	LocalePTBR = "pt-BR"
	LocaleES   = "es"
)

// Message keys. Every key must exist in every locale (enforced by tests).
const (
	keyTitleFailing   = "title_failing"
	keyTitleAbandoned = "title_abandoned"
	keyEvent          = "event"
	keyAttempts       = "attempts"
	keyLastHTTP       = "last_http"
	keyLastError      = "last_error"
	keyWebhookID      = "webhook_id"
	keySuppressed     = "suppressed"
	keyOpen           = "open"
	keyNoEvent        = "no_event"
	keyNextRetry      = "next_retry"
	keyNoMoreRetries  = "no_more_retries"
)

var catalog = map[string]map[string]string{
	LocaleEN: {
		keyTitleFailing:   "⚠️ Webhook deliveries are failing — %s",
		keyTitleAbandoned: "❌ Webhook delivery abandoned — %s",
		keyEvent:          "Event: %s",
		keyAttempts:       "Failed attempts: %d",
		keyLastHTTP:       "Last result: HTTP %d",
		keyLastError:      "Last result: %s",
		keyWebhookID:      "Webhook ID: %s",
		keySuppressed:     "%d more webhook(s) of this endpoint failed since the previous alert.",
		keyOpen:           "Open in HookShield: %s",
		keyNoEvent:        "(unknown)",
		keyNextRetry:      "HookShield keeps retrying automatically.",
		keyNoMoreRetries:  "No more retries will be made. You can replay it from the dashboard.",
	},
	LocalePTBR: {
		keyTitleFailing:   "⚠️ Entregas de webhook estão falhando — %s",
		keyTitleAbandoned: "❌ Entrega de webhook abandonada — %s",
		keyEvent:          "Evento: %s",
		keyAttempts:       "Tentativas com falha: %d",
		keyLastHTTP:       "Último resultado: HTTP %d",
		keyLastError:      "Último resultado: %s",
		keyWebhookID:      "ID do webhook: %s",
		keySuppressed:     "Mais %d webhook(s) deste endpoint falharam desde o alerta anterior.",
		keyOpen:           "Abrir no HookShield: %s",
		keyNoEvent:        "(desconhecido)",
		keyNextRetry:      "O HookShield continua tentando automaticamente.",
		keyNoMoreRetries:  "Não haverá novas tentativas. Você pode reenviá-lo pelo painel.",
	},
	LocaleES: {
		keyTitleFailing:   "⚠️ Las entregas de webhooks están fallando — %s",
		keyTitleAbandoned: "❌ Entrega de webhook abandonada — %s",
		keyEvent:          "Evento: %s",
		keyAttempts:       "Intentos fallidos: %d",
		keyLastHTTP:       "Último resultado: HTTP %d",
		keyLastError:      "Último resultado: %s",
		keyWebhookID:      "ID del webhook: %s",
		keySuppressed:     "%d webhook(s) más de este endpoint fallaron desde la alerta anterior.",
		keyOpen:           "Abrir en HookShield: %s",
		keyNoEvent:        "(desconocido)",
		keyNextRetry:      "HookShield sigue reintentando automáticamente.",
		keyNoMoreRetries:  "No habrá más reintentos. Puedes reenviarlo desde el panel.",
	},
}

func normalizeLocale(l string) string {
	if _, ok := catalog[l]; ok {
		return l
	}
	switch strings.ToLower(strings.SplitN(l, "-", 2)[0]) {
	case "pt":
		return LocalePTBR
	case "es":
		return LocaleES
	}
	return LocaleEN
}

func t(locale, key string, args ...any) string {
	return fmt.Sprintf(catalog[normalizeLocale(locale)][key], args...)
}

// Rendered is a localized, human-readable alert.
type Rendered struct {
	Title string
	Lines []string
}

// Text joins title and lines (for email bodies, Telegram, Slack).
func (r Rendered) Text() string {
	return r.Title + "\n\n" + strings.Join(r.Lines, "\n")
}

// Render builds the localized message for an alert.
func Render(a Alert, c Context, locale, logURL string) Rendered {
	name := c.EndpointName
	if name == "" {
		name = c.EndpointSlug
	}
	title := t(locale, keyTitleFailing, name)
	if a.Final {
		title = t(locale, keyTitleAbandoned, name)
	}
	event := a.EventType
	if event == "" {
		event = t(locale, keyNoEvent)
	}
	lines := []string{t(locale, keyEvent, event), t(locale, keyAttempts, a.Attempts)}
	if a.LastStatus > 0 {
		lines = append(lines, t(locale, keyLastHTTP, a.LastStatus))
	} else if a.LastError != "" {
		lines = append(lines, t(locale, keyLastError, a.LastError))
	}
	lines = append(lines, t(locale, keyWebhookID, a.LogID))
	if a.Final {
		lines = append(lines, t(locale, keyNoMoreRetries))
	} else {
		lines = append(lines, t(locale, keyNextRetry))
	}
	if a.Suppressed > 0 {
		lines = append(lines, t(locale, keySuppressed, a.Suppressed))
	}
	if logURL != "" {
		lines = append(lines, t(locale, keyOpen, logURL))
	}
	return Rendered{Title: title, Lines: lines}
}
