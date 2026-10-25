package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- where money was spent ----
//
// GET /places lists the purchases that have a location (from a scanned
// receipt's shop, or set by hand), compact enough to draw on a map and to
// match against the places another app recorded (Pocket Movement's visits,
// Pocket Day's stops). Linked apps read it with transactions:read.

type placeItem struct {
	ID           int64   `json:"id"`
	Date         string  `json:"date"`
	Time         string  `json:"time"`
	TS           int64   `json:"ts"` // the moment in Unix seconds (noon when the time is unknown)
	TimeKnown    bool    `json:"time_known"`
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	Kind         string  `json:"kind"`
	Payee        string  `json:"payee"`
	Description  string  `json:"description"`
	Amount       int64   `json:"amount"` // minor units of Currency
	Currency     string  `json:"currency"`
	AmountHome   int64   `json:"amount_home"`
	Category     string  `json:"category"`
	CategoryIcon string  `json:"category_icon"`
	ReceiptID    *int64  `json:"receipt_id,omitempty"`
	Shop         string  `json:"shop,omitempty"`
	Address      string  `json:"address,omitempty"`
	City         string  `json:"city,omitempty"`
	Items        int     `json:"items,omitempty"`
	URL          string  `json:"url"`
}

func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.TxFilter{AccountIDs: s.visibleIDs(ctx), From: q.Get("from"), To: q.Get("to"), Limit: 1000, Oldest: true, HasLocation: true}
	if n := queryInt(r, "limit"); n > 0 && n < 1000 {
		f.Limit = n
	}
	if !validDate(f.From) || !validDate(f.To) {
		writeErr(w, http.StatusBadRequest, "from and to are YYYY-MM-DD")
		return
	}
	txs, _, err := s.store.ListTransactions(ctx, f)
	if err != nil {
		s.fail(w, err, "places")
		return
	}
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	out := make([]placeItem, 0, len(txs))
	receipts := map[int64]*store.Receipt{}
	for _, t := range txs {
		if t.Lat == nil || t.Lng == nil || t.Kind == "transfer" {
			continue
		}
		it := placeItem{ID: t.ID, Date: t.Date, Time: t.Time, Lat: *t.Lat, Lng: *t.Lng, Kind: t.Kind, Payee: t.Payee, Description: t.Description,
			Amount: t.Amount, Currency: t.Currency, AmountHome: t.AmountHome, Category: t.CategoryName, CategoryIcon: t.CategoryIcon,
			ReceiptID: t.ReceiptID, URL: base + "/transactions/" + strconv.FormatInt(t.ID, 10)}
		clock := t.Time
		if clock == "" {
			clock = "12:00"
		} else {
			it.TimeKnown = true
		}
		if ts, err := time.ParseInLocation("2006-01-02 15:04", t.Date+" "+clock, time.Local); err == nil {
			it.TS = ts.Unix()
		}
		if t.ReceiptID != nil {
			rec, ok := receipts[*t.ReceiptID]
			if !ok {
				rec, _ = s.store.GetReceipt(ctx, *t.ReceiptID)
				receipts[*t.ReceiptID] = rec
			}
			if rec != nil {
				it.Items = len(rec.Items)
				if m := rec.Merchant; m != nil {
					it.Shop, it.Address, it.City = payeeFor(rec), m.Address, m.City
				}
			}
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "home_currency": st.HomeCurrency})
}
