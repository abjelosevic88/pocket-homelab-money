package server

// A Firefly III-compatible slice of the API, mounted at /firefly/api/v1, plus
// Firefly Pico's /api/transaction-templates. Everything that talked to
// Firefly (the Raycast extension, the Pocket Homelab phone app, Pocket
// Garage/Travel/Day, the ops scripts) only needs its Firefly URL changed to
// https://<money>/firefly and a Pocket Money API token as the bearer.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func (s *Server) compatRoutes(r chi.Router) {
	r.Route("/firefly/api", func(r chi.Router) {
		r.Use(s.compatAuth)
		r.Get("/transaction-templates", s.ffTemplates)
		r.Route("/v1", func(r chi.Router) {
			r.Get("/about", s.ffAbout)
			r.Get("/about/user", s.ffAboutUser)
			r.Get("/accounts", s.ffAccounts)
			r.Get("/accounts/{id}", s.ffAccount)
			r.Get("/accounts/{id}/transactions", s.ffAccountTransactions)
			r.Get("/categories", s.ffCategories)
			r.Get("/categories/{id}", s.ffCategory)
			r.Get("/tags", s.ffTags)
			r.Get("/tags/{tag}", s.ffTag)
			r.Get("/tags/{tag}/transactions", s.ffTagTransactions)
			r.Get("/transactions", s.ffTransactions)
			r.Post("/transactions", s.ffCreateTransaction)
			r.Get("/transactions/{id}", s.ffTransaction)
			r.Put("/transactions/{id}", s.ffUpdateTransaction)
			r.Delete("/transactions/{id}", s.ffDeleteTransaction)
			r.Get("/bills", s.ffBills)
			r.Get("/subscriptions", s.ffBills)
			r.Get("/currencies", s.ffCurrencies)
			r.Get("/exchange-rates", s.ffExchangeRates)
			r.Get("/insight/expense/total", s.ffInsight("expense"))
			r.Get("/insight/income/total", s.ffInsight("income"))
			r.Get("/budgets", s.ffEmpty)
			r.Get("/piggy-banks", s.ffEmpty)
			r.Get("/rules", s.ffEmpty)
			r.Get("/recurrences", s.ffEmpty)
		})
	})
}

// compatAuth accepts the same credentials as the native API (bearer token
// first) and answers in Firefly's error shape.
func (s *Server) compatAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.resolveUser(r)
		if err != nil || u == nil {
			ffError(w, http.StatusUnauthorized, "Unauthenticated.")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func ffError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "exception": ""})
}

func ffJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type ffItem struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes any    `json:"attributes"`
	Links      any    `json:"links,omitempty"`
}

func pageParams(r *http.Request) (int, int) {
	page := queryInt(r, "page")
	limit := queryInt(r, "limit")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 1000 {
		limit = 50
	}
	return page, limit
}

func ffMeta(total, page, limit int) map[string]any {
	pages := (total + limit - 1) / limit
	if pages < 1 {
		pages = 1
	}
	count := limit
	if page*limit > total {
		count = max(0, total-(page-1)*limit)
	}
	return map[string]any{"pagination": map[string]any{"total": total, "count": count, "per_page": limit, "current_page": page, "total_pages": pages}}
}

func ffPage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	page, limit := pageParams(r)
	lo := min(len(items), (page-1)*limit)
	hi := min(len(items), lo+limit)
	ffJSON(w, http.StatusOK, map[string]any{"data": items[lo:hi], "meta": ffMeta(len(items), page, limit)})
}

// ffNum formats minor units the way Firefly does ("12.500000000000").
func ffNum(minor int64, code string) string {
	d := currency.Decimals(code)
	return strconv.FormatFloat(float64(minor)/math.Pow10(d), 'f', 12, 64)
}

func (s *Server) ffAbout(w http.ResponseWriter, r *http.Request) {
	ffJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"version": "6.7.9", "api_version": "6.7.9", "php_version": "pocket-money " + Version, "os": "Linux", "driver": "sqlite"}})
}

func (s *Server) ffAboutUser(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ffJSON(w, http.StatusOK, map[string]any{"data": ffItem{Type: "users", ID: strconv.FormatInt(u.ID, 10), Attributes: map[string]any{"email": u.Email, "role": "owner", "blocked": false}}})
}

func ffAccountRole(kind string) string {
	switch kind {
	case "savings":
		return "savingAsset"
	case "cash":
		return "cashWalletAsset"
	case "credit":
		return "ccAsset"
	}
	return "defaultAsset"
}

func (s *Server) ffAccountItem(a store.Account) ffItem {
	typ := "asset"
	if store.IsLiability(a.Kind) {
		typ = "liabilities"
	}
	return ffItem{Type: "accounts", ID: strconv.FormatInt(a.ID, 10), Attributes: map[string]any{
		"created_at": a.CreatedAt, "updated_at": a.UpdatedAt, "active": !a.Archived, "order": a.SortOrder, "name": a.Name, "type": typ,
		"account_role": ffAccountRole(a.Kind), "currency_id": a.Currency, "currency_code": a.Currency, "currency_symbol": a.Currency,
		"currency_decimal_places": currency.Decimals(a.Currency), "current_balance": ffNum(a.Balance, a.Currency), "current_balance_date": time.Now().Format(time.RFC3339),
		"notes": a.Notes, "iban": a.Number, "account_number": a.Number, "include_net_worth": a.IncludeNetWorth, "opening_balance": ffNum(a.OpeningBalance, a.Currency),
		"opening_balance_date": a.OpeningDate, "virtual_balance": "0", "credit_card_type": nil, "liability_type": liabType(a),
		"liability_direction": liabDir(a), "interest": strconv.FormatFloat(a.InterestRate, 'f', -1, 64), "interest_period": "yearly",
	}}
}

func (s *Server) ffAccounts(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("type")
	accts, err := s.store.ListAccounts(r.Context(), actorID(r.Context()), true)
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	items := []ffItem{}
	for _, a := range accts {
		isLiab := store.IsLiability(a.Kind)
		switch t {
		case "", "all", "asset", "assets":
			if (t == "asset" || t == "assets") && isLiab {
				continue
			}
		case "liabilities", "liability", "loan", "debt", "mortgage":
			if !isLiab {
				continue
			}
		default: // expense / revenue / cash accounts do not exist as accounts here
			continue
		}
		items = append(items, s.ffAccountItem(a))
	}
	ffPage(w, r, items)
}

func (s *Server) ffAccount(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if !s.canSee(r.Context(), id) {
		ffError(w, 404, "Resource not found")
		return
	}
	a, err := s.store.GetAccount(r.Context(), id)
	if err != nil {
		ffError(w, 404, "Resource not found")
		return
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": s.ffAccountItem(*a)})
}

func (s *Server) ffCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.ListCategories(r.Context())
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	items := []ffItem{}
	for _, c := range cats {
		items = append(items, ffItem{Type: "categories", ID: strconv.FormatInt(c.ID, 10), Attributes: map[string]any{"name": c.Name, "notes": nil, "created_at": "", "updated_at": ""}})
	}
	ffPage(w, r, items)
}

func (s *Server) ffCategory(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.GetCategory(r.Context(), idParam(r, "id"))
	if err != nil {
		ffError(w, 404, "Resource not found")
		return
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": ffItem{Type: "categories", ID: strconv.FormatInt(c.ID, 10), Attributes: map[string]any{"name": c.Name, "notes": nil}}})
}

func (s *Server) ffTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	items := []ffItem{}
	for _, t := range tags {
		items = append(items, ffItem{Type: "tags", ID: strconv.FormatInt(t.ID, 10), Attributes: map[string]any{"tag": t.Name, "date": nil, "description": nil, "created_at": t.CreatedAt}})
	}
	ffPage(w, r, items)
}

// tagParam accepts a tag id or a tag name, like Firefly does.
func (s *Server) tagParam(r *http.Request) (*store.Tag, error) {
	v := chi.URLParam(r, "tag")
	if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		if t, err := s.store.GetTag(r.Context(), id); err == nil {
			return t, nil
		}
	}
	return s.store.GetTagByName(r.Context(), v)
}

func (s *Server) ffTag(w http.ResponseWriter, r *http.Request) {
	t, err := s.tagParam(r)
	if err != nil {
		ffError(w, 404, "Resource not found")
		return
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": ffItem{Type: "tags", ID: strconv.FormatInt(t.ID, 10), Attributes: map[string]any{"tag": t.Name}}})
}

func ffKindFilter(typ string) string {
	switch strings.ToLower(typ) {
	case "withdrawal", "withdrawals", "expense", "expenses":
		return "expense"
	case "deposit", "deposits", "income", "revenue":
		return "income"
	case "transfer", "transfers":
		return "transfer"
	}
	return ""
}

func (s *Server) ffTxList(w http.ResponseWriter, r *http.Request, f store.TxFilter) {
	q := r.URL.Query()
	f.AccountIDs = s.visibleIDs(r.Context())
	f.Kind = ffKindFilter(q.Get("type"))
	f.From, f.To = q.Get("start"), q.Get("end")
	page, limit := pageParams(r)
	f.Limit, f.Offset = limit, (page-1)*limit
	txs, tot, err := s.store.ListTransactions(r.Context(), f)
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	items := make([]ffItem, 0, len(txs))
	for i := range txs {
		items = append(items, s.ffGroup(&txs[i]))
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": items, "meta": ffMeta(tot.Count, page, limit)})
}

func (s *Server) ffTransactions(w http.ResponseWriter, r *http.Request) {
	s.ffTxList(w, r, store.TxFilter{})
}

func (s *Server) ffAccountTransactions(w http.ResponseWriter, r *http.Request) {
	id := idParam(r, "id")
	if !s.canSee(r.Context(), id) {
		ffError(w, 404, "Resource not found")
		return
	}
	s.ffTxList(w, r, store.TxFilter{AccountID: id})
}

func (s *Server) ffTagTransactions(w http.ResponseWriter, r *http.Request) {
	t, err := s.tagParam(r)
	if err != nil {
		ffJSON(w, http.StatusOK, map[string]any{"data": []any{}, "meta": ffMeta(0, 1, 50)})
		return
	}
	s.ffTxList(w, r, store.TxFilter{Tag: t.Name})
}

func ffDate(date, hm string) string {
	if hm == "" {
		hm = "00:00"
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+hm, time.Local)
	if err != nil {
		return date + "T00:00:00+00:00"
	}
	return t.Format("2006-01-02T15:04:05-07:00")
}

// ffGroup renders a transaction as a Firefly transaction group with one split.
func (s *Server) ffGroup(t *store.Transaction) ffItem {
	id := strconv.FormatInt(t.ID, 10)
	acct := map[string]any{"id": strconv.FormatInt(t.AccountID, 10), "name": t.AccountName, "type": "Asset account"}
	other := map[string]any{"id": nil, "name": firstNonEmpty(t.Payee, "(cash)"), "type": "Expense account"}
	var src, dst map[string]any
	typ := "withdrawal"
	switch t.Kind {
	case "income":
		typ = "deposit"
		other["type"] = "Revenue account"
		src, dst = other, acct
	case "transfer":
		typ = "transfer"
		to := map[string]any{"id": "", "name": t.ToAccountName, "type": "Asset account"}
		if t.ToAccountID != nil {
			to["id"] = strconv.FormatInt(*t.ToAccountID, 10)
		}
		src, dst = acct, to
	default:
		src, dst = acct, other
	}
	var fa, fc any
	if t.ForeignAmount > 0 {
		fa, fc = ffNum(t.ForeignAmount, t.ForeignCurrency), t.ForeignCurrency
	}
	if t.Kind == "transfer" && t.ToCurrency != "" && t.ToCurrency != t.Currency {
		fa, fc = ffNum(t.ToAmount, t.ToCurrency), t.ToCurrency
	}
	var catID, catName, subID, subName, notes any
	if t.CategoryID != nil {
		catID, catName = strconv.FormatInt(*t.CategoryID, 10), t.CategoryName
	}
	if t.SubscriptionID != nil {
		subID, subName = strconv.FormatInt(*t.SubscriptionID, 10), t.SubscriptionNm
	}
	if t.Notes != "" {
		notes = t.Notes
	}
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	split := map[string]any{
		"user": "1", "transaction_journal_id": id, "type": typ, "date": ffDate(t.Date, t.Time), "order": 0,
		"currency_id": t.Currency, "currency_code": t.Currency, "currency_symbol": t.Currency, "currency_name": t.Currency, "currency_decimal_places": currency.Decimals(t.Currency),
		"foreign_currency_id": fc, "foreign_currency_code": fc, "foreign_currency_decimal_places": nil, "amount": ffNum(t.Amount, t.Currency), "foreign_amount": fa,
		"description": t.Description, "source_id": src["id"], "source_name": src["name"], "source_type": src["type"], "source_iban": nil,
		"destination_id": dst["id"], "destination_name": dst["name"], "destination_type": dst["type"], "destination_iban": nil,
		"budget_id": nil, "budget_name": nil, "category_id": catID, "category_name": catName,
		"bill_id": subID, "bill_name": subName, "subscription_id": subID, "subscription_name": subName,
		"reconciled": false, "notes": notes, "tags": tags, "internal_reference": nil, "external_id": nil, "external_url": nil,
		"original_source": "pocket-money-" + Version, "recurrence_id": nil, "has_attachments": t.AttachmentCount > 0,
		"latitude": t.Lat, "longitude": t.Lng, "zoom_level": nil,
		"pc_amount": nil, "primary_currency_code": nil,
	}
	splits := []any{split}
	var title any
	if len(t.Splits) > 0 {
		// one Firefly split per part; journal ids after the first are made unique
		title = t.Description
		splits = splits[:0]
		for i, sp := range t.Splits {
			p := map[string]any{}
			for k, v := range split {
				p[k] = v
			}
			if i > 0 {
				p["transaction_journal_id"] = strconv.FormatInt(1_000_000_000+sp.ID, 10)
			}
			p["order"] = i
			p["amount"] = ffNum(sp.Amount, t.Currency)
			if sp.ForeignAmount > 0 {
				p["foreign_amount"] = ffNum(sp.ForeignAmount, t.ForeignCurrency)
			}
			p["category_id"], p["category_name"] = nil, nil
			if sp.CategoryID != nil {
				p["category_id"], p["category_name"] = strconv.FormatInt(*sp.CategoryID, 10), sp.CategoryName
			}
			if sp.Note != "" {
				p["description"] = sp.Note
			}
			splits = append(splits, p)
		}
	}
	return ffItem{Type: "transactions", ID: id, Attributes: map[string]any{"created_at": t.CreatedAt, "updated_at": t.UpdatedAt, "user": "1", "group_title": title, "transactions": splits},
		Links: map[string]any{"self": "/api/v1/transactions/" + id}}
}

func (s *Server) ffTransaction(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTransaction(r.Context(), idParam(r, "id"))
	if err != nil || !s.canSee(r.Context(), t.AccountID) {
		ffError(w, 404, "Resource not found")
		return
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": s.ffGroup(t)})
}

// ffSplitIn is what clients POST.
type ffSplitIn struct {
	Type            string          `json:"type"`
	Date            string          `json:"date"`
	Amount          json.RawMessage `json:"amount"`
	Description     string          `json:"description"`
	CurrencyCode    string          `json:"currency_code"`
	ForeignAmount   json.RawMessage `json:"foreign_amount"`
	ForeignCurrency string          `json:"foreign_currency_code"`
	SourceID        json.RawMessage `json:"source_id"`
	SourceName      string          `json:"source_name"`
	DestinationID   json.RawMessage `json:"destination_id"`
	DestinationName string          `json:"destination_name"`
	CategoryID      json.RawMessage `json:"category_id"`
	CategoryName    string          `json:"category_name"`
	Tags            []string        `json:"tags"`
	Notes           string          `json:"notes"`
	BillID          json.RawMessage `json:"bill_id"`
}

func rawString(b json.RawMessage) string {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return ""
	}
	return strings.Trim(s, `"`)
}

func rawFloat(b json.RawMessage) (float64, bool) {
	v := rawString(b)
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
	return f, err == nil
}

var reCurrencyTag = regexp.MustCompile(`\[currency:[A-Za-z]{3}\]`)

// ffToIn maps a Firefly split onto our transaction input.
func (s *Server) ffToIn(ctx context.Context, sp ffSplitIn, accts []store.Account) (*txIn, string) {
	in := &txIn{Source: "api"}
	kind := ffKindFilter(sp.Type)
	if kind == "" {
		kind = "expense"
	}
	in.Kind = &kind
	find := func(id json.RawMessage, name string) *store.Account {
		if v := rawString(id); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				for i := range accts {
					if accts[i].ID == n {
						return &accts[i]
					}
				}
			}
		}
		return store.FindAccountByName(accts, name)
	}
	var acct *store.Account
	switch kind {
	case "income":
		acct = find(sp.DestinationID, sp.DestinationName)
		if sp.SourceName != "" && sp.SourceName != "(cash)" {
			p := sp.SourceName
			in.Payee = &p
		}
	case "transfer":
		acct = find(sp.SourceID, sp.SourceName)
		if to := find(sp.DestinationID, sp.DestinationName); to != nil {
			in.ToAccountID = &to.ID
		}
	default:
		acct = find(sp.SourceID, sp.SourceName)
		if sp.DestinationName != "" && sp.DestinationName != "(cash)" && find(sp.DestinationID, sp.DestinationName) == nil {
			p := sp.DestinationName
			in.Payee = &p
		}
	}
	if acct == nil {
		return nil, "unknown source/destination account"
	}
	in.AccountID = &acct.ID
	date, hm := ffDateTime(sp.Date)
	if len(sp.Date) == 10 {
		date, hm = sp.Date, ""
	}
	in.Date, in.Time = &date, &hm
	amt, ok := rawFloat(sp.Amount)
	if !ok || amt == 0 {
		return nil, "amount is required"
	}
	cur := acct.Currency
	if c := strings.ToUpper(strings.TrimSpace(sp.CurrencyCode)); c != "" && currency.Valid(c) {
		cur = c
	}
	minor := int64(math.Round(math.Abs(amt) * math.Pow10(currency.Decimals(cur))))
	in.Amount, in.Currency = &minor, &cur
	if fa, ok := rawFloat(sp.ForeignAmount); ok && fa != 0 && sp.ForeignCurrency != "" {
		fc := strings.ToUpper(sp.ForeignCurrency)
		fm := int64(math.Round(math.Abs(fa) * math.Pow10(currency.Decimals(fc))))
		if kind == "transfer" {
			in.ToAmount = &fm
		} else if cur == acct.Currency {
			// amount is in the account currency, foreign is what the receipt said
			in.ForeignAmount, in.ForeignCurrency = &fm, &fc
			in.AccountAmount = &minor
			in.Amount, in.Currency = nil, nil
		}
	}
	desc := strings.TrimSpace(sp.Description)
	in.Description = &desc
	if v := rawString(sp.CategoryID); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			in.CategoryID = &n
		}
	} else if sp.CategoryName != "" {
		if id, err := s.store.EnsureCategory(ctx, sp.CategoryName, "expense"); err == nil {
			in.CategoryID = &id
		}
	}
	tags := cleanTags(sp.Tags)
	in.Tags = &tags
	notes := strings.TrimSpace(reCurrencyTag.ReplaceAllString(sp.Notes, ""))
	in.Notes = &notes
	if v := rawString(sp.BillID); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			in.SubscriptionID = &n
		}
	}
	return in, ""
}

func (s *Server) ffCreateTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		ApplyRules   *bool       `json:"apply_rules"`
		Transactions []ffSplitIn `json:"transactions"`
	}
	if err := decode(r, &body); err != nil || len(body.Transactions) == 0 {
		ffError(w, http.StatusUnprocessableEntity, "The given data was invalid.")
		return
	}
	accts, _ := s.store.ListAccounts(ctx, actorID(ctx), false)
	if in, ok := s.ffMergeSplits(ctx, body.Transactions, accts); ok {
		in.SkipRules = body.ApplyRules != nil && !*body.ApplyRules
		t, msg, err := s.createTx(ctx, in)
		if msg != "" {
			ffError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		if err != nil {
			ffError(w, 500, err.Error())
			return
		}
		ffJSON(w, http.StatusOK, map[string]any{"data": s.ffGroup(t)})
		return
	}
	var first *store.Transaction
	for _, sp := range body.Transactions {
		in, msg := s.ffToIn(ctx, sp, accts)
		if msg != "" {
			ffError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		in.SkipRules = body.ApplyRules != nil && !*body.ApplyRules
		t, msg, err := s.createTx(ctx, in)
		if msg != "" {
			ffError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		if err != nil {
			ffError(w, 500, err.Error())
			return
		}
		if first == nil {
			first = t
		}
	}
	ffJSON(w, http.StatusOK, map[string]any{"data": s.ffGroup(first)})
}

func (s *Server) ffUpdateTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.store.GetTransaction(ctx, idParam(r, "id"))
	if err != nil || !s.canWrite(ctx, t.AccountID) {
		ffError(w, 404, "Resource not found")
		return
	}
	var body struct {
		Transactions []ffSplitIn `json:"transactions"`
	}
	if err := decode(r, &body); err != nil || len(body.Transactions) == 0 {
		ffError(w, http.StatusUnprocessableEntity, "The given data was invalid.")
		return
	}
	sp := body.Transactions[0]
	if sp.Type == "" {
		sp.Type = map[string]string{"expense": "withdrawal", "income": "deposit", "transfer": "transfer"}[t.Kind]
	}
	if sp.Date == "" {
		sp.Date = t.Date
	}
	if len(sp.Amount) == 0 {
		sp.Amount = json.RawMessage(strconv.Quote(majorPlain(t.Amount, t.Currency)))
	}
	if len(sp.SourceID) == 0 && sp.SourceName == "" && len(sp.DestinationID) == 0 && sp.DestinationName == "" {
		id := json.RawMessage(strconv.FormatInt(t.AccountID, 10))
		if t.Kind == "income" {
			sp.DestinationID = id
		} else {
			sp.SourceID = id
		}
	}
	accts, _ := s.store.ListAccounts(ctx, actorID(ctx), false)
	in, msg := s.ffToIn(ctx, sp, accts)
	if msg != "" {
		ffError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if sp.Description == "" {
		in.Description = nil
	}
	if sp.Tags == nil {
		in.Tags = nil
	}
	if msg := s.build(ctx, in, t); msg != "" {
		ffError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if err := s.store.UpdateTransaction(ctx, t); err != nil {
		ffError(w, 500, err.Error())
		return
	}
	t, _ = s.store.GetTransaction(ctx, t.ID)
	s.emitTx(ctx, "transaction.updated", t)
	ffJSON(w, http.StatusOK, map[string]any{"data": s.ffGroup(t)})
}

func (s *Server) ffDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.store.GetTransaction(ctx, idParam(r, "id"))
	if err != nil || !s.canWrite(ctx, t.AccountID) {
		ffError(w, 404, "Resource not found")
		return
	}
	for _, p := range s.store.AttachmentPaths(ctx, t.ID) {
		s.removeFile(p)
	}
	_ = s.store.DeleteTransaction(ctx, t.ID)
	s.emitTx(ctx, "transaction.deleted", t)
	s.publish("money.transaction_deleted", fmt.Sprintf("transaction:%d", t.ID), txEvent(t))
	w.WriteHeader(http.StatusNoContent)
}

// ffBills renders subscriptions as Firefly bills, including pay_dates in the
// requested window and the "notify: N" marker the old reminder scripts read.
func (s *Server) ffBills(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subs, err := s.store.ListSubscriptions(ctx, actorID(ctx))
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	end := r.URL.Query().Get("end")
	if !validDate(end) || end == "" {
		end = addDays(store.Today(), 365)
	}
	items := []ffItem{}
	for i := range subs {
		x := &subs[i]
		if x.Kind != "expense" {
			continue
		}
		freq := x.Freq
		skip := x.Every - 1
		if x.Freq == "monthly" && x.Every == 3 {
			freq, skip = "quarterly", 0
		} else if x.Freq == "monthly" && x.Every == 6 {
			freq, skip = "half-year", 0
		}
		pay := []string{}
		if x.Active {
			for _, d := range occurrences(x, end, 400) {
				pay = append(pay, ffDate(d, ""))
			}
		}
		if len(pay) == 0 && x.Active && x.NextDate != "" && !limitReached(x) {
			pay = append(pay, ffDate(dueOf(x), "")) // like Firefly: the next one even past the window
		}
		notes := strings.TrimSpace(x.Notes)
		if x.NotifyDays >= 0 {
			notes = strings.TrimSpace(notes + "\nnotify: " + strconv.Itoa(x.NotifyDays))
		}
		var next any
		if x.Active && x.NextDate != "" && !limitReached(x) {
			next = ffDate(dueOf(x), "")
		}
		hi := x.AmountMax
		if hi < x.Amount {
			hi = x.Amount
		}
		var paid []map[string]any
		if x.LastPaid != "" {
			paid = []map[string]any{{"date": ffDate(x.LastPaid, "")}}
		}
		items = append(items, ffItem{Type: "bills", ID: strconv.FormatInt(x.ID, 10), Attributes: map[string]any{
			"name": x.Name, "currency_code": x.Currency, "currency_id": x.Currency, "currency_decimal_places": currency.Decimals(x.Currency),
			"amount_min": majorPlain(x.Amount, x.Currency), "amount_max": majorPlain(hi, x.Currency), "amount_avg": majorPlain((x.Amount+hi)/2, x.Currency),
			"date": ffDate(firstNonEmpty(x.StartDate, x.NextDate), ""), "end_date": nilIfEmpty(x.EndDate), "repeat_freq": freq, "skip": skip, "active": x.Active,
			"notes": notes, "pay_dates": pay, "paid_dates": paid, "next_expected_match": next, "object_group_title": nil,
		}})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	ffPage(w, r, items)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// usedCurrencies are the home currency plus every account currency.
func (s *Server) usedCurrencies(ctx context.Context) []string {
	st, _ := s.store.GetSettings(ctx)
	seen := map[string]bool{st.HomeCurrency: true}
	out := []string{st.HomeCurrency}
	accts, _ := s.store.ListAccounts(ctx, actorID(ctx), true)
	for _, a := range accts {
		if !seen[a.Currency] {
			seen[a.Currency] = true
			out = append(out, a.Currency)
		}
	}
	rows, err := s.store.DB.QueryContext(ctx, `SELECT DISTINCT foreign_currency FROM transactions WHERE foreign_currency != ''`)
	if err == nil {
		var extra []string
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				extra = append(extra, c)
			}
		}
		rows.Close()
		for _, c := range extra {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

func (s *Server) ffCurrencies(w http.ResponseWriter, r *http.Request) {
	st, _ := s.store.GetSettings(r.Context())
	items := []ffItem{}
	for _, c := range s.usedCurrencies(r.Context()) {
		cur, _ := currency.Lookup(c)
		items = append(items, ffItem{Type: "currencies", ID: c, Attributes: map[string]any{"code": c, "name": cur.Name, "symbol": c, "decimal_places": cur.Decimals, "enabled": true, "default": c == st.HomeCurrency, "primary": c == st.HomeCurrency}})
	}
	ffPage(w, r, items)
}

func (s *Server) ffExchangeRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cs := s.usedCurrencies(ctx)
	today := store.Today()
	items := []ffItem{}
	for _, a := range cs {
		for _, b := range cs {
			if a >= b {
				continue
			}
			rt, _ := s.rate(ctx, a, b, today)
			items = append(items, ffItem{Type: "currency_exchange_rates", ID: a + "-" + b, Attributes: map[string]any{
				"from_currency_code": a, "to_currency_code": b, "rate": strconv.FormatFloat(rt, 'f', 12, 64), "date": ffDate(today, "")}})
		}
	}
	ffPage(w, r, items)
}

func (s *Server) ffInsight(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		q := r.URL.Query()
		txs, _, err := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Kind: kind, From: q.Get("start"), To: q.Get("end"), Limit: 1000000})
		if err != nil {
			ffError(w, 500, err.Error())
			return
		}
		sums := map[string]int64{}
		for _, t := range txs {
			sums[t.Currency] += t.Amount
		}
		out := []map[string]any{}
		for c, v := range sums {
			sign := int64(-1)
			if kind == "income" {
				sign = 1
			}
			f := float64(sign*v) / math.Pow10(currency.Decimals(c))
			out = append(out, map[string]any{"difference": fmt.Sprintf("%.*f", currency.Decimals(c), f), "difference_float": f, "currency_id": c, "currency_code": c})
		}
		ffJSON(w, http.StatusOK, out)
	}
}

func (s *Server) ffEmpty(w http.ResponseWriter, r *http.Request) {
	ffJSON(w, http.StatusOK, map[string]any{"data": []any{}, "meta": ffMeta(0, 1, 50)})
}

// ffTemplates renders templates the way Firefly Pico's API does.
func (s *Server) ffTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tpls, err := s.store.ListTemplates(ctx, actorID(ctx))
	if err != nil {
		ffError(w, 500, err.Error())
		return
	}
	accCur := map[int64]string{}
	accts, _ := s.store.ListAccounts(ctx, actorID(ctx), true)
	for _, a := range accts {
		accCur[a.ID] = a.Currency
	}
	items := []map[string]any{}
	for _, t := range tpls {
		var amount any
		cur := t.Currency
		if t.AccountID != nil && cur == "" {
			cur = accCur[*t.AccountID]
		}
		if t.Amount != nil {
			amount = float64(*t.Amount) / math.Pow10(currency.Decimals(cur))
		}
		src, dst := t.AccountID, t.ToAccountID
		typ := map[string]string{"expense": "withdrawal", "income": "deposit", "transfer": "transfer"}[t.Kind]
		if t.Kind == "income" {
			src, dst = nil, t.AccountID
		}
		notes := t.Notes
		if t.Currency != "" {
			notes = strings.TrimSpace(notes + " [currency:" + t.Currency + "]")
		}
		tags := []map[string]any{}
		for _, name := range t.Tags {
			if tg, err := s.store.GetTagByName(ctx, name); err == nil {
				tags = append(tags, map[string]any{"tag_id": tg.ID, "transaction_template_id": t.ID})
			}
		}
		extra := []map[string]any{}
		for _, a := range strings.Split(t.Aliases, ",") {
			if a = strings.TrimSpace(a); a != "" {
				extra = append(extra, map[string]any{"name": a, "transaction_template_id": t.ID})
			}
		}
		items = append(items, map[string]any{"id": t.ID, "name": t.Name, "amount": amount, "account_source_id": src, "account_destination_id": dst, "category_id": t.CategoryID,
			"type": typ, "notes": nilIfEmpty(notes), "description": nilIfEmpty(t.Description), "budget_id": nil, "tags": tags, "extra_names": extra,
			"created_at": t.CreatedAt, "updated_at": t.CreatedAt})
	}
	ffPage(w, r, items)
}

// ffMergeSplits turns a Firefly group of several withdrawal (or deposit)
// splits from the same account, date and currency into one split transaction.
func (s *Server) ffMergeSplits(ctx context.Context, sps []ffSplitIn, accts []store.Account) (*txIn, bool) {
	if len(sps) < 2 {
		return nil, false
	}
	first := sps[0]
	if ffKindFilter(first.Type) == "transfer" {
		return nil, false
	}
	var total float64
	var parts []splitIn
	var cur string
	for i, sp := range sps {
		if sp.Type != first.Type || rawString(sp.SourceID) != rawString(first.SourceID) || sp.SourceName != first.SourceName ||
			rawString(sp.DestinationID) != rawString(first.DestinationID) || sp.Date != first.Date || sp.CurrencyCode != first.CurrencyCode {
			return nil, false
		}
		a, ok := rawFloat(sp.Amount)
		if !ok || a == 0 {
			return nil, false
		}
		total += math.Abs(a)
		if i == 0 {
			cur = strings.ToUpper(sp.CurrencyCode)
		}
		p := splitIn{Note: strings.TrimSpace(sp.Description)}
		if v := rawString(sp.CategoryID); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				p.CategoryID = &n
			}
		} else if sp.CategoryName != "" {
			if id, err := s.store.EnsureCategory(ctx, sp.CategoryName, "expense"); err == nil {
				p.CategoryID = &id
			}
		}
		parts = append(parts, p)
		_ = cur
	}
	merged := first
	merged.Amount = json.RawMessage(strconv.FormatFloat(total, 'f', -1, 64))
	merged.CategoryID, merged.CategoryName, merged.ForeignAmount = nil, "", nil
	in, msg := s.ffToIn(ctx, merged, accts)
	if msg != "" {
		return nil, false
	}
	// parts are in the same currency as the total that was typed
	dec := currency.Decimals(cur)
	if in.Currency != nil {
		dec = currency.Decimals(*in.Currency)
	}
	for i, sp := range sps {
		a, _ := rawFloat(sp.Amount)
		parts[i].Amount = int64(math.Round(math.Abs(a) * math.Pow10(dec)))
	}
	var tags []string
	for _, sp := range sps {
		tags = append(tags, sp.Tags...)
	}
	tags = cleanTags(tags)
	in.Tags = &tags
	in.Splits = &parts
	if g := strings.TrimSpace(first.Description); g != "" {
		in.Description = &g
	}
	return in, true
}

func liabType(a store.Account) any {
	if !store.IsLiability(a.Kind) {
		return nil
	}
	return a.Kind
}

// liabDir uses Firefly's words: debit = I owe it, credit = I am owed it.
func liabDir(a store.Account) any {
	if !store.IsLiability(a.Kind) {
		return nil
	}
	if a.Direction == "owed" {
		return "credit"
	}
	return "debit"
}
