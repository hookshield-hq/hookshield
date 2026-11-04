//go:build integration

package alert_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/alert"
	"github.com/hookshield-hq/hookshield/backend/internal/crypto"
	"github.com/hookshield-hq/hookshield/backend/internal/db"
	"github.com/hookshield-hq/hookshield/backend/internal/outbound"
	"github.com/hookshield-hq/hookshield/backend/internal/queue"
	"github.com/hookshield-hq/hookshield/backend/internal/seed"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/testdb"
	"github.com/hookshield-hq/hookshield/backend/internal/testredis"
	"github.com/hookshield-hq/hookshield/backend/internal/uuid"
)

// recorder is a fake HTTP channel (webhook, Slack or Telegram API).
type recorder struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	fail   int // fail the first N requests with 500
	srv    *httptest.Server
}

func newRecorder(t *testing.T, fail int) *recorder {
	r := &recorder{fail: fail}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.fail > 0 {
			r.fail--
			w.WriteHeader(500)
			return
		}
		r.paths = append(r.paths, req.URL.Path)
		r.bodies = append(r.bodies, string(b))
		w.WriteHeader(200)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

type spyMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *spyMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	m.sent = append(m.sent, to+"|"+subject+"|"+body)
	m.mu.Unlock()
	return nil
}

type env struct {
	ctx    context.Context
	store  *store.Store
	cipher *crypto.Cipher
	seed   seed.Result
	mailer *spyMailer
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if err := db.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	res, err := seed.Run(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := crypto.New(bytes.Repeat([]byte{8}, crypto.KeySize), 1)
	return &env{ctx: ctx, store: s, cipher: cipher, seed: res, mailer: &spyMailer{}}
}

func (e *env) addChannel(t *testing.T, endpointID *string, typ string, target any, enabled bool) {
	t.Helper()
	id := uuid.NewV7()
	ct, nonce, err := alert.EncryptTarget(e.cipher, id, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateAlertChannel(e.ctx, e.seed.UserID, store.AlertChannel{
		ID: id, EndpointID: endpointID, Type: typ, TargetEncrypted: ct, TargetNonce: nonce, KeyVersion: 1, Enabled: enabled,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) sender(telegramAPI string) *alert.Sender {
	return alert.NewSender(nil, e.store, e.cipher, outbound.NewClient(outbound.Options{AllowPrivate: true, Timeout: 2 * time.Second}),
		alert.SenderOptions{AppURL: "https://app.hookshield.dev", TelegramAPI: telegramAPI, Mailer: e.mailer,
			RetryDelays: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}})
}

func sampleAlert(e *env) alert.Alert {
	return alert.Alert{LogID: "log-42", EndpointID: e.seed.EndpointID, UserID: e.seed.UserID, EventType: "invoice.paid",
		Attempts: 4, LastStatus: 503, At: time.Now().UTC(), Suppressed: 1}
}

func TestDeliverToAllChannelTypes(t *testing.T) {
	e := setup(t)
	hook := newRecorder(t, 0)
	endpointHook := newRecorder(t, 0)
	slack := newRecorder(t, 0)
	tg := newRecorder(t, 0)

	if err := e.store.SetEndpointAlertWebhook(e.ctx, e.seed.EndpointID, endpointHook.srv.URL); err != nil {
		t.Fatal(err)
	}
	ep := e.seed.EndpointID
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: hook.srv.URL + "/alerts"}, true)
	e.addChannel(t, &ep, store.ChannelSlack, alert.SlackTarget{WebhookURL: slack.srv.URL + "/services/T/B/X"}, true)
	e.addChannel(t, nil, store.ChannelTelegram, alert.TelegramTarget{BotToken: "123:ABC", ChatID: "-1001"}, true)
	e.addChannel(t, nil, store.ChannelEmail, alert.EmailTarget{}, true)                                     // account email
	e.addChannel(t, nil, store.ChannelEmail, alert.EmailTarget{To: "ops@example.com"}, true)                // explicit
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: hook.srv.URL + "/disabled"}, false) // disabled

	results, err := e.sender(tg.srv.URL).Deliver(e.ctx, sampleAlert(e))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 { // endpoint webhook + 5 enabled channels
		t.Fatalf("results %+v", results)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s failed: %v", r.Type, r.Err)
		}
	}

	var p alert.Payload
	if err := json.Unmarshal([]byte(hook.got()[0]), &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != "hookshield.alert" || p.WebhookID != "log-42" || p.Attempts != 4 || p.LastStatus != 503 || p.Suppressed != 1 ||
		p.Endpoint.Slug != "ep_demo" || p.URL != "https://app.hookshield.dev/logs/log-42" || !strings.Contains(p.Message, "failing") {
		t.Fatalf("payload %+v", p)
	}
	if hook.paths[0] != "/alerts" || len(hook.got()) != 1 {
		t.Fatalf("disabled channel used or wrong path: %v", hook.paths)
	}
	if len(endpointHook.got()) != 1 {
		t.Fatal("endpoint alert_webhook_url not called")
	}
	if !strings.Contains(slack.got()[0], `"text":"*⚠️ Webhook deliveries are failing`) {
		t.Fatalf("slack body %s", slack.got()[0])
	}
	if tg.paths[0] != "/bot123:ABC/sendMessage" || !strings.Contains(tg.got()[0], `"chat_id":"-1001"`) {
		t.Fatalf("telegram %v %s", tg.paths, tg.got()[0])
	}
	if len(e.mailer.sent) != 2 || !strings.HasPrefix(e.mailer.sent[0], "dev@hookshield.local|⚠️") || !strings.HasPrefix(e.mailer.sent[1], "ops@example.com|") {
		t.Fatalf("emails %v", e.mailer.sent)
	}
}

func TestChannelScopeAndLocale(t *testing.T) {
	e := setup(t)
	if _, err := e.store.Pool().Exec(e.ctx, `UPDATE users SET locale = 'pt-BR' WHERE id = $1`, e.seed.UserID); err != nil {
		t.Fatal(err)
	}
	// A second endpoint of the same user: its channel must not receive alerts of ep_demo.
	other, err := e.store.CreateEndpoint(e.ctx, store.CreateEndpointParams{UserID: e.seed.UserID, Name: "other", Slug: "ep_other",
		TargetURL: "https://x.io", AuthTokenHash: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	mine := newRecorder(t, 0)
	notMine := newRecorder(t, 0)
	ep := e.seed.EndpointID
	e.addChannel(t, &ep, store.ChannelWebhook, alert.WebhookTarget{URL: mine.srv.URL}, true)
	e.addChannel(t, &other.ID, store.ChannelWebhook, alert.WebhookTarget{URL: notMine.srv.URL}, true)

	if _, err := e.sender("").Deliver(e.ctx, sampleAlert(e)); err != nil {
		t.Fatal(err)
	}
	if len(mine.got()) != 1 || len(notMine.got()) != 0 {
		t.Fatalf("scoping: mine=%d other=%d", len(mine.got()), len(notMine.got()))
	}
	if !strings.Contains(mine.got()[0], "Entregas de webhook estão falhando") {
		t.Fatalf("message not localized: %s", mine.got()[0])
	}
}

func TestRetriesAndFailuresAreIsolated(t *testing.T) {
	e := setup(t)
	flaky := newRecorder(t, 2)   // fails twice, then succeeds on the 3rd attempt
	broken := newRecorder(t, 99) // always fails
	good := newRecorder(t, 0)
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: flaky.srv.URL}, true)
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: broken.srv.URL}, true)
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: good.srv.URL}, true)
	e.addChannel(t, nil, store.ChannelSlack, alert.SlackTarget{}, true) // invalid target

	results, _ := e.sender("").Deliver(e.ctx, sampleAlert(e))
	errs := 0
	for _, r := range results {
		if r.Err != nil {
			errs++
		}
	}
	if errs != 2 || len(flaky.got()) != 1 || len(good.got()) != 1 {
		t.Fatalf("errors=%d flaky=%d good=%d results=%+v", errs, len(flaky.got()), len(good.got()), results)
	}
}

func TestEmailWithoutSMTPIsReported(t *testing.T) {
	e := setup(t)
	e.addChannel(t, nil, store.ChannelEmail, alert.EmailTarget{}, true)
	s := alert.NewSender(nil, e.store, e.cipher, http.DefaultClient, alert.SenderOptions{})
	results, _ := s.Deliver(e.ctx, sampleAlert(e))
	if len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "HS_SMTP_URL") {
		t.Fatalf("results %+v", results)
	}
}

func TestServiceCooldownGroupsAlerts(t *testing.T) {
	rdb, keys := testredis.New(t)
	ctx := context.Background()
	svc := alert.NewService(rdb, keys, time.Hour)
	a := alert.Alert{LogID: "l1", EndpointID: "e1", UserID: "u1", Attempts: 4, At: time.Now()}

	for i := 0; i < 4; i++ {
		a.LogID = "l" + string(rune('1'+i))
		if err := svc.Notify(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := rdb.XLen(ctx, keys.AlertStream()).Result(); n != 1 {
		t.Fatalf("stream has %d alerts during cooldown, want 1", n)
	}
	// Another endpoint is not affected by e1's cooldown.
	if err := svc.Notify(ctx, alert.Alert{LogID: "x", EndpointID: "e2", At: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// Cooldown over: the next alert reports the 3 grouped ones.
	rdb.Del(ctx, keys.AlertCooldown("e1"))
	a.LogID = "l9"
	_ = svc.Notify(ctx, a)

	consumer := queue.NewConsumer(rdb, keys.AlertStream(), alert.Group, "t")
	_ = consumer.EnsureGroup(ctx)
	msgs, _ := consumer.Read(ctx, 10, 50*time.Millisecond)
	if len(msgs) != 3 {
		t.Fatalf("got %d alerts, want 3", len(msgs))
	}
	if msgs[2].Values["log_id"] != "l9" || msgs[2].Values["suppressed"] != "3" || msgs[0].Values["suppressed"] != "0" {
		t.Fatalf("suppressed counters: %v / %v", msgs[0].Values, msgs[2].Values)
	}
}

func TestSenderConsumesStream(t *testing.T) {
	e := setup(t)
	rdb, keys := testredis.New(t)
	hook := newRecorder(t, 0)
	e.addChannel(t, nil, store.ChannelWebhook, alert.WebhookTarget{URL: hook.srv.URL}, true)

	svc := alert.NewService(rdb, keys, 0)
	_ = svc.Notify(e.ctx, sampleAlert(e))
	_ = svc.Notify(e.ctx, alert.Alert{EndpointID: "00000000-0000-0000-0000-000000000000", At: time.Now()}) // deleted endpoint

	consumer := queue.NewConsumer(rdb, keys.AlertStream(), alert.Group, "s1")
	_ = consumer.EnsureGroup(e.ctx)
	s := alert.NewSender(consumer, e.store, e.cipher, outbound.NewClient(outbound.Options{AllowPrivate: true}),
		alert.SenderOptions{Block: 50 * time.Millisecond})
	n, err := s.SendBatch(e.ctx)
	if err != nil || n != 2 {
		t.Fatalf("handled %d, err %v", n, err)
	}
	if len(hook.got()) != 1 {
		t.Fatal("alert not delivered from the stream")
	}
	if l, _ := rdb.XLen(e.ctx, keys.AlertStream()).Result(); l != 0 {
		t.Fatal("alerts not acknowledged and deleted")
	}
}
