package server

import (
	"net/http"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- categories (each person has their own) ----

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListCategories(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "categories")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type categoryIn struct {
	ParentID  *int64  `json:"parent_id"`
	Name      *string `json:"name"`
	Kind      *string `json:"kind"`
	Icon      *string `json:"icon"`
	Color     *string `json:"color"`
	Archived  *bool   `json:"archived"`
	SortOrder *int    `json:"sort_order"`
}

func (in *categoryIn) apply(c *store.Category) string {
	if in.ParentID != nil {
		c.ParentID = idOrNil(in.ParentID)
	}
	if in.Name != nil {
		c.Name = strings.TrimSpace(*in.Name)
	}
	if in.Kind != nil {
		c.Kind = *in.Kind
	}
	if in.Icon != nil {
		c.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.Color != nil {
		c.Color = *in.Color
	}
	if in.Archived != nil {
		c.Archived = *in.Archived
	}
	if in.SortOrder != nil {
		c.SortOrder = *in.SortOrder
	}
	switch {
	case c.Name == "":
		return "name is required"
	case c.Kind != "expense" && c.Kind != "income" && c.Kind != "both":
		return "kind must be expense, income or both"
	case c.ParentID != nil && *c.ParentID == c.ID:
		return "a category cannot be its own parent"
	}
	return ""
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := &store.Category{UserID: actorID(r.Context()), Kind: "expense"}
	if msg := in.apply(c); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if c.Icon == "" {
		c.Icon = guessEmoji(c.Name)
	}
	if c.ParentID != nil {
		if p, err := s.store.GetCategory(r.Context(), c.UserID, *c.ParentID); err != nil || p.ParentID != nil {
			writeErr(w, http.StatusBadRequest, "the parent must be a top-level category")
			return
		}
	}
	if err := s.store.CreateCategory(r.Context(), c); err != nil {
		s.fail(w, err, "create category")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetCategory(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "category")
		return
	}
	var in categoryIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(c); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if c.ParentID != nil {
		if p, err := s.store.GetCategory(r.Context(), c.UserID, *c.ParentID); err != nil || p.ParentID != nil {
			writeErr(w, http.StatusBadRequest, "the parent must be a top-level category")
			return
		}
	}
	if err := s.store.UpdateCategory(r.Context(), c); err != nil {
		s.fail(w, err, "update category")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	var moveTo *int64
	if v := queryInt(r, "move_to"); v > 0 {
		m := int64(v)
		moveTo = &m
	}
	if err := s.store.DeleteCategory(r.Context(), actorID(r.Context()), idParam(r, "id"), moveTo); err != nil {
		s.fail(w, err, "delete category")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- tags ----

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListTags(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	uid := actorID(r.Context())
	id, err := s.store.EnsureTag(r.Context(), uid, in.Name)
	if err != nil {
		s.fail(w, err, "create tag")
		return
	}
	t, _ := s.store.GetTag(r.Context(), uid, id)
	if in.Color != "" {
		t.Color = in.Color
		_ = s.store.UpdateTag(r.Context(), t)
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTag(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "tag")
		return
	}
	var in struct {
		Name     *string `json:"name"`
		Color    *string `json:"color"`
		Archived *bool   `json:"archived"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if in.Color != nil {
		t.Color = *in.Color
	}
	if in.Archived != nil {
		t.Archived = *in.Archived
	}
	if err := s.store.UpdateTag(r.Context(), t); err != nil {
		s.fail(w, err, "update tag")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteTag(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete tag")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMergeTag(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Into int64 `json:"into"`
	}
	if err := decode(r, &in); err != nil || in.Into <= 0 {
		writeErr(w, http.StatusBadRequest, "into is required")
		return
	}
	from := idParam(r, "id")
	if from == in.Into {
		writeErr(w, http.StatusBadRequest, "pick another tag")
		return
	}
	if err := s.store.MergeTags(r.Context(), actorID(r.Context()), from, in.Into); err != nil {
		s.fail(w, err, "merge tags")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- templates (per user) ----

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListTemplates(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "templates")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type templateIn struct {
	Name        *string   `json:"name"`
	Aliases     *string   `json:"aliases"`
	Kind        *string   `json:"kind"`
	Amount      *int64    `json:"amount"`
	ClearAmount bool      `json:"clear_amount"`
	Currency    *string   `json:"currency"`
	AccountID   *int64    `json:"account_id"`
	ToAccountID *int64    `json:"to_account_id"`
	CategoryID  *int64    `json:"category_id"`
	Tags        *[]string `json:"tags"`
	Description *string   `json:"description"`
	Payee       *string   `json:"payee"`
	Notes       *string   `json:"notes"`
	Pinned      *bool     `json:"pinned"`
	SortOrder   *int      `json:"sort_order"`
}

func (s *Server) applyTemplate(r *http.Request, in *templateIn, t *store.Template) string {
	if in.Name != nil {
		t.Name = strings.TrimSpace(*in.Name)
	}
	if in.Aliases != nil {
		t.Aliases = strings.TrimSpace(*in.Aliases)
	}
	if in.Kind != nil {
		t.Kind = *in.Kind
	}
	if in.Amount != nil {
		t.Amount = in.Amount
		if *in.Amount <= 0 {
			t.Amount = nil
		}
	}
	if in.ClearAmount {
		t.Amount = nil
	}
	if in.Currency != nil {
		t.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.AccountID != nil {
		t.AccountID = idOrNil(in.AccountID)
	}
	if in.ToAccountID != nil {
		t.ToAccountID = idOrNil(in.ToAccountID)
	}
	if in.CategoryID != nil {
		t.CategoryID = idOrNil(in.CategoryID)
	}
	if in.Tags != nil {
		t.Tags = cleanTags(*in.Tags)
	}
	if in.Description != nil {
		t.Description = strings.TrimSpace(*in.Description)
	}
	if in.Payee != nil {
		t.Payee = strings.TrimSpace(*in.Payee)
	}
	if in.Notes != nil {
		t.Notes = *in.Notes
	}
	if in.Pinned != nil {
		t.Pinned = *in.Pinned
	}
	if in.SortOrder != nil {
		t.SortOrder = *in.SortOrder
	}
	switch {
	case t.Name == "":
		return "name is required"
	case !store.Valid(store.TransactionKinds, t.Kind):
		return "unknown kind"
	case t.Currency != "" && !currency.Valid(t.Currency):
		return "unknown currency"
	case t.AccountID != nil && !s.canWrite(r.Context(), *t.AccountID):
		return "you cannot book on that account"
	case t.ToAccountID != nil && !s.canWrite(r.Context(), *t.ToAccountID):
		return "you cannot book on the destination account"
	case in.CategoryID != nil && t.CategoryID != nil && !s.store.OwnsCategories(r.Context(), t.OwnerID, []int64{*t.CategoryID}):
		return "unknown category"
	case t.Kind == "transfer" && t.ToAccountID == nil:
		return "a transfer template needs a destination account"
	}
	return ""
}

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var in templateIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	t := &store.Template{OwnerID: actorID(r.Context()), Kind: "expense", Tags: []string{}}
	if msg := s.applyTemplate(r, &in, t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateTemplate(r.Context(), t); err != nil {
		s.fail(w, err, "create template")
		return
	}
	t, _ = s.store.GetTemplate(r.Context(), t.ID)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) ownTemplate(w http.ResponseWriter, r *http.Request) *store.Template {
	t, err := s.store.GetTemplate(r.Context(), idParam(r, "id"))
	if err != nil || t.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return t
}

func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	t := s.ownTemplate(w, r)
	if t == nil {
		return
	}
	var in templateIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.applyTemplate(r, &in, t); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateTemplate(r.Context(), t); err != nil {
		s.fail(w, err, "update template")
		return
	}
	t, _ = s.store.GetTemplate(r.Context(), t.ID)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	t := s.ownTemplate(w, r)
	if t == nil {
		return
	}
	if err := s.store.DeleteTemplate(r.Context(), t.ID); err != nil {
		s.fail(w, err, "delete template")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUseTemplate books a template in one tap; the body may override the
// amount (in the template's currency), date and description.
func (s *Server) handleUseTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := s.ownTemplate(w, r)
	if t == nil {
		return
	}
	var over struct {
		Amount      *int64  `json:"amount"`
		Date        *string `json:"date"`
		Description *string `json:"description"`
		AccountID   *int64  `json:"account_id"`
	}
	_ = decode(r, &over)
	in := txIn{Kind: &t.Kind, AccountID: t.AccountID, ToAccountID: t.ToAccountID, CategoryID: t.CategoryID, Tags: &t.Tags, Payee: &t.Payee,
		Notes: &t.Notes, Source: "template", TemplateID: t.ID}
	desc := t.Description
	if desc == "" {
		desc = t.Name
	}
	in.Description = &desc
	in.Amount = t.Amount
	if over.Amount != nil {
		in.Amount = over.Amount
	}
	if t.Currency != "" {
		in.Currency = &t.Currency
	}
	if over.Date != nil {
		in.Date = over.Date
	}
	if over.Description != nil && *over.Description != "" {
		in.Description = over.Description
	}
	if over.AccountID != nil {
		in.AccountID = over.AccountID
	}
	if in.AccountID == nil || *in.AccountID == 0 {
		st, _ := s.store.GetSettings(ctx, actorID(ctx))
		accts, _ := s.store.ListAccounts(ctx, actorID(ctx), false)
		var wr []store.Account
		for _, a := range accts {
			if a.Role != "viewer" {
				wr = append(wr, a)
			}
		}
		d := s.defaultAccount(ctx, st, wr)
		in.AccountID = &d
	}
	if in.Amount == nil {
		writeErr(w, http.StatusBadRequest, "this template has no amount — type one")
		return
	}
	tx, msg, err := s.createTx(ctx, &in)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err != nil {
		s.fail(w, err, "use template")
		return
	}
	writeJSON(w, http.StatusCreated, tx)
}

// guessEmoji picks an icon for a category from its name (English and Bosnian/Croatian/Serbian).
func guessEmoji(name string) string {
	n := strings.ToLower(name)
	table := []struct {
		words []string
		emoji string
	}{
		{[]string{"pekar", "bakery", "kifl", "hljeb", "bread"}, "🥐"},
		{[]string{"restoran", "restaurant", "kafa", "coffee", "cafe", "kafić", "kafic", "bar", "dining", "eat out"}, "🍽️"},
		{[]string{"hrana", "food", "grocer", "namirnic", "market", "supermarket", "nabavka"}, "🛒"},
		{[]string{"alkohol", "alcohol", "pivo", "beer", "wine", "vino"}, "🍷"},
		{[]string{"auto", "car", "gorivo", "fuel", "benzin", "parking", "vozilo"}, "🚗"},
		{[]string{"racun", "račun", "bill", "util", "struja", "voda", "rent", "kirija", "stan"}, "🧾"},
		{[]string{"aplikac", "app", "software", "subscription", "pretplat", "stream"}, "📱"},
		{[]string{"apotek", "pharma", "lijek", "lek", "health", "zdravlj", "doktor", "doctor"}, "💊"},
		{[]string{"pet", "pets", "ljubim", "mačk", "mack", "pas", "dog", "cat", "zivotinj", "životinj"}, "🐾"},
		{[]string{"odjec", "odjeć", "odec", "cloth", "fashion", "obuć", "obuc", "shoe"}, "👕"},
		{[]string{"kozmet", "cosmetic", "beauty", "frizer", "hair"}, "💄"},
		{[]string{"uređaj", "uredjaj", "device", "electronic", "tech", "gadget", "kompjuter", "laptop"}, "💻"},
		{[]string{"alat", "tool", "hardware", "majstor"}, "🔧"},
		{[]string{"fasung", "kuća", "kuca", "home", "house", "namještaj", "namjestaj", "furniture"}, "🏠"},
		{[]string{"gitar", "music", "muzik", "instrument"}, "🎸"},
		{[]string{"psihoterap", "therapy", "terapij"}, "🧘"},
		{[]string{"alimentac", "child", "dijete", "djeca", "kid", "škol", "skol"}, "🧒"},
		{[]string{"selo", "village", "vikendic", "garden", "bašt", "bast"}, "🌾"},
		{[]string{"travel", "putovan", "trip", "hotel", "smještaj", "smjestaj", "avion", "flight"}, "✈️"},
		{[]string{"transfer", "prenos"}, "🔁"},
		{[]string{"plata", "salary", "wage", "income", "prihod", "dobit"}, "💰"},
		{[]string{"porez", "tax", "sp", "firma", "business", "knjigovod"}, "🏢"},
		{[]string{"poklon", "gift", "donac", "donation"}, "🎁"},
		{[]string{"sport", "gym", "teretan", "fitness"}, "🏋️"},
		{[]string{"edu", "knjig", "book", "kurs", "course"}, "📚"},
		{[]string{"insur", "osigur"}, "🛡️"},
		{[]string{"ostalo", "other", "misc", "razno"}, "📦"},
	}
	words := strings.FieldsFunc(n, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '/' || r == '&' })
	for _, row := range table {
		for _, w := range row.words {
			if len(w) <= 3 {
				// short words only as a whole word ("pet" must not hit "petrol")
				for _, x := range words {
					if x == w {
						return row.emoji
					}
				}
				continue
			}
			if strings.HasPrefix(n, w) || strings.Contains(n, w) {
				return row.emoji
			}
		}
	}
	return ""
}
