//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
	"github.com/hookshield-hq/hookshield/backend/internal/usage"
)

// fakeBilling is a provider with a predictable checkout and a webhook format
// of its own: {"user_id","subscription_id","plan","status","updated_at"},
// "signed" with the header X-Fake-Signature: ok.
type fakeBilling struct{ checkouts []core.CheckoutRequest }

func (f *fakeBilling) Offers() []core.Offer {
	return []core.Offer{
		{ID: "developer", Currency: "USD"},
		{ID: "pro", MonthlyPriceCents: 2900, Currency: "USD", OveragePriceCents: 100, OverageUnit: 10_000, Purchasable: true},
		{ID: "business", MonthlyPriceCents: 8900, Currency: "USD", OveragePriceCents: 80, OverageUnit: 10_000, Purchasable: true},
		{ID: "enterprise", Currency: "USD", ContactURL: "mailto:sales@example.com"},
	}
}

func (f *fakeBilling) Checkout(_ context.Context, req core.CheckoutRequest) (core.CheckoutResult, error) {
	f.checkouts = append(f.checkouts, req)
	if req.Plan != "pro" && req.Plan != "business" {
		return core.CheckoutResult{}, core.ErrUnknownPlan
	}
	if req.Current != nil { // switch the plan in place
		sub := *req.Current
		sub.Plan, sub.UpdatedAt = req.Plan, time.Now()
		return core.CheckoutResult{Subscription: &sub}, nil
	}
	return core.CheckoutResult{URL: "https://pay.example.com/checkout?plan=" + req.Plan + "&return=" + url.QueryEscape(req.ReturnURL)}, nil
}

func (f *fakeBilling) Portal(_ context.Context, _ core.User, sub core.Subscription) (string, error) {
	return "https://pay.example.com/portal/" + sub.SubscriptionID, nil
}

func (f *fakeBilling) HandleWebhook(ctx context.Context, body []byte, h http.Header, acct core.Accounts) error {
	if h.Get("X-Fake-Signature") != "ok" {
		return core.ErrInvalidSignature
	}
	var in struct {
		UserID, SubscriptionID, Plan, Status string
		UpdatedAt                            time.Time
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return core.ErrInvalidPayload
	}
	if in.UserID == "" { // later events may only carry the subscription
		known, err := acct.SubscriptionByProviderID(ctx, "fake", in.SubscriptionID)
		if err != nil {
			return err
		}
		in.UserID = known.UserID
	}
	if _, err := acct.User(ctx, in.UserID); err != nil {
		return err
	}
	_, err := acct.SaveSubscription(ctx, core.Subscription{UserID: in.UserID, Provider: "fake", CustomerID: "cus_1",
		SubscriptionID: in.SubscriptionID, ItemID: "item_1", Plan: in.Plan, Status: in.Status, UpdatedAt: in.UpdatedAt})
	return err
}

func (m *mgmt) billingWebhook(signature string, payload map[string]any) resp {
	m.t.Helper()
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", m.srv.URL+"/v1/billing/webhook", bytes.NewReader(raw))
	req.Header.Set("X-Fake-Signature", signature)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer res.Body.Close()
	out := resp{status: res.StatusCode}
	_ = json.NewDecoder(res.Body).Decode(&out.body)
	if e, ok := out.body["error"].(map[string]any); ok {
		out.code, _ = e["code"].(string)
	}
	return out
}

func TestBillingLifecycle(t *testing.T) {
	provider := &fakeBilling{}
	m := setupMgmt(t, true, provider)
	ctx := context.Background()
	b := m.signedIn("payer@example.com")
	me := user(b.do("GET", "/v1/me", nil))
	userID := str(me["id"])
	_, slug, tok := endpointTo(t, b, newDestination(t), nil)

	if f := b.do("GET", "/v1/features", nil); f.body["billing"] != true {
		t.Fatalf("features: %+v", f.body)
	}

	// Free plan: usage counted against the hard 5,000 quota.
	m.webhook(slug, tok, `{}`, nil)
	ov := b.do("GET", "/v1/billing", nil)
	u := obj(ov.body["usage"])
	if ov.status != 200 || str(ov.body["plan"]) != "developer" || ov.body["subscription"] != nil ||
		u["requests"] != float64(1) || u["included"] != float64(5000) || u["hard_limit"] != true || len(list(ov.body["offers"])) != 4 {
		t.Fatalf("overview: %d %+v", ov.status, ov.body)
	}
	pro := obj(list(ov.body["offers"])[1])
	if str(pro["id"]) != "pro" || obj(pro["limits"])["monthly_requests"] != float64(100_000) || obj(pro["limits"])["retention_hours"] != float64(30*24) {
		t.Fatalf("pro offer: %+v", pro)
	}
	if ent := obj(list(ov.body["offers"])[3]); obj(ent["limits"])["monthly_requests"] != nil {
		t.Fatalf("enterprise must be unlimited (null): %+v", ent)
	}
	m.rdb.Set(ctx, m.keys.MonthlyUsage(userID, limits.Period(time.Now())), 5000, time.Hour)
	if code := m.send(slug, map[string]string{"X-HookShield-Token": tok}, `{}`); code != 429 {
		t.Fatalf("over the free quota: %d", code)
	}

	// Checkout of a new subscription → provider URL with the dashboard return URL.
	co := b.do("POST", "/v1/billing/checkout", map[string]any{"plan": "pro"})
	if co.status != 200 || !strings.Contains(str(co.body["url"]), url.QueryEscape("/en/billing?checkout=success")) || co.body["subscription"] != nil {
		t.Fatalf("checkout: %d %+v", co.status, co.body)
	}
	if r := b.do("POST", "/v1/billing/checkout", map[string]any{"plan": "enterprise"}); r.status != 422 || r.code != "billing.invalid_plan" {
		t.Fatalf("enterprise checkout: %d %s", r.status, r.code)
	}
	if r := b.do("POST", "/v1/billing/portal", nil); r.status != 409 || r.code != "billing.no_subscription" {
		t.Fatalf("portal without subscription: %d %s", r.status, r.code)
	}

	// Provider notifications: signature checked, newest change wins, plan applied at once.
	t1 := time.Now().UTC().Truncate(time.Second)
	if r := m.billingWebhook("forged", map[string]any{"UserID": userID}); r.status != 400 || r.code != "billing.invalid_signature" {
		t.Fatalf("forged webhook: %d %s", r.status, r.code)
	}
	if r := m.billingWebhook("ok", map[string]any{"UserID": userID, "SubscriptionID": "sub_1", "Plan": "pro", "Status": "active", "UpdatedAt": t1}); r.status != 200 {
		t.Fatalf("subscription created: %d %+v", r.status, r.body)
	}
	if p := str(user(b.do("GET", "/v1/me", nil))["plan_type"]); p != "pro" {
		t.Fatalf("plan after subscribing: %s", p)
	}
	// The cached endpoint configuration was dropped: the higher quota applies now.
	if code := m.send(slug, map[string]string{"X-HookShield-Token": tok}, `{}`); code != 202 {
		t.Fatalf("after upgrading: %d", code)
	}
	if r := m.billingWebhook("ok", map[string]any{"SubscriptionID": "sub_1", "Plan": "pro", "Status": "expired", "UpdatedAt": t1.Add(-time.Minute)}); r.status != 200 {
		t.Fatalf("stale webhook: %d", r.status)
	}
	sub := obj(b.do("GET", "/v1/billing", nil).body["subscription"])
	if str(sub["status"]) != "active" || str(sub["plan"]) != "pro" {
		t.Fatalf("a stale notification must be ignored: %+v", sub)
	}
	if r := m.billingWebhook("ok", map[string]any{"SubscriptionID": "sub_unknown", "Plan": "pro", "Status": "active", "UpdatedAt": t1}); r.status != 404 || r.code != "billing.not_found" {
		t.Fatalf("unknown subscription: %d %s", r.status, r.code)
	}

	// A provider ID already linked to another account is refused, not reassigned.
	other := user(m.signedIn("other@example.com").do("GET", "/v1/me", nil))
	if r := m.billingWebhook("ok", map[string]any{"UserID": str(other["id"]), "SubscriptionID": "sub_1", "Plan": "pro", "Status": "active", "UpdatedAt": t1}); r.status != 409 || r.code != "billing.subscription_conflict" {
		t.Fatalf("subscription of another account: %d %s", r.status, r.code)
	}

	// review: two first subscriptions of an account arriving together (paid
	// twice in two tabs): exactly one is stored, the other gets a conflict.
	racer := str(user(m.signedIn("racer@example.com").do("GET", "/v1/me", nil))["id"])
	codes := make(chan int, 2)
	for _, id := range []string{"sub_race_a", "sub_race_b"} {
		go func() {
			codes <- m.billingWebhook("ok", map[string]any{"UserID": racer, "SubscriptionID": id, "Plan": "pro", "Status": "active", "UpdatedAt": t1}).status
		}()
	}
	if a, b := <-codes, <-codes; a+b != 200+409 {
		t.Fatalf("concurrent first subscriptions: %d and %d, want one 200 and one 409", a, b)
	}

	// security review: a second subscription for an account whose current one
	// is still in effect is refused — it must not replace (and later expire)
	// the subscription the user is paying for.
	if r := m.billingWebhook("ok", map[string]any{"UserID": userID, "SubscriptionID": "sub_2", "Plan": "pro", "Status": "expired", "UpdatedAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}); r.status != 409 || r.code != "billing.subscription_conflict" {
		t.Fatalf("second subscription of a paying account: %d %s", r.status, r.code)
	}
	if p := str(user(b.do("GET", "/v1/me", nil))["plan_type"]); p != "pro" {
		t.Fatalf("plan after a foreign subscription event: %s", p)
	}

	// Switching plans while subscribed is applied in place.
	if r := b.do("POST", "/v1/billing/checkout", map[string]any{"plan": "pro"}); r.status != 422 || r.code != "billing.same_plan" {
		t.Fatalf("same plan: %d %s", r.status, r.code)
	}
	sw := b.do("POST", "/v1/billing/checkout", map[string]any{"plan": "business"})
	if sw.status != 200 || sw.body["url"] != nil || str(obj(sw.body["subscription"])["plan"]) != "business" {
		t.Fatalf("switch: %d %+v", sw.status, sw.body)
	}
	if p := str(user(b.do("GET", "/v1/me", nil))["plan_type"]); p != "business" {
		t.Fatalf("plan after switching: %s", p)
	}
	if last := provider.checkouts[len(provider.checkouts)-1]; last.Current == nil || last.Current.SubscriptionID != "sub_1" || last.User.ID != userID {
		t.Fatalf("switch request: %+v", last)
	}
	if r := b.do("POST", "/v1/billing/portal", nil); r.status != 200 || str(r.body["url"]) != "https://pay.example.com/portal/sub_1" {
		t.Fatalf("portal: %d %+v", r.status, r.body)
	}

	// Durable usage and the reporting contract used by billing jobs.
	if n, err := usage.Flush(ctx, m.rdb, m.keys, store.New(m.pool), time.Now()); err != nil || n != 1 {
		t.Fatalf("flush: %d %v", n, err)
	}
	period := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	us := usage.Store{St: store.New(m.pool)}
	rows, err := us.PendingUsage(ctx, "fake", []time.Time{period})
	if err != nil || len(rows) != 1 || rows[0].Requests != 5001 || rows[0].UnitsReported != 0 || rows[0].Subscription.ItemID != "item_1" {
		t.Fatalf("pending usage: %+v %v", rows, err)
	}
	if ok, err := us.MarkUsageReported(ctx, userID, period, 0, 1); !ok || err != nil {
		t.Fatalf("mark reported: %v %v", ok, err)
	}
	if ok, _ := us.MarkUsageReported(ctx, userID, period, 0, 1); ok {
		t.Fatal("a second runner must lose the compare-and-set")
	}
	// A release subtracts only its own claim: another runner's later claim stays.
	if ok, _ := us.MarkUsageReported(ctx, userID, period, 1, 3); !ok {
		t.Fatal("claim 1 → 3")
	}
	if err := us.ReleaseUsage(ctx, userID, period, 1); err != nil {
		t.Fatal(err)
	}
	if err := us.RaiseIncluded(ctx, userID, period, 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := us.RaiseIncluded(ctx, userID, period, 100_000); err != nil { // never lowered
		t.Fatal(err)
	}
	if rows, _ := us.PendingUsage(ctx, "fake", []time.Time{period}); len(rows) != 1 || rows[0].UnitsReported != 2 || rows[0].IncludedRequests != 1_000_000 {
		t.Fatalf("after release and raise: %+v", rows)
	}

	// Expired → back to the free plan.
	if r := m.billingWebhook("ok", map[string]any{"SubscriptionID": "sub_1", "Plan": "business", "Status": "expired", "UpdatedAt": time.Now().Add(time.Minute)}); r.status != 200 {
		t.Fatalf("expired: %d", r.status)
	}
	if p := str(user(b.do("GET", "/v1/me", nil))["plan_type"]); p != "developer" {
		t.Fatalf("plan after expiring: %s", p)
	}
	if rows, _ := us.PendingUsage(ctx, "fake", []time.Time{period}); len(rows) != 0 {
		t.Fatalf("expired subscriptions are not billed: %+v", rows)
	}

	// Billing is for the dashboard session only.
	key := str(b.do("POST", "/v1/api-keys", map[string]any{"name": "a", "scope": "admin"}).body["key"])
	req, _ := http.NewRequest("GET", m.srv.URL+"/v1/billing", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Fatalf("API key on /v1/billing: %v %v", res.StatusCode, err)
	}
}
