package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
	"github.com/abjelosevic88/pocket-homelab-money/internal/quick"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

func (s *Server) aiOrFail(w http.ResponseWriter, r *http.Request) *clients {
	c := s.clients(r.Context())
	c.ai.Pocket, _ = s.inferenceFor(r.Context(), actorID(r.Context()), c.st.AIProvider, c.st.AIKey != "")
	if !c.ai.Configured() {
		writeErr(w, http.StatusBadRequest, "connect Pocket AI or add an OpenAI key under Settings → AI")
		return nil
	}
	return &c
}

type aiTx struct {
	Kind        string   `json:"kind"`
	Amount      float64  `json:"amount"`
	Currency    string   `json:"currency"`
	Date        string   `json:"date"`
	Time        string   `json:"time"`
	AccountID   FlexID   `json:"account_id"`
	ToAccountID FlexID   `json:"to_account_id"`
	CategoryID  FlexID   `json:"category_id"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Payee       string   `json:"payee"`
}

// handleAIParse turns free text ("juče 20 maraka pekara i 35 eura gorivo sa
// unicredit") into one or more drafts the user confirms before booking.
func (s *Server) handleAIParse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.aiOrFail(w, r)
	if c == nil {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	qc, accts := s.quickContext(ctx)
	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "Today is %s (%s), local time %s. Home currency: %s.\n", now.Format("2006-01-02"), now.Weekday(), now.Format("15:04"), c.st.HomeCurrency)
	b.WriteString("Accounts (id: name, currency):\n")
	for _, a := range qc.Accounts {
		def := ""
		if a.ID == qc.DefaultAccount {
			def = " (default)"
		}
		fmt.Fprintf(&b, "- %d: %s, %s%s\n", a.ID, a.Name, a.Currency, def)
	}
	b.WriteString("Categories (id: name):\n")
	for _, cat := range qc.Categories {
		fmt.Fprintf(&b, "- %d: %s\n", cat.ID, cat.Name)
	}
	tags, _ := s.store.ListTags(ctx, actorID(ctx))
	var tn []string
	for _, t := range tags {
		if !t.Archived {
			tn = append(tn, t.Name)
		}
	}
	fmt.Fprintf(&b, "Existing tags: %s\n", strings.Join(tn, ", "))
	if len(qc.Templates) > 0 {
		b.WriteString("The user's usual entries (name → category id, tags):\n")
		for i, t := range qc.Templates {
			if i >= 40 {
				break
			}
			fmt.Fprintf(&b, "- %s → %d %v\n", t.Names[0], t.CategoryID, t.Tags)
		}
	}
	if c.st.AIContext != "" {
		b.WriteString("Notes from the user: " + c.st.AIContext + "\n")
	}
	b.WriteString("\nText: " + in.Text)
	system := `You turn a short spoken or typed note about money into transactions for a personal finance app.
The note may be in English or Bosnian/Croatian/Serbian (Latin or Cyrillic, with or without diacritics).
Bosnian hints: "juče/juce" = yesterday, "prekjuče" = two days ago, "maraka"/"KM" = BAM, "eura/evra" = EUR, "dolara" = USD, "dinara" = RSD,
"plata"/"plaća" = salary (income), "dobio/primio" = received (income), "sa/s/sa računa X" = paid from account X, "na" = to account X (transfer when both are accounts).
Return JSON: {"transactions":[{"kind":"expense|income|transfer","amount":number (major units, positive),"currency":"ISO code or empty for the account's",
"date":"YYYY-MM-DD","time":"HH:MM or empty","account_id":id,"to_account_id":id or 0,"category_id":id or 0,"tags":[],"description":"short, in the user's language","payee":""}]}.
One note can hold several transactions ("i", "and", commas) — split them. Use the default account unless an account is named.
Pick the closest existing category; reuse existing tags only when clearly implied. Never invent amounts: skip an item without one.`
	var out struct {
		Transactions []aiTx `json:"transactions"`
	}
	u, err := c.ai.JSON(ctx, system, b.String(), &out)
	s.store.LogAI(ctx, actorID(ctx), "parse", u.Model, u.PromptTokens, u.CompletionTokens)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	ok := map[int64]bool{}
	for _, a := range qc.Accounts {
		ok[a.ID] = true
	}
	catOK := map[int64]bool{}
	for _, cat := range qc.Categories {
		catOK[cat.ID] = true
	}
	previews := []quickPreview{}
	for _, t := range out.Transactions {
		if t.Amount <= 0 {
			continue
		}
		d := quick.Draft{Kind: t.Kind, Currency: strings.ToUpper(strings.TrimSpace(t.Currency)), Date: t.Date, Time: t.Time, Tags: cleanTags(t.Tags),
			Description: strings.TrimSpace(t.Description), Payee: strings.TrimSpace(t.Payee), Input: in.Text, Recognized: []string{"AI"}}
		if !store.Valid(store.TransactionKinds, d.Kind) {
			d.Kind = "expense"
		}
		if !validDate(d.Date) || d.Date == "" {
			d.Date = store.Today()
		}
		if _, err := time.Parse("15:04", d.Time); err != nil {
			d.Time = ""
		}
		d.AccountID = int64(t.AccountID)
		if !ok[d.AccountID] {
			d.AccountID = qc.DefaultAccount
		}
		if ok[int64(t.ToAccountID)] && int64(t.ToAccountID) != d.AccountID {
			d.ToAccountID = int64(t.ToAccountID)
		} else if d.Kind == "transfer" {
			d.Kind = "expense"
		}
		if catOK[int64(t.CategoryID)] && d.Kind != "transfer" {
			d.CategoryID = int64(t.CategoryID)
		}
		if d.Currency != "" && !currency.Valid(d.Currency) {
			d.Currency = ""
		}
		cur := d.Currency
		if cur == "" {
			for _, a := range qc.Accounts {
				if a.ID == d.AccountID {
					cur = a.Currency
				}
			}
		}
		minor := int64(math.Round(t.Amount * math.Pow10(currency.Decimals(cur))))
		d.Amount = &minor
		previews = append(previews, s.preview(ctx, d, accts))
	}
	writeJSON(w, http.StatusOK, previews)
}

// handleAITranscribe turns a voice note into text (the client then parses it).
func (s *Server) handleAITranscribe(w http.ResponseWriter, r *http.Request) {
	c := s.clients(r.Context())
	if c.ai.Key == "" {
		writeErr(w, 400, "Dictation requires your OpenAI key in Settings → AI")
		return
	}
	data, name, _, err := s.readUpload(r, "file")
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "no audio")
		return
	}
	if name == "" {
		name = "note.webm"
	}
	text, err := c.ai.Transcribe(r.Context(), data, name, c.st.AILanguage)
	s.store.LogAI(r.Context(), actorID(r.Context()), "transcribe", c.ai.TranscribeModel, 0, 0)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// moneyBrief is the plain-text picture of the user's finances the model sees.
func (s *Server) moneyBrief(ctx context.Context) string {
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	ids := s.visibleIDs(ctx)
	m := func(v int64) string { return majorString(v, st.HomeCurrency) }
	var b strings.Builder
	fmt.Fprintf(&b, "Today: %s. All amounts in %s unless marked.\n\nAccounts:\n", store.Today(), st.HomeCurrency)
	accts, _, _ := s.accountsWithHome(ctx, false)
	for _, a := range accts {
		fmt.Fprintf(&b, "- %s (%s): %s", a.Name, a.Kind, majorString(a.Balance, a.Currency))
		if a.Currency != st.HomeCurrency {
			fmt.Fprintf(&b, " ≈ %s", m(a.BalanceHome))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nMonths (income / expense, top categories):\n")
	f, t := periodBounds(store.Today(), "monthly", st.MonthStartDay, st.WeekStart)
	for i := 0; i < 12; i++ {
		days, _ := s.store.DailySums(ctx, ids, f, t)
		var inc, exp int64
		for _, d := range days {
			if d.Kind == "income" {
				inc += d.Sum
			} else {
				exp += d.Sum
			}
		}
		if inc == 0 && exp == 0 {
			break
		}
		cats, _ := s.store.CategorySums(ctx, ids, "expense", f, t)
		var parts []string
		for j, c := range cats {
			if j >= 8 {
				break
			}
			name := c.Name
			if name == "" {
				name = "uncategorised"
			}
			parts = append(parts, fmt.Sprintf("%s %s", name, m(c.Sum)))
		}
		fmt.Fprintf(&b, "- %s: in %s / out %s; %s\n", periodLabel(f, t, "monthly"), m(inc), m(exp), strings.Join(parts, ", "))
		f, t = prevPeriod(f, "monthly", st.MonthStartDay, st.WeekStart)
	}
	tags, _ := s.store.TagSums(ctx, ids, "expense", "1900-01-01", "2999-12-31")
	if len(tags) > 0 {
		b.WriteString("\nSpending per tag (all time): ")
		var parts []string
		for _, g := range tags {
			parts = append(parts, fmt.Sprintf("%s %s", g.Name, m(g.Sum)))
		}
		b.WriteString(strings.Join(parts, ", ") + "\n")
	}
	subs, _ := s.store.ListSubscriptions(ctx, actorID(ctx))
	if len(subs) > 0 {
		b.WriteString("\nSubscriptions and bills:\n")
		for i := range subs {
			x := &subs[i]
			s.decorate(ctx, x, st.HomeCurrency)
			fmt.Fprintf(&b, "- %s: %s %s every %d, next %s, ≈%s/month%s\n", x.Name, majorString(x.Amount, x.Currency), x.Freq, x.Every, x.NextDate, m(x.MonthlyHome), map[bool]string{true: "", false: " (paused)"}[x.Active])
		}
	}
	top, _ := s.store.TopSpends(ctx, ids, addDays(store.Today(), -365), store.Today(), 25)
	if len(top) > 0 {
		b.WriteString("\nWhere money went in the last 12 months (by payee/description): ")
		var parts []string
		for _, g := range top {
			parts = append(parts, fmt.Sprintf("%s %s (%d×)", g.Name, m(g.Sum), g.Count))
		}
		b.WriteString(strings.Join(parts, ", ") + "\n")
	}
	return b.String()
}

// handleAIAsk answers a question about the user's money from the brief and,
// when the question names something, the matching transactions.
func (s *Server) handleAIAsk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.aiOrFail(w, r)
	if c == nil {
		return
	}
	var in struct {
		Question string `json:"question"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		writeErr(w, http.StatusBadRequest, "question is required")
		return
	}
	brief := s.moneyBrief(ctx)
	// pull transactions that mention words from the question
	var hits []string
	seen := map[int64]bool{}
	for _, word := range strings.Fields(fold(in.Question)) {
		word = strings.Trim(word, "?!.,:;\"'")
		if len(word) < 4 {
			continue
		}
		txs, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Q: word, Limit: 60})
		for _, t := range txs {
			if seen[t.ID] || len(hits) >= 150 {
				continue
			}
			seen[t.ID] = true
			hits = append(hits, fmt.Sprintf("%s %s %s %s [%s] %s", t.Date, t.Kind, majorString(t.Amount, t.Currency), t.Description, t.CategoryName, strings.Join(t.Tags, ",")))
		}
	}
	user := brief
	if len(hits) > 0 {
		user += "\nTransactions that match words in the question:\n" + strings.Join(hits, "\n") + "\n"
	}
	user += "\nQuestion: " + in.Question
	system := `You answer questions about the user's personal finances using only the data given. Be concrete: name amounts, months and dates.
Keep it short (a few sentences or a small list). If the data cannot answer it, say what is missing. Answer in the language of the question.`
	txt, u, err := c.ai.Complete(ctx, system, user, false)
	s.store.LogAI(ctx, actorID(ctx), "ask", u.Model, u.PromptTokens, u.CompletionTokens)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": txt, "matched": len(hits)})
}

func (s *Server) handleAIUsage(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.AIUsage(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "usage")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// FlexID accepts ids as numbers or strings (models return both).
type FlexID int64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	var n float64
	if _, err := fmt.Sscanf(s, "%g", &n); err != nil {
		*f = 0
		return nil
	}
	*f = FlexID(n)
	return nil
}
