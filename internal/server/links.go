package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core): other Pocket
// apps may act here for a person with a pcx_ token, but only within these
// scopes. Everything not listed in linkRoutes stays closed to them, and they
// may change or delete only transactions they created themselves.

var linkOffers = []core.Offer{
	{Scope: "accounts:read", Name: "See accounts and categories", Description: "List your accounts (names, currencies, balances) and categories, so the app can ask where a cost is booked."},
	{Scope: "transactions:write", Name: "Book transactions", Description: "Create transactions on accounts you can book on, and change or delete only the ones that app created."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/accounts$`), "accounts:read"},
	{"GET", regexp.MustCompile(`^/api/v1/accounts/\d+/categories$`), "accounts:read"},
	{"GET", regexp.MustCompile(`^/api/v1/categories$`), "accounts:read"},
	{"POST", regexp.MustCompile(`^/api/v1/transactions$`), "transactions:write"},
	{"GET PUT DELETE", regexp.MustCompile(`^/api/v1/transactions/\d+$`), "transactions:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token may make this request.
func linkAllows(p *principal, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range p.Scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkApp returns the acting app's slug when the request came in on a link token.
func linkApp(p *principal) string {
	if p != nil && p.Kind == "link" {
		return p.LinkApp
	}
	return ""
}

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey).(*principal)
	return p
}
