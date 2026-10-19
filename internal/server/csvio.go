package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// readCSV sniffs the delimiter and returns all records.
func readCSV(data []byte, delim string) ([][]string, rune, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		// most bank exports that are not UTF-8 are Windows-1250/1252; keep ASCII, mark the rest
		data = []byte(strings.ToValidUTF8(string(data), "?"))
	}
	var d rune
	switch delim {
	case "comma":
		d = ','
	case "semicolon":
		d = ';'
	case "tab":
		d = '\t'
	default:
		first := string(data)
		if i := strings.IndexByte(first, '\n'); i > 0 {
			first = first[:i]
		}
		d = ','
		best := strings.Count(first, ",")
		for _, c := range []rune{';', '\t', '|'} {
			if n := strings.Count(first, string(c)); n > best {
				d, best = c, n
			}
		}
	}
	rd := csv.NewReader(bytes.NewReader(data))
	rd.Comma = d
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	rd.TrimLeadingSpace = true
	recs, err := rd.ReadAll()
	return recs, d, err
}

// CSVMapping tells the importer which column holds what (-1 = none).
type CSVMapping struct {
	Date        int    `json:"date"`
	Amount      int    `json:"amount"`
	Debit       int    `json:"debit"`
	Credit      int    `json:"credit"`
	Description int    `json:"description"`
	Payee       int    `json:"payee"`
	Category    int    `json:"category"`
	Notes       int    `json:"notes"`
	Currency    int    `json:"currency"`
	DateFormat  string `json:"date_format"` // auto | dmy | mdy | ymd
	Decimal     string `json:"decimal"`     // auto | dot | comma
	Invert      bool   `json:"invert"`      // positive numbers are expenses
	HasHeader   bool   `json:"has_header"`
	Delimiter   string `json:"delimiter"`
}

func guessMapping(header []string) CSVMapping {
	m := CSVMapping{Date: -1, Amount: -1, Debit: -1, Credit: -1, Description: -1, Payee: -1, Category: -1, Notes: -1, Currency: -1, DateFormat: "auto", Decimal: "auto", HasHeader: true}
	for i, h := range header {
		k := fold(strings.TrimSpace(h))
		has := func(words ...string) bool {
			for _, w := range words {
				if strings.Contains(k, w) {
					return true
				}
			}
			return false
		}
		switch {
		case m.Date < 0 && has("date", "datum", "booking", "valuta dat", "knjiz"):
			m.Date = i
		case m.Debit < 0 && has("debit", "zaduz", "isplat", "withdraw", "out", "rashod"):
			m.Debit = i
		case m.Credit < 0 && has("credit", "odobr", "uplat", "deposit", "prihod"):
			m.Credit = i
		case m.Amount < 0 && has("amount", "iznos", "suma", "betrag", "value"):
			m.Amount = i
		case m.Currency < 0 && (k == "currency" || k == "valuta" || k == "ccy" || has("currency")):
			m.Currency = i
		case m.Payee < 0 && has("payee", "merchant", "primalac", "counterparty", "partner", "beneficiary", "name"):
			m.Payee = i
		case m.Description < 0 && has("description", "opis", "svrha", "purpose", "details", "memo", "text", "narrative"):
			m.Description = i
		case m.Category < 0 && has("category", "kategor"):
			m.Category = i
		case m.Notes < 0 && has("note", "biljesk", "napomena", "reference", "poziv"):
			m.Notes = i
		}
	}
	if m.Description < 0 && m.Payee >= 0 {
		m.Description, m.Payee = m.Payee, -1
	}
	return m
}

var reNum = regexp.MustCompile(`[^0-9,.\-]`)

func parseAmount(s, decimal string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") || strings.HasSuffix(s, "-")
	s = reNum.ReplaceAllString(s, "")
	if strings.HasSuffix(s, "-") {
		s = strings.TrimSuffix(s, "-")
		neg = true
	}
	if decimal == "auto" {
		lc, ld := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
		switch {
		case lc > ld:
			decimal = "comma"
		default:
			decimal = "dot"
		}
		// "1,234" with three digits after the comma and no dot is a thousands separator
		if lc >= 0 && ld < 0 && len(s)-lc-1 == 3 && strings.Count(s, ",") >= 1 && !strings.Contains(s[lc+1:], ",") {
			decimal = "dot"
		}
	}
	if decimal == "comma" {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	} else {
		s = strings.ReplaceAll(s, ",", "")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		f = -math.Abs(f)
	}
	return f, true
}

var dateLayouts = map[string][]string{
	"dmy": {"02.01.2006", "2.1.2006", "02/01/2006", "2/1/2006", "02-01-2006", "02.01.06", "02.01.2006."},
	"mdy": {"01/02/2006", "1/2/2006", "01-02-2006", "01/02/06"},
	"ymd": {"2006-01-02", "2006/01/02", "2006.01.02", "20060102"},
}

func parseCSVDate(s, format string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) > 10 && (s[10] == ' ' || s[10] == 'T') {
		s = s[:10]
	}
	order := []string{"ymd", "dmy", "mdy"}
	if format != "auto" && format != "" {
		order = []string{format}
	}
	for _, f := range order {
		for _, l := range dateLayouts[f] {
			if t, err := time.Parse(l, s); err == nil {
				return t.Format("2006-01-02"), true
			}
		}
	}
	return "", false
}

func cell(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func (s *Server) handleCSVPreview(w http.ResponseWriter, r *http.Request) {
	data, _, _, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	recs, d, err := readCSV(data, r.FormValue("delimiter"))
	if err != nil || len(recs) == 0 {
		writeErr(w, http.StatusBadRequest, "could not read the CSV")
		return
	}
	m := guessMapping(recs[0])
	m.Delimiter = map[rune]string{',': "comma", ';': "semicolon", '\t': "tab"}[d]
	rows := recs
	if len(rows) > 21 {
		rows = rows[:21]
	}
	writeJSON(w, http.StatusOK, map[string]any{"header": recs[0], "rows": rows[1:], "total": len(recs) - 1, "mapping": m})
}

// handleCSVImport books every row as a transaction on one account, skipping
// rows that were imported before or already exist (same day, amount, text).
func (s *Server) handleCSVImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, _, _, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no file")
		return
	}
	var m CSVMapping
	if err := json.Unmarshal([]byte(r.FormValue("mapping")), &m); err != nil {
		writeErr(w, http.StatusBadRequest, "mapping is required")
		return
	}
	accountID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if !s.canWrite(ctx, accountID) {
		writeErr(w, http.StatusBadRequest, "choose an account you can book on")
		return
	}
	acct, _ := s.store.GetAccount(ctx, accountID)
	dry := r.FormValue("dry_run") == "1"
	recs, _, err := readCSV(data, m.Delimiter)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the CSV")
		return
	}
	if m.HasHeader && len(recs) > 0 {
		recs = recs[1:]
	}
	if m.Date < 0 || (m.Amount < 0 && m.Debit < 0 && m.Credit < 0) {
		writeErr(w, http.StatusBadRequest, "map at least the date and an amount column")
		return
	}
	cats, _ := s.store.ListCategories(ctx, actorID(ctx))
	catByName := map[string]int64{}
	for _, c := range cats {
		catByName[fold(c.Name)] = c.ID
	}
	type rowResult struct {
		Row    int    `json:"row"`
		Status string `json:"status"` // created | duplicate | error | ready
		Reason string `json:"reason,omitempty"`
		Date   string `json:"date"`
		Amount int64  `json:"amount"`
		Kind   string `json:"kind"`
		Text   string `json:"text"`
	}
	out := []rowResult{}
	created, dupes, bad := 0, 0, 0
	for i, rec := range recs {
		rr := rowResult{Row: i + 1}
		date, ok := parseCSVDate(cell(rec, m.Date), m.DateFormat)
		if !ok {
			rr.Status, rr.Reason = "error", "date "+cell(rec, m.Date)
			bad++
			out = append(out, rr)
			continue
		}
		var amt float64
		var have bool
		if m.Amount >= 0 {
			amt, have = parseAmount(cell(rec, m.Amount), m.Decimal)
		}
		if !have && m.Debit >= 0 {
			if v, ok := parseAmount(cell(rec, m.Debit), m.Decimal); ok && v != 0 {
				amt, have = -math.Abs(v), true
			}
		}
		if !have && m.Credit >= 0 {
			if v, ok := parseAmount(cell(rec, m.Credit), m.Decimal); ok && v != 0 {
				amt, have = math.Abs(v), true
			}
		}
		if !have || amt == 0 {
			rr.Status, rr.Reason = "error", "no amount"
			bad++
			out = append(out, rr)
			continue
		}
		if m.Invert {
			amt = -amt
		}
		kind := "income"
		if amt < 0 {
			kind = "expense"
		}
		cur := acct.Currency
		if c := strings.ToUpper(cell(rec, m.Currency)); currency.Valid(c) {
			cur = c
		}
		minor := int64(math.Round(math.Abs(amt) * math.Pow10(currency.Decimals(cur))))
		desc := cell(rec, m.Description)
		payee := cell(rec, m.Payee)
		if desc == "" {
			desc = payee
		}
		rr.Date, rr.Amount, rr.Kind, rr.Text = date, minor, kind, desc
		h := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%s|%s|%d", accountID, date, minor, desc, payee, i)))
		ext := "csv:" + hex.EncodeToString(h[:8])
		// same-day same-amount same-text = already there (or imported before)
		if _, err := s.store.TransactionByExternalID(ctx, acct.OwnerID, ext); err == nil || s.looksDuplicate(ctx, accountID, date, kind, minor, cur, desc) {
			rr.Status = "duplicate"
			dupes++
			out = append(out, rr)
			continue
		}
		if dry {
			rr.Status = "ready"
			out = append(out, rr)
			continue
		}
		in := txIn{Kind: &kind, Date: &date, AccountID: &accountID, Amount: &minor, Currency: &cur, Description: &desc, Payee: &payee, Source: "csv"}
		empty := ""
		in.Time = &empty
		if n := cell(rec, m.Notes); n != "" {
			in.Notes = &n
		}
		if c := cell(rec, m.Category); c != "" {
			if id, ok := catByName[fold(c)]; ok {
				in.CategoryID = &id
			}
		}
		if in.CategoryID == nil {
			in.CategoryID = s.store.LearnedCategory(ctx, actorID(ctx), firstNonEmpty(payee, desc))
		}
		t, msg, err := s.createTx(ctx, &in)
		if msg != "" || err != nil {
			rr.Status, rr.Reason = "error", firstNonEmpty(msg, fmt.Sprint(err))
			bad++
			out = append(out, rr)
			continue
		}
		t.ExternalID = ext
		_ = s.store.UpdateTransaction(ctx, actorID(ctx), t)
		rr.Status = "created"
		created++
		out = append(out, rr)
	}
	if !dry && created > 0 {
		s.store.LogActivity(ctx, actorID(ctx), "import.csv", fmt.Sprintf("%d transactions into %s", created, acct.Name))
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "duplicates": dupes, "errors": bad, "rows": out})
}

func (s *Server) looksDuplicate(ctx context.Context, accountID int64, date, kind string, amount int64, cur, desc string) bool {
	var n int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM transactions WHERE account_id = ? AND date = ? AND kind = ? AND (amount = ? OR (foreign_amount = ? AND foreign_currency = ?))
		AND (lower(description) = lower(?) OR ? = '' OR source != 'csv')`, accountID, date, kind, amount, amount, cur, desc, desc).Scan(&n)
	return n > 0
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	f := s.txFilter(r)
	f.Limit = 1000000
	f.Oldest = true
	txs, _, err := s.store.ListTransactions(r.Context(), f)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), actorID(r.Context()))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-money-%s.csv"`, store.Today()))
	_, _ = io.WriteString(w, "\xef\xbb\xbf")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "time", "kind", "account", "amount", "currency", "foreign_amount", "foreign_currency", "to_account", "to_amount", "amount_" + strings.ToLower(st.HomeCurrency),
		"description", "payee", "category", "tags", "notes", "subscription", "id"})
	for _, t := range txs {
		if len(t.Splits) > 0 {
			var parts []string
			for _, sp := range t.Splits {
				parts = append(parts, fmt.Sprintf("%s %s", firstNonEmpty(sp.CategoryName, "—"), majorPlain(sp.Amount, t.Currency)))
			}
			t.CategoryName = strings.Join(parts, "; ")
		}
		fa, ta := "", ""
		if t.ForeignAmount > 0 {
			fa = majorPlain(t.ForeignAmount, t.ForeignCurrency)
		}
		if t.Kind == "transfer" {
			ta = majorPlain(t.ToAmount, t.ToCurrency)
		}
		_ = cw.Write([]string{t.Date, t.Time, t.Kind, t.AccountName, majorPlain(t.Amount, t.Currency), t.Currency, fa, t.ForeignCurrency, t.ToAccountName, ta,
			majorPlain(t.AmountHome, st.HomeCurrency), t.Description, t.Payee, t.CategoryName, strings.Join(t.Tags, ", "), t.Notes, t.SubscriptionNm, strconv.FormatInt(t.ID, 10)})
	}
	cw.Flush()
}

func majorPlain(minor int64, code string) string {
	d := currency.Decimals(code)
	return strconv.FormatFloat(float64(minor)/math.Pow10(d), 'f', d, 64)
}

// handleExportJSON writes everything the user can see as one JSON file.
func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	accts, _ := s.store.ListAccounts(ctx, uid, true)
	txs, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Limit: 10000000, Oldest: true})
	cats, _ := s.store.ListCategories(ctx, actorID(ctx))
	tags, _ := s.store.ListTags(ctx, actorID(ctx))
	subs, _ := s.store.ListSubscriptions(ctx, uid)
	budgets, _ := s.store.ListBudgets(ctx, uid)
	goals, _ := s.store.ListGoals(ctx, uid)
	for i := range goals {
		goals[i].Entries, _ = s.store.GoalEntries(ctx, goals[i].ID)
	}
	tpls, _ := s.store.ListTemplates(ctx, uid)
	rules, _ := s.store.ListRules(ctx, uid)
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-money-%s.json"`, store.Today()))
	writeJSON(w, http.StatusOK, map[string]any{"app": "pocket-money", "version": Version, "exported_at": store.Now(), "home_currency": st.HomeCurrency,
		"accounts": accts, "categories": cats, "tags": tags, "transactions": txs, "subscriptions": subs, "budgets": budgets, "goals": goals, "templates": tpls, "rules": rules})
}
