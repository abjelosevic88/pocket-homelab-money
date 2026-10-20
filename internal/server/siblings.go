package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-money/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- Pocket Travel ----
//
// Like Pocket Garage: each person chooses how Money reaches their Pocket
// Travel. Through Pocket Core, Money acts for the person with an app link
// (trips:read), so everyone gets their own trips with nothing to paste; a
// URL + token typed into Settings (the way it always worked, and the only
// way without Core) is the direct connection; or off.

var travelScopes = []string{"trips:read"}

// linkUses are the Pocket Core app links Money asks for in its manifest.
var linkUses = []core.Use{
	{App: "travel", Scopes: travelScopes, Reason: "Put the running trip's tag on new expenses"},
}

// Connection modes, chosen per person and per app in Settings.
const (
	modeCore   = "core"
	modeDirect = "direct"
	modeOff    = "off"
)

// connMode is the mode in effect: the chosen one, else direct when a token
// was entered (how it always worked), else Pocket Core.
func connMode(mode, token string) string {
	switch mode {
	case modeCore, modeDirect, modeOff:
		return mode
	}
	if token != "" {
		return modeDirect
	}
	return modeCore
}

// siblingConn is how Money reaches one person's Pocket Travel.
type siblingConn struct {
	client *pocket.Client
	via    string // direct | core
	public string // what a browser opens
}

func linkReason(app, reason string) string {
	switch reason {
	case "off_person":
		return "you switched the Money → " + app + " link off in Pocket Core (Settings → Apps acting for me)"
	case "off_admin":
		return "the Money → " + app + " link is switched off in Pocket Core (Links)"
	case "target_missing":
		return app + " is not registered with Pocket Core"
	case "not_offered":
		return "this " + app + " version does not accept app links yet"
	case "not_declared":
		return "Pocket Core has not seen this Money version yet; it re-registers within a minute of starting"
	case "not_in_audience":
		return "you do not have access to " + app + " in Pocket Core"
	case "unknown_user":
		return "your email is not a person in Pocket Core"
	}
	return ""
}

// siblingFor connects to a Pocket app for a person in the mode in effect.
// Through Core it asks for a fresh five-minute token every time (nothing is
// kept), so a link switched off stops the next call.
func (s *Server) siblingFor(ctx context.Context, u *store.User, slug, app string, scopes []string, mode, url, token, public string) (*siblingConn, error) {
	if u == nil {
		return nil, errors.New("nobody to act for")
	}
	switch connMode(mode, token) {
	case modeOff:
		return nil, fmt.Errorf("%s is switched off in Settings → Integrations", app)
	case modeDirect:
		if token == "" {
			return nil, fmt.Errorf("%s is set to a direct connection but has no token: add one in Settings → Integrations, or switch to Pocket Core", app)
		}
		base := url
		if base == "" {
			base = s.resolveSibling(ctx, slug)
		}
		if base == "" {
			return nil, fmt.Errorf("set the %s URL in Settings → Integrations", app)
		}
		return &siblingConn{client: pocket.New(base, token, 15*time.Second), via: modeDirect, public: firstNonEmpty(public, base)}, nil
	}
	if !s.core.Configured() {
		return nil, fmt.Errorf("%s through Pocket Core: there is no Pocket Core on this box; use a direct connection", app)
	}
	tok, err := s.core.ActAs(ctx, u.Email, slug, scopes...)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			if t := linkReason(app, le.Reason); t != "" {
				return nil, errors.New(t)
			}
		}
		return nil, err
	}
	return &siblingConn{client: pocket.New(tok.URL, tok.AccessToken, 15*time.Second), via: modeCore, public: firstNonEmpty(public, tok.PublicURL, tok.URL)}, nil
}

// travelFor is the person's own Pocket Travel, in their own mode.
func (s *Server) travelFor(ctx context.Context, u *store.User) (*siblingConn, error) {
	if u == nil {
		return nil, errors.New("nobody to act for")
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	return s.siblingFor(ctx, u, "travel", "Pocket Travel", travelScopes, st.TravelMode, st.TravelURL, st.TravelToken, st.TravelPublicURL)
}

// connStatus says, without issuing anything, how a person reaches a Pocket
// app: the mode in effect, whether it works ("core" / "direct" / "" for
// not connected) and why not.
func (s *Server) connStatus(ctx context.Context, u *store.User, slug, app, mode, token string, scopes []string) (eff, via, detail string) {
	eff = connMode(mode, token)
	switch eff {
	case modeOff:
		return eff, "", "switched off"
	case modeDirect:
		if token == "" {
			return eff, "", "no token yet"
		}
		return eff, modeDirect, ""
	}
	if u == nil || !s.core.Configured() {
		return eff, "", "there is no Pocket Core on this box"
	}
	on, reason := s.core.LinkOn(ctx, u.Email, slug, scopes...)
	if on {
		return eff, modeCore, ""
	}
	if t := linkReason(app, reason); t != "" {
		return eff, "", t
	}
	return eff, "", reason
}
