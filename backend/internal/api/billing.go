package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hookshield-hq/hookshield/backend/core"
	"github.com/hookshield-hq/hookshield/backend/internal/limits"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// FreePlan is the plan of accounts without a usable subscription.
const FreePlan = "developer"

// maxWebhookBody bounds billing provider notifications.
const maxWebhookBody = 256 << 10

// CoreUser converts a stored user to the view extensions get.
func CoreUser(u store.User) core.User {
	name := ""
	if u.Name != nil {
		name = *u.Name
	}
	return core.User{ID: u.ID, Email: u.Email, Name: name, Locale: u.Locale, PlanType: u.PlanType, EmailVerified: u.EmailVerifiedAt != nil}
}

func (s *Server) registerBilling(mux *http.ServeMux) {
	s.handle(mux, "GET /v1/features", accessPublic, s.features)
	if s.opts.Billing == nil {
		return
	}
	s.handle(mux, "GET /v1/billing", accessSession, s.billingOverview)
	s.handle(mux, "POST /v1/billing/checkout", accessSession, s.billingCheckout)
	s.handle(mux, "POST /v1/billing/portal", accessSession, s.billingPortal)
	s.handle(mux, "POST /v1/billing/webhook", accessPublic, s.billingWebhook)
}

// features tells the dashboard which optional screens this server offers.
func (s *Server) features(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"billing": s.opts.Billing != nil})
}

func billingErr(err error) error {
	switch {
	case errors.Is(err, core.ErrInvalidSignature):
		return &apiError{Status: http.StatusBadRequest, Code: "billing.invalid_signature"}
	case errors.Is(err, core.ErrInvalidPayload):
		return &apiError{Status: http.StatusBadRequest, Code: "billing.invalid_payload"}
	case errors.Is(err, core.ErrUnknownPlan):
		return invalid("billing.invalid_plan")
	case errors.Is(err, core.ErrNoSubscription):
		return &apiError{Status: http.StatusConflict, Code: "billing.no_subscription"}
	case errors.Is(err, core.ErrNotFound):
		return &apiError{Status: http.StatusNotFound, Code: "billing.not_found"}
	case errors.Is(err, core.ErrSubscriptionConflict):
		return &apiError{Status: http.StatusConflict, Code: "billing.subscription_conflict"}
	case errors.Is(err, core.ErrProvider):
		return &apiError{Status: http.StatusBadGateway, Code: "billing.provider_error"}
	}
	return err
}

type subscriptionJSON struct {
	Plan     string     `json:"plan"`
	Interval string     `json:"interval"`
	Status   string     `json:"status"`
	RenewsAt *time.Time `json:"renews_at"`
	EndsAt   *time.Time `json:"ends_at"`
	TestMode bool       `json:"test_mode"`
}

func toSubscriptionJSON(sub store.Subscription) *subscriptionJSON {
	return &subscriptionJSON{Plan: sub.Plan, Interval: core.NormalizeInterval(sub.Interval), Status: sub.Status, RenewsAt: utc(sub.RenewsAt), EndsAt: utc(sub.EndsAt), TestMode: sub.TestMode}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toCoreSubscription(sub store.Subscription) core.Subscription {
	return core.Subscription{
		UserID: sub.UserID, Provider: sub.Provider, CustomerID: sub.CustomerID, SubscriptionID: sub.SubscriptionID,
		ItemID: sub.ItemID, Plan: sub.Plan, Interval: core.NormalizeInterval(sub.Interval), Status: sub.Status, RenewsAt: sub.RenewsAt, EndsAt: sub.EndsAt,
		TestMode: sub.TestMode, UpdatedAt: sub.ProviderUpdatedAt,
	}
}

func limitJSON(v int64) any {
	if v == core.Unlimited {
		return nil
	}
	return v
}

// billingOverview is the billing page: plan, subscription, usage of the month and the catalog.
func (s *Server) billingOverview(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var sub *subscriptionJSON
	switch got, err := s.opts.Store.GetSubscription(r.Context(), u.ID); {
	case err == nil:
		sub = toSubscriptionJSON(got)
	case !errors.Is(err, store.ErrNotFound):
		s.fail(w, err)
		return
	}

	now := time.Now().UTC()
	period := limits.Period(now)
	used, err := s.opts.Redis.Get(r.Context(), s.opts.Keys.MonthlyUsage(u.ID, period)).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		s.fail(w, err)
		return
	}
	lim := s.plans().For(u.PlanType)

	offers := make([]map[string]any, 0)
	for _, o := range s.opts.Billing.Offers() {
		l := s.plans().For(o.ID)
		offers = append(offers, map[string]any{
			"id": o.ID, "monthly_price_cents": o.MonthlyPriceCents, "yearly_price_cents": o.YearlyPriceCents, "currency": o.Currency,
			"overage_price_cents": o.OveragePriceCents, "overage_unit": o.OverageUnit,
			"purchasable": o.Purchasable, "contact_url": o.ContactURL,
			"limits": map[string]any{
				"monthly_requests": limitJSON(l.MonthlyRequests), "hard_limit": l.HardLimit,
				"max_retries": limitJSON(int64(l.MaxRetries)), "retention_hours": int64(l.Retention / time.Hour),
			},
		})
	}
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	writeJSON(w, http.StatusOK, map[string]any{
		"plan":         u.PlanType,
		"subscription": sub,
		"usage": map[string]any{
			"period_start": periodStart, "period_end": periodStart.AddDate(0, 1, 0),
			"requests": used, "included": limitJSON(lim.MonthlyRequests), "hard_limit": lim.HardLimit,
		},
		"offers": offers,
	})
}

func (s *Server) billingCheckout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Plan     string `json:"plan"`
		Interval string `json:"interval"` // "month" (default) or "year"
	}
	if !decode(w, r, &in) {
		return
	}
	interval := core.NormalizeInterval(strings.TrimSpace(in.Interval))
	if interval != core.IntervalMonth && interval != core.IntervalYear {
		s.writeErr(w, invalid("billing.invalid_interval"))
		return
	}
	u := userFrom(r)
	req := core.CheckoutRequest{
		User: CoreUser(u), Plan: strings.TrimSpace(in.Plan), Interval: interval,
		ReturnURL: fmt.Sprintf("%s/%s/billing?checkout=success", s.opts.AppURL, u.Locale),
	}
	switch got, err := s.opts.Store.GetSubscription(r.Context(), u.ID); {
	case err == nil:
		cs := toCoreSubscription(got)
		if cs.EffectivePlan(FreePlan, time.Now()) != FreePlan {
			req.Current = &cs
		}
	case !errors.Is(err, store.ErrNotFound):
		s.fail(w, err)
		return
	}
	if req.Current != nil && req.Current.Plan == req.Plan && core.NormalizeInterval(req.Current.Interval) == req.Interval {
		s.writeErr(w, invalid("billing.same_plan"))
		return
	}
	res, err := s.opts.Billing.Checkout(r.Context(), req)
	if err != nil {
		s.billingFail(w, "checkout", err)
		return
	}
	if res.Subscription != nil {
		if _, err := (accounts{s}).SaveSubscription(r.Context(), *res.Subscription); err != nil {
			s.fail(w, err)
			return
		}
		saved, err := s.opts.Store.GetSubscription(r.Context(), u.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"url": nil, "subscription": toSubscriptionJSON(saved)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": res.URL, "subscription": nil})
}

func (s *Server) billingPortal(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	sub, err := s.opts.Store.GetSubscription(r.Context(), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		s.writeErr(w, &apiError{Status: http.StatusConflict, Code: "billing.no_subscription"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	url, err := s.opts.Billing.Portal(r.Context(), CoreUser(u), toCoreSubscription(sub))
	if err != nil {
		s.billingFail(w, "portal", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url})
}

// billingWebhook receives the provider's notifications. It is public: the
// implementation authenticates them by signature.
func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil {
		s.writeErr(w, &apiError{Status: http.StatusBadRequest, Code: "billing.invalid_payload"})
		return
	}
	if len(body) > maxWebhookBody {
		s.writeErr(w, &apiError{Status: http.StatusRequestEntityTooLarge, Code: "billing.payload_too_large"})
		return
	}
	if err := s.opts.Billing.HandleWebhook(r.Context(), body, r.Header, accounts{s}); err != nil {
		s.billingFail(w, "webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// billingFail logs provider failures (never the payload) and answers with a code.
func (s *Server) billingFail(w http.ResponseWriter, op string, err error) {
	mapped := err
	var direct *apiError
	if !errors.As(err, &direct) {
		mapped = billingErr(err)
	}
	var ae *apiError
	if errors.As(mapped, &ae) {
		lvl := s.opts.Logger.Info
		if ae.Status >= 500 {
			lvl = s.opts.Logger.Warn
		}
		lvl("billing request rejected", "op", op, "code", ae.Code, "err", err)
	}
	s.writeErr(w, mapped)
}

func (s *Server) plans() core.Plans {
	if s.opts.Plans == nil {
		return core.SelfHosted{}
	}
	return s.opts.Plans
}

// accounts implements core.Accounts for billing implementations.
type accounts struct{ s *Server }

func (a accounts) User(ctx context.Context, id string) (core.User, error) {
	if !uuidRE.MatchString(id) {
		return core.User{}, core.ErrNotFound
	}
	u, err := a.s.opts.Store.GetUserByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return core.User{}, core.ErrNotFound
	}
	return CoreUser(u), err
}

func (a accounts) SubscriptionByProviderID(ctx context.Context, provider, id string) (core.Subscription, error) {
	sub, err := a.s.opts.Store.GetSubscriptionByProviderID(ctx, provider, id)
	if errors.Is(err, store.ErrNotFound) {
		return core.Subscription{}, core.ErrNotFound
	}
	return toCoreSubscription(sub), err
}

func (a accounts) SaveSubscription(ctx context.Context, sub core.Subscription) (bool, error) {
	if sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = time.Now()
	}
	plan := sub.EffectivePlan(FreePlan, time.Now())
	applied, slugs, err := a.s.opts.Store.SaveSubscription(ctx, store.Subscription{
		UserID: sub.UserID, Provider: sub.Provider, CustomerID: sub.CustomerID, SubscriptionID: sub.SubscriptionID,
		ItemID: sub.ItemID, Plan: sub.Plan, Interval: core.NormalizeInterval(sub.Interval), Status: sub.Status, RenewsAt: sub.RenewsAt, EndsAt: sub.EndsAt,
		TestMode: sub.TestMode, ProviderUpdatedAt: sub.UpdatedAt,
	}, plan)
	if errors.Is(err, store.ErrConflict) {
		// The account already has another subscription in effect (or the
		// provider's subscription ID belongs to another account).
		return false, core.ErrSubscriptionConflict
	}
	if err != nil || !applied {
		return applied, err
	}
	for _, slug := range slugs {
		a.s.invalidate(ctx, slug)
	}
	a.s.opts.Logger.Info("subscription saved", "user", sub.UserID, "provider", sub.Provider, "status", sub.Status, "plan", plan)
	return true, nil
}
