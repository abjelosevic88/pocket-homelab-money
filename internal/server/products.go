package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- products bought, from receipt items ----
//
// Every receipt line belongs to a product (the same article across
// receipts, by its folded name). Products sit in the person's groups
// ("Milk"), matched by keywords or placed by hand, which answers "how much
// milk did I buy and what did it cost".

func (s *Server) itemFilter(r *http.Request) store.ItemStatsFilter {
	q := r.URL.Query()
	f := store.ItemStatsFilter{UserID: actorID(r.Context()), From: q.Get("from"), To: q.Get("to")}
	if g := q.Get("group"); g == "none" || g == "0" {
		f.Ungrouped = true
	} else {
		f.GroupID, _ = strconv.ParseInt(g, 10, 64)
	}
	f.ProductID, _ = strconv.ParseInt(q.Get("product"), 10, 64)
	f.MerchantID, _ = strconv.ParseInt(q.Get("merchant"), 10, 64)
	return f
}

func (s *Server) handleProductGroups(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.GroupStats(r.Context(), s.itemFilter(r))
	if err != nil {
		s.fail(w, err, "product groups")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type groupIn struct {
	Name       *string `json:"name"`
	Icon       *string `json:"icon"`
	Keywords   *string `json:"keywords"`
	CategoryID *int64  `json:"category_id"`
	SortOrder  *int    `json:"sort_order"`
}

func (s *Server) applyGroup(r *http.Request, in groupIn, g *store.ProductGroup) string {
	if in.Name != nil {
		g.Name = strings.TrimSpace(*in.Name)
	}
	if in.Icon != nil {
		g.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.Keywords != nil {
		g.Keywords = strings.Join(strings.FieldsFunc(*in.Keywords, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }), ",")
		parts := []string{}
		for _, k := range strings.Split(g.Keywords, ",") {
			if k = strings.TrimSpace(k); k != "" {
				parts = append(parts, k)
			}
		}
		g.Keywords = strings.Join(parts, ", ")
	}
	if in.CategoryID != nil {
		g.CategoryID = idOrNil(in.CategoryID)
		if g.CategoryID != nil {
			if _, err := s.store.GetCategory(r.Context(), actorID(r.Context()), *g.CategoryID); err != nil {
				return "unknown category"
			}
		}
	}
	if in.SortOrder != nil {
		g.SortOrder = *in.SortOrder
	}
	if g.Name == "" {
		return "a group needs a name"
	}
	return ""
}

func (s *Server) handleCreateProductGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in groupIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	g := &store.ProductGroup{UserID: actorID(ctx)}
	if msg := s.applyGroup(r, in, g); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateGroup(ctx, g); err != nil {
		s.fail(w, err, "create group")
		return
	}
	moved, _ := s.store.RegroupProducts(ctx, g.UserID)
	writeJSON(w, http.StatusCreated, map[string]any{"group": g, "moved": moved})
}

func (s *Server) handleUpdateProductGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	g, err := s.store.GetGroup(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "group")
		return
	}
	var in groupIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.applyGroup(r, in, g); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateGroup(ctx, g); err != nil {
		s.fail(w, err, "update group")
		return
	}
	moved, _ := s.store.RegroupProducts(ctx, g.UserID)
	writeJSON(w, http.StatusOK, map[string]any{"group": g, "moved": moved})
}

func (s *Server) handleDeleteProductGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := s.store.GetGroup(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "group")
		return
	}
	if err := s.store.DeleteGroup(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStarterGroups adds the suggested groups the person does not have yet.
func (s *Server) handleStarterGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	have, err := s.store.ListGroups(ctx, uid)
	if err != nil {
		s.fail(w, err, "groups")
		return
	}
	names := map[string]bool{}
	for _, g := range have {
		names[strings.ToLower(g.Name)] = true
	}
	added := 0
	for i, sg := range fiscal.StarterGroups {
		if names[strings.ToLower(sg.Name)] {
			continue
		}
		g := &store.ProductGroup{UserID: uid, Name: sg.Name, Icon: sg.Icon, Keywords: sg.Keywords, SortOrder: len(have) + i}
		if err := s.store.CreateGroup(ctx, g); err == nil {
			added++
		}
	}
	moved, _ := s.store.RegroupProducts(ctx, uid)
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "moved": moved})
}

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ProductStats(r.Context(), s.itemFilter(r), r.URL.Query().Get("q"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "products")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.store.GetProduct(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "product")
		return
	}
	var in struct {
		Name    *string `json:"name"`
		GroupID *int64  `json:"group_id"` // 0 = no group
		Auto    bool    `json:"auto"`     // back to keyword matching
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Auto {
		p.GroupLocked = false
	} else if in.GroupID != nil {
		p.GroupID = idOrNil(in.GroupID)
		p.GroupLocked = true
		if p.GroupID != nil {
			if _, err := s.store.GetGroup(ctx, p.UserID, *p.GroupID); err != nil {
				writeErr(w, http.StatusBadRequest, "unknown group")
				return
			}
		}
	}
	if err := s.store.UpdateProduct(ctx, p); err != nil {
		s.fail(w, err, "product")
		return
	}
	if in.Auto {
		_, _ = s.store.RegroupProducts(ctx, p.UserID)
		p, _ = s.store.GetProduct(ctx, p.UserID, p.ID)
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handlePurchases(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Purchases(r.Context(), s.itemFilter(r), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "purchases")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleProductSummary is one total for a group or product (the strip above
// a filtered transaction list).
func (s *Server) handleProductSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := s.itemFilter(r)
	q := r.URL.Query()
	if v := q.Get("product_group"); v != "" && f.GroupID == 0 {
		f.GroupID, _ = strconv.ParseInt(v, 10, 64)
	}
	var out store.ItemStats
	switch {
	case f.ProductID > 0:
		list, err := s.store.ProductStats(ctx, f, "", 1)
		if err != nil {
			s.fail(w, err, "summary")
			return
		}
		p, err := s.store.GetProduct(ctx, f.UserID, f.ProductID)
		if err != nil {
			s.fail(w, err, "product")
			return
		}
		out = store.ItemStats{Key: p.ID, Name: p.Name, GroupID: p.GroupID}
		if len(list) > 0 {
			out = list[0]
		}
	case f.GroupID > 0 || f.Ungrouped:
		all, err := s.store.GroupStats(ctx, f)
		if err != nil {
			s.fail(w, err, "summary")
			return
		}
		for _, x := range all {
			if x.Key == f.GroupID {
				out = x
			}
		}
		if out.Name == "" {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "pass product_group or product")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
