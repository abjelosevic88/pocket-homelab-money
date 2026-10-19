package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

type ctxKey int

const userKey ctxKey = 1

const sessionCookie = "pm_session"

func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

func actorID(ctx context.Context) int64 {
	if u := userFrom(ctx); u != nil {
		return u.ID
	}
	return 0
}

// requireAuth resolves the current user from a session cookie, bearer token,
// or trusted proxy header depending on AUTH_MODE.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.resolvePrincipal(r)
		if errors.Is(err, errLinkScope) {
			writeErr(w, http.StatusForbidden, "this Pocket Core link does not cover "+r.Method+" "+r.URL.Path)
			return
		}
		if err != nil || p == nil || p.User == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

func (s *Server) resolveUser(r *http.Request) (*store.User, error) {
	p, err := s.resolvePrincipal(r)
	if err != nil || p == nil {
		return nil, err
	}
	return p.User, nil
}

// resolvePrincipal is resolveUser plus how the person came in (session, API
// token, Pocket Core device), which the audit log records.
func (s *Server) resolvePrincipal(r *http.Request) (*principal, error) {
	p, err := s.resolvePrincipalRaw(r)
	if err != nil || p == nil || p.User == nil {
		return nil, err
	}
	if p.Kind == "link" && !linkAllows(p, r) {
		return nil, errLinkScope
	}
	return p, nil
}

func (s *Server) resolvePrincipalRaw(r *http.Request) (*principal, error) {
	ctx := r.Context()
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		tok = strings.TrimSpace(h[7:])
	} else if t := r.Header.Get("X-API-Key"); t != "" {
		tok = t
	}
	if tok != "" {
		if s.core.Configured() && (core.IsCoreToken(tok) || core.IsLinkToken(tok)) {
			return s.coreUser(ctx, tok)
		}
		u, name, err := s.store.APITokenUser(ctx, tok)
		via := "API token"
		if name != "" {
			via = "API token '" + name + "'"
		}
		return &principal{User: u, Kind: "token", Via: via, Source: "api"}, err
	}
	switch s.cfg.AuthMode {
	case "none":
		u, err := s.firstOrSystemUser(ctx, "admin@localhost", "Admin")
		return &principal{User: u, Kind: "none", Source: "web"}, err
	case "proxy":
		name := r.Header.Get(s.cfg.AuthProxyHeader)
		if name == "" {
			return nil, http.ErrNoCookie
		}
		email := r.Header.Get(s.cfg.AuthProxyEmailHeader)
		if email == "" {
			email = name
			if !strings.Contains(email, "@") {
				email += "@proxy"
			}
		}
		u, err := s.firstOrSystemUser(ctx, strings.ToLower(email), name)
		return &principal{User: u, Kind: "proxy", Source: "web"}, err
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetSessionUser(ctx, c.Value)
	return &principal{User: u, Kind: "session", Source: "web"}, err
}

// coreUser validates a token Pocket Core issued to a paired phone (pcd_), a
// person (pcu_) or an OIDC client (pco_) and maps it to the local account
// with the same email, creating a member on first sight.
func (s *Server) coreUser(ctx context.Context, tok string) (*principal, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || in.User == nil || in.User.Email == "" {
		return nil, http.ErrNoCookie
	}
	u, err := s.firstOrSystemUser(ctx, strings.ToLower(in.User.Email), in.User.Name)
	if in.Delegated() {
		if in.App == nil || in.App.Slug == "" {
			return nil, http.ErrNoCookie
		}
		name := in.App.Name
		if name == "" {
			name = in.App.Slug
		}
		return &principal{User: u, Kind: "link", Via: name + " (Pocket Core link)", Source: "api", LinkApp: in.App.Slug, Scopes: strings.Fields(in.Scope)}, err
	}
	via := "Pocket Core"
	switch {
	case in.Device != nil && in.Device.Name != "":
		via = "Pocket Core device '" + in.Device.Name + "'"
	case in.Device != nil:
		via = "Pocket Core device"
	case in.App != nil && in.App.Name != "":
		via = "Pocket Core app '" + in.App.Name + "'"
	}
	return &principal{User: u, Kind: "core", Via: via, Source: "api"}, err
}

// firstOrSystemUser returns the user with that email, creating it when missing.
// The very first account becomes admin (and adopts the integration defaults
// seeded from the environment); later ones are members and start with an
// empty ledger: no accounts, categories, tags, settings or tokens of anyone else.
func (s *Server) firstOrSystemUser(ctx context.Context, email, name string) (*store.User, error) {
	if u, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return u, nil
	}
	role := "member"
	if n, _ := s.store.CountUsers(ctx); n == 0 {
		role = "admin"
	}
	u := &store.User{Email: email, Name: name, Role: role}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	if role == "admin" {
		_ = s.store.AdoptSettings(ctx, u.ID)
	}
	s.log.Info("auto-provisioned user", "email", email, "mode", s.cfg.AuthMode, "role", role)
	return u, nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, id string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", HttpOnly: true,
		Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()),
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.fail(w, err, "count users")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), actorID(r.Context()))
	u, _ := s.resolveUser(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":    n == 0 && s.cfg.AuthMode == "local",
		"auth_mode":      s.cfg.AuthMode,
		"authenticated":  u != nil,
		"user":           u,
		"version":        Version,
		"title":          st.Title,
		"oidc":           s.oidcEnabled() && s.cfg.AuthMode == "local",
		"oidc_name":      s.oidcName(),
		"password_login": s.cfg.PasswordLogin || s.cfg.AuthMode != "local",
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		s.fail(w, err, "count users")
		return
	}
	if n > 0 || s.cfg.AuthMode != "local" {
		writeErr(w, http.StatusForbidden, "setup already completed")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" || len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "email and a password of at least 8 characters are required")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	u := &store.User{Email: in.Email, Name: in.Name, PasswordHash: string(hash), Role: "admin"}
	if err := s.store.CreateUser(ctx, u); err != nil {
		s.fail(w, err, "create user")
		return
	}
	_ = s.store.AdoptSettings(ctx, u.ID)
	st, _ := s.store.GetSettings(ctx, u.ID)
	st.SetupComplete = true
	_ = s.store.SaveSettings(ctx, u.ID, st, true)
	id, err := s.store.CreateSession(ctx, u.ID, s.cfg.SessionTTL)
	if err != nil {
		s.fail(w, err, "create session")
		return
	}
	s.setSessionCookie(w, id, s.cfg.SessionTTL)
	s.log.Info("initial setup completed", "email", u.Email)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.cfg.PasswordLogin {
		writeErr(w, http.StatusForbidden, "password sign-in is switched off — use "+s.oidcName())
		return
	}
	u, err := s.store.GetUserByEmail(r.Context(), strings.TrimSpace(strings.ToLower(in.Email)))
	if err != nil || u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Password)) != nil {
		time.Sleep(300 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	id, err := s.store.CreateSession(r.Context(), u.ID, s.cfg.SessionTTL)
	if err != nil {
		s.fail(w, err, "create session")
		return
	}
	s.setSessionCookie(w, id, s.cfg.SessionTTL)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	s.setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in struct {
		Name            string `json:"name"`
		Email           string `json:"email"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Name != "" {
		u.Name = in.Name
	}
	if in.Email != "" {
		u.Email = strings.ToLower(strings.TrimSpace(in.Email))
	}
	if in.NewPassword != "" {
		if u.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.CurrentPassword)) != nil {
			writeErr(w, http.StatusBadRequest, "current password is incorrect")
			return
		}
		if len(in.NewPassword) < 8 {
			writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
		u.PasswordHash = string(h)
	}
	if err := s.store.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, err, "update user")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ---- users ----

// handleListUsers is open to every signed-in user so accounts can be shared with them.
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, err, "list users")
		return
	}
	if users == nil {
		users = []store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "only an admin can do that")
		return false
	}
	return true
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" {
		writeErr(w, http.StatusBadRequest, "email is required")
		return
	}
	if s.cfg.AuthMode == "local" && s.cfg.PasswordLogin && len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if in.Role != "admin" {
		in.Role = "member"
	}
	u := &store.User{Email: in.Email, Name: strings.TrimSpace(in.Name), Role: in.Role}
	if in.Password != "" {
		h, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		u.PasswordHash = string(h)
	}
	if err := s.store.CreateUser(r.Context(), u); err != nil {
		s.fail(w, err, "create user")
		return
	}
	s.log.Info("user created", "email", u.Email, "role", u.Role, "by", userFrom(r.Context()).Email)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	u, err := s.store.GetUser(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "get user")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Email != "" {
		u.Email = strings.TrimSpace(strings.ToLower(in.Email))
	}
	if in.Name != "" {
		u.Name = strings.TrimSpace(in.Name)
	}
	if in.Role == "admin" || in.Role == "member" {
		if in.Role == "member" && u.Role == "admin" {
			if n, _ := s.store.CountAdmins(r.Context()); n <= 1 {
				writeErr(w, http.StatusBadRequest, "there must be at least one admin")
				return
			}
		}
		u.Role = in.Role
	}
	if in.Password != "" {
		if len(in.Password) < 8 {
			writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		u.PasswordHash = string(h)
	}
	if err := s.store.UpdateUser(r.Context(), u); err != nil {
		s.fail(w, err, "update user")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := idParam(r, "id")
	if id == userFrom(r.Context()).ID {
		writeErr(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	target, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		s.fail(w, err, "get user")
		return
	}
	if target.Role == "admin" {
		if n, _ := s.store.CountAdmins(r.Context()); n <= 1 {
			writeErr(w, http.StatusBadRequest, "there must be at least one admin")
			return
		}
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.fail(w, err, "delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- tokens ----

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.ListAPITokens(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.fail(w, err, "list tokens")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	_ = decode(r, &in)
	if in.Name == "" {
		in.Name = "token"
	}
	plain, t, err := s.store.CreateAPIToken(r.Context(), userFrom(r.Context()).ID, in.Name)
	if err != nil {
		s.fail(w, err, "create token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": plain, "info": t})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAPIToken(r.Context(), userFrom(r.Context()).ID, idParam(r, "id")); err != nil {
		s.fail(w, err, "delete token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
