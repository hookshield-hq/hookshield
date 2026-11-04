package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// TriggerRequest is a test event (POST /v1/endpoints/{id}/test): it goes
// through the whole pipeline — transform rules, delivery, retries, alerts —
// and also reaches `hookshield listen`.
type TriggerRequest struct {
	Method   string              `json:"method,omitempty"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Body     json.RawMessage     `json:"body,omitempty"`
	BodyText *string             `json:"body_text,omitempty"`
}

// BuildTrigger turns the trigger flags into a request. data is JSON, @file or
// "-" (stdin); event alone sends {"type": event, …}.
func BuildTrigger(method, event, data string, headers []string, now time.Time) (TriggerRequest, error) {
	tr := TriggerRequest{Method: strings.ToUpper(strings.TrimSpace(method))}
	if tr.Method == "" {
		tr.Method = http.MethodPost
	}
	if len(headers) > 0 {
		// Custom headers replace the API's defaults, so keep a Content-Type.
		tr.Headers = map[string][]string{}
		for _, h := range headers {
			k, v, ok := strings.Cut(h, ":")
			if !ok || strings.TrimSpace(k) == "" {
				return tr, fmt.Errorf("--header %q: use \"Name: value\"", h)
			}
			ck := http.CanonicalHeaderKey(strings.TrimSpace(k))
			tr.Headers[ck] = append(tr.Headers[ck], strings.TrimSpace(v))
		}
		if tr.Headers["Content-Type"] == nil {
			tr.Headers["Content-Type"] = []string{"application/json"}
		}
	}

	raw := data
	switch {
	case data == "-":
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20+1))
		if err != nil {
			return tr, err
		}
		if len(b) > 1<<20 {
			return tr, errors.New("--data from stdin is larger than 1 MiB")
		}
		raw = string(b)
	case strings.HasPrefix(data, "@"):
		b, err := os.ReadFile(strings.TrimPrefix(data, "@"))
		if err != nil {
			return tr, err
		}
		raw = string(b)
	}
	switch {
	case raw != "" && json.Valid([]byte(raw)):
		tr.Body = json.RawMessage(raw)
	case raw != "":
		tr.BodyText = &raw
	case event != "":
		b, _ := json.Marshal(map[string]any{"type": event, "message": "Sent by hookshield trigger", "sent_at": now.UTC().Format(time.RFC3339)})
		tr.Body = b
	}
	return tr, nil
}

// Trigger sends a test event to an endpoint and returns the new log ID.
func (c *Client) Trigger(ctx context.Context, endpointID string, tr TriggerRequest) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.Do(ctx, http.MethodPost, "/v1/endpoints/"+url.PathEscape(endpointID)+"/test", nil, tr, &out)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("the API did not return a log ID")
	}
	return out.ID, nil
}
