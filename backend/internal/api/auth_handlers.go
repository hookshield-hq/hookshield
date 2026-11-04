package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/auth"
	"github.com/hookshield-hq/hookshield/backend/internal/httpx"
	"github.com/hookshield-hq/hookshield/backend/internal/store"
)

// UserJSON is the public representation of an account.
type UserJSON struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	EmailVerified   bool      `json:"email_verified"`
	HasPassword     bool      `json:"has_password"`
	Locale          string    `json:"locale"`
	ThemePreference string    `json:"theme_preference"`
	PlanType        string    `json:"plan_type"`
	CreatedAt       time.Time `json:"created_at"`
}

func toUserJSON(u store.User) UserJSON {
	name := ""
	if u.Name != nil {
		name = *u.Name
	}
	return UserJSON{
		ID: u.ID, Email: u.Email, Name: name, EmailVerified: u.EmailVerifiedAt != nil, HasPassword: u.HasPassword,
		Locale: u.Locale, ThemePreference: u.ThemePreference, PlanType: u.PlanType, CreatedAt: u.CreatedAt.UTC(),
	}
}

// decode reads a JSON body (max 64 KiB, unknown fields rejected).
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation.invalid_json")
		return false
	}
	return true
}

// fail maps service errors to responses; unexpected errors become 500 "internal".
func (s *Server) fail(w http.ResponseWriter, err error) {
	var ae *auth.Error
	if errors.As(err, &ae) {
		httpx.Error(w, ae.Status, ae.Code)
		return
	}
	s.opts.Logger.Error("internal error", "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal")
}

func (s *Server) meta(r *http.Request) auth.Meta {
	return auth.Meta{IP: httpx.ClientIP(r, s.opts.TrustedProxies), UserAgent: r.UserAgent()}
}

func (s *Server) providers(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]bool{"password": true, "google": s.auth.GoogleEnabled()})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Locale   string `json:"locale"`
	}
	if !decode(w, r, &in) {
		return
	}
	login, err := s.auth.Register(r.Context(), auth.RegisterInput{Email: in.Email, Password: in.Password, Name: in.Name, Locale: in.Locale}, s.meta(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.setSessionCookie(w, login.Token, s.auth.SessionTTL())
	httpx.JSON(w, http.StatusCreated, map[string]any{"user": toUserJSON(login.User)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	login, err := s.auth.Login(r.Context(), in.Email, in.Password, s.meta(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.setSessionCookie(w, login.Token, s.auth.SessionTTL())
	httpx.JSON(w, http.StatusOK, map[string]any{"user": toUserJSON(login.User)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.clearCookie(w, sessionCookie, "/")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.auth.VerifyEmail(r.Context(), in.Token); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	s.auth.ResendVerification(r.Context(), userFrom(r))
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.auth.ForgotPassword(r.Context(), in.Email)
	w.WriteHeader(http.StatusAccepted) // same answer whether the account exists or not
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.auth.ResetPassword(r.Context(), in.Token, in.Password); err != nil {
		s.fail(w, err)
		return
	}
	s.clearCookie(w, sessionCookie, "/")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.auth.ChangePassword(r.Context(), userFrom(r), sessionFrom(r).ID, in.CurrentPassword, in.NewPassword); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"user": toUserJSON(userFrom(r))})
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            *string `json:"name"`
		Locale          *string `json:"locale"`
		ThemePreference *string `json:"theme_preference"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.auth.UpdateProfile(r.Context(), userFrom(r), store.ProfileUpdate{Name: in.Name, Locale: in.Locale, ThemePreference: in.ThemePreference})
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": toUserJSON(u)})
}

// ── Google ───────────────────────────────────────────────

func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	authURL, state, err := s.auth.GoogleStart(r.Context(), r.URL.Query().Get("redirect"))
	if err != nil {
		s.redirectError(w, r, "/", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: state, Path: "/v1/auth/google", MaxAge: 600,
		HttpOnly: true, Secure: s.opts.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cookieState := ""
	if c, err := r.Cookie(oauthStateCookie); err == nil {
		cookieState = c.Value
	}
	s.clearCookie(w, oauthStateCookie, "/v1/auth/google")

	if q.Get("error") != "" { // user cancelled on Google's screen
		s.redirectError(w, r, "/", auth.ErrGoogleFailed)
		return
	}
	login, redirect, err := s.auth.GoogleCallback(r.Context(), q.Get("state"), cookieState, q.Get("code"), s.meta(r))
	if err != nil {
		s.redirectError(w, r, redirect, err)
		return
	}
	s.setSessionCookie(w, login.Token, s.auth.SessionTTL())
	http.Redirect(w, r, s.opts.AppURL+auth.SafeRedirect(redirect), http.StatusFound)
}

// redirectError sends the browser back to the dashboard login page with an error code.
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, _ string, err error) {
	code := "internal"
	var ae *auth.Error
	if errors.As(err, &ae) {
		code = ae.Code
	} else {
		s.opts.Logger.Error("google sign-in error", "err", err)
	}
	http.Redirect(w, r, s.opts.AppURL+"/login?error="+url.QueryEscape(code), http.StatusFound)
}
