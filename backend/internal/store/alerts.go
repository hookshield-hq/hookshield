package store

import "context"

// Alert channel types (alert_channels.type).
const (
	ChannelWebhook  = "webhook"
	ChannelEmail    = "email"
	ChannelSlack    = "slack"
	ChannelTelegram = "telegram"
)

// AlertChannelAAD binds a channel's encrypted target to the channel row.
func AlertChannelAAD(channelID string) []byte {
	return []byte("alert_channel:" + channelID + ":target")
}

// AlertChannel is a configured alert destination (target still encrypted).
type AlertChannel struct {
	ID              string
	EndpointID      *string // nil = every endpoint of the user
	Type            string
	TargetEncrypted []byte
	TargetNonce     []byte
	KeyVersion      int16
	Enabled         bool
}

// CreateAlertChannel stores a channel. The ID is chosen by the caller because
// the target must be encrypted with AlertChannelAAD(id) before insertion.
func (s *Store) CreateAlertChannel(ctx context.Context, userID string, c AlertChannel) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO alert_channels (id, user_id, endpoint_id, type, target_encrypted, target_nonce, key_version, is_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		c.ID, userID, c.EndpointID, c.Type, c.TargetEncrypted, c.TargetNonce, c.KeyVersion, c.Enabled)
	return mapErr(err)
}

// ListAlertChannels returns the enabled channels that apply to an endpoint:
// the ones bound to it plus the account-wide ones (endpoint_id IS NULL).
func (s *Store) ListAlertChannels(ctx context.Context, userID, endpointID string) ([]AlertChannel, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, endpoint_id, type, target_encrypted, target_nonce, key_version, is_enabled
		FROM alert_channels
		WHERE user_id = $1 AND is_enabled AND (endpoint_id IS NULL OR endpoint_id = $2)
		ORDER BY created_at, id`, userID, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertChannel
	for rows.Next() {
		var c AlertChannel
		if err := rows.Scan(&c.ID, &c.EndpointID, &c.Type, &c.TargetEncrypted, &c.TargetNonce, &c.KeyVersion, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AlertContext is what an alert message needs to know about the endpoint and its owner.
type AlertContext struct {
	EndpointID      string
	EndpointName    string
	EndpointSlug    string
	AlertWebhookURL *string
	UserID          string
	UserEmail       string
	UserLocale      string
}

// GetAlertContext loads the endpoint and owner details for an alert.
func (s *Store) GetAlertContext(ctx context.Context, endpointID string) (AlertContext, error) {
	var c AlertContext
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.name, e.slug, e.alert_webhook_url, u.id, u.email, u.locale
		FROM endpoints e JOIN users u ON u.id = e.user_id
		WHERE e.id = $1`, endpointID,
	).Scan(&c.EndpointID, &c.EndpointName, &c.EndpointSlug, &c.AlertWebhookURL, &c.UserID, &c.UserEmail, &c.UserLocale)
	return c, mapErr(err)
}

// SetEndpointAlertWebhook sets (or clears with "") the endpoint's alert webhook URL.
func (s *Store) SetEndpointAlertWebhook(ctx context.Context, endpointID, url string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE endpoints SET alert_webhook_url = NULLIF($2, '') WHERE id = $1`, endpointID, url)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
