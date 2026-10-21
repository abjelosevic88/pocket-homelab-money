// Package quick turns one line of text into a transaction draft without any
// AI: "kafa 6", "cg hrana 20eur", "+1500 plata", "gorivo 80 juce",
// "taxi 12 usd @cash #trip", "plata 4800-10,23". Templates are matched by name prefix the way the
// Firefly Pico assistant did it, so muscle memory carries over.
package quick

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-money/internal/currency"
)

// Template is the slice of a template the parser needs.
type Template struct {
	ID          int64
	Names       []string // name first, then aliases
	Kind        string
	Amount      *int64 // minor units of Currency (or the account's currency)
	Currency    string
	AccountID   int64
	ToAccountID int64
	CategoryID  int64
	Tags        []string
	Description string
	Payee       string
}

// Named is an id with a name (categories).
type Named struct {
	ID   int64
	Name string
}

// Account is an account the user may book on.
type Account struct {
	ID       int64
	Name     string
	Currency string
}

// Context is everything the parser may match against.
type Context struct {
	Today          time.Time
	Templates      []Template
	Categories     []Named
	Accounts       []Account
	DefaultAccount int64
	// Learn returns the category usually used for a description (may be nil).
	Learn func(text string) int64
}

// Draft is the parsed result. Amount is in minor units of Currency.
type Draft struct {
	Kind        string   `json:"kind"`
	Amount      *int64   `json:"amount"`
	Currency    string   `json:"currency"`
	Date        string   `json:"date"`
	Time        string   `json:"time"`
	AccountID   int64    `json:"account_id"`
	ToAccountID int64    `json:"to_account_id"`
	CategoryID  int64    `json:"category_id"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Payee       string   `json:"payee"`
	TemplateID  int64    `json:"template_id"`
	Calc        string   `json:"calc,omitempty"` // the sum typed for the amount: "4800 − 10.23"
	Recognized  []string `json:"recognized"`     // human hints: "template Kafa", "yesterday", "EUR"
	Input       string   `json:"input"`
}

// currency words and symbols people actually type
var currencyWords = map[string]string{
	"km": "BAM", "kм": "BAM", "kn": "HRK", "bam": "BAM", "maraka": "BAM", "marka": "BAM", "marke": "BAM", "konvertibilnih": "BAM",
	"eur": "EUR", "euro": "EUR", "eura": "EUR", "evra": "EUR", "evro": "EUR", "eure": "EUR", "euros": "EUR", "€": "EUR",
	"usd": "USD", "dolar": "USD", "dolara": "USD", "dollar": "USD", "dollars": "USD", "$": "USD",
	"rsd": "RSD", "din": "RSD", "dinar": "RSD", "dinara": "RSD",
	"gbp": "GBP", "£": "GBP", "funti": "GBP", "chf": "CHF", "franak": "CHF", "franaka": "CHF",
	"try": "TRY", "lira": "TRY", "lire": "TRY", "huf": "HUF", "forinti": "HUF", "czk": "CZK", "pln": "PLN",
	"jpy": "JPY", "¥": "JPY", "yen": "JPY", "jena": "JPY",
}

// relative day words in English and Bosnian/Croatian/Serbian (with and without diacritics)
var dayWords = map[string]int{
	"today": 0, "danas": 0, "yesterday": -1, "juce": -1, "jucer": -1, "jučer": -1, "juče": -1, "yday": -1,
	"prekjuce": -2, "prekjucer": -2, "prekjučer": -2, "prekjuče": -2, "tomorrow": 1, "sutra": 1,
}

var weekdayWords = map[string]time.Weekday{
	"monday": time.Monday, "mon": time.Monday, "ponedjeljak": time.Monday, "ponedeljak": time.Monday, "pon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "utorak": time.Tuesday, "uto": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday, "srijeda": time.Wednesday, "sreda": time.Wednesday, "sri": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "cetvrtak": time.Thursday, "četvrtak": time.Thursday, "cet": time.Thursday,
	"friday": time.Friday, "fri": time.Friday, "petak": time.Friday, "pet": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday, "subota": time.Saturday, "sub": time.Saturday,
	"sunday": time.Sunday, "sun": time.Sunday, "nedjelja": time.Sunday, "nedelja": time.Sunday, "ned": time.Sunday,
}

var (
	reOffset   = regexp.MustCompile(`^([+-])(\d{1,3})d$`)
	reAmount   = regexp.MustCompile(`^([+-]?)([€$£¥]?)(\d{1,3}(?:[ .]\d{3})+(?:,\d{1,3})?|\d+(?:[.,]\d{1,3})?)([a-zA-Z€$£¥кмКМ.]{0,8})$`)
	reDate     = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})?$`)
	reThousand = regexp.MustCompile(`^\d{1,3}(\.\d{3})+(,\d+)?$`)
	reISODate  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	reTime     = regexp.MustCompile(`^(\d{1,2})[:h](\d{2})$`)
	// a sum in place of the amount: "4800-10.23", "3x2,5", "(12+8)/2eur"
	reExpr = regexp.MustCompile(`^([+-]?)([€$£¥]?)([\d(][\d.,()+\-*/x×÷]*[\d)])([a-zA-Z€$£¥кмКМ.]{0,8})$`)
)

func fold(s string) string {
	r := strings.NewReplacer("č", "c", "ć", "c", "š", "s", "ž", "z", "đ", "dj", "Č", "c", "Ć", "c", "Š", "s", "Ž", "z", "Đ", "dj")
	return strings.ToLower(r.Replace(s))
}

// Parse reads one line.
func Parse(input string, c Context) Draft {
	d := Draft{Kind: "expense", Tags: []string{}, Recognized: []string{}, Input: input, Date: c.Today.Format("2006-01-02"), AccountID: c.DefaultAccount}
	raw := strings.TrimSpace(input)
	if raw == "" {
		return d
	}
	if strings.HasPrefix(raw, "+") && len(raw) > 1 && !unicode.IsDigit(rune(raw[1])) {
		// "+ plata 1500"
		d.Kind = "income"
		d.Recognized = append(d.Recognized, "income")
		raw = strings.TrimSpace(raw[1:])
	}
	tokens := strings.Fields(raw)

	// 1. template by the longest leading run of words that names one template
	var tpl *Template
	consumed := 0
	for k := min(len(tokens), 5); k >= 1 && tpl == nil; k-- {
		prefix := fold(strings.Join(tokens[:k], " "))
		if looksLikeAmount(prefix) {
			continue
		}
		var cands []*Template
		var exact *Template
		for i := range c.Templates {
			t := &c.Templates[i]
			for _, n := range t.Names {
				fn := fold(n)
				if fn == prefix {
					exact = t
				}
				if strings.HasPrefix(fn, prefix) {
					cands = append(cands, t)
					break
				}
			}
			if exact != nil {
				break
			}
		}
		switch {
		case exact != nil:
			tpl, consumed = exact, k
		case len(cands) == 1 && len(prefix) >= 3:
			tpl, consumed = cands[0], k
		}
	}
	if tpl != nil {
		d.TemplateID = tpl.ID
		if tpl.Kind != "" && d.Kind != "income" {
			d.Kind = tpl.Kind
		}
		d.Amount = tpl.Amount
		d.Currency = tpl.Currency
		if tpl.AccountID > 0 {
			d.AccountID = tpl.AccountID
		}
		d.ToAccountID = tpl.ToAccountID
		d.CategoryID = tpl.CategoryID
		d.Tags = append(d.Tags, tpl.Tags...)
		d.Description = tpl.Description
		if d.Description == "" {
			d.Description = tpl.Names[0]
		}
		d.Payee = tpl.Payee
		d.Recognized = append(d.Recognized, "template "+tpl.Names[0])
		tokens = tokens[consumed:]
	}

	// 2. walk the rest: amount, currency, dates, time, @account, >account, #tag
	tokens = joinSums(tokens)
	var words []string
	amountSeen := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		low := fold(strings.Trim(tok, ",;"))
		switch {
		case strings.HasPrefix(tok, "#") && len(tok) > 1:
			d.Tags = append(d.Tags, strings.ReplaceAll(tok[1:], "_", " "))
			continue
		case strings.HasPrefix(tok, "@") && len(tok) > 1:
			if a := findAccount(c.Accounts, tok[1:]); a != nil {
				d.AccountID = a.ID
				d.Recognized = append(d.Recognized, "from "+a.Name)
				continue
			}
		case (strings.HasPrefix(tok, ">") || strings.HasPrefix(tok, "→")) && len(strings.TrimLeft(tok, ">→")) > 0:
			if a := findAccount(c.Accounts, strings.TrimLeft(tok, ">→")); a != nil {
				d.ToAccountID = a.ID
				d.Kind = "transfer"
				d.Recognized = append(d.Recognized, "to "+a.Name)
				continue
			}
		}
		if m := reOffset.FindStringSubmatch(low); m != nil {
			n, _ := strconv.Atoi(m[2])
			if m[1] == "-" {
				n = -n
			}
			d.Date = c.Today.AddDate(0, 0, n).Format("2006-01-02")
			d.Recognized = append(d.Recognized, d.Date)
			continue
		}
		if off, ok := dayWords[low]; ok {
			d.Date = c.Today.AddDate(0, 0, off).Format("2006-01-02")
			d.Recognized = append(d.Recognized, tok)
			continue
		}
		if wd, ok := weekdayWords[low]; ok && len(low) > 3 {
			back := (int(c.Today.Weekday()) - int(wd) + 7) % 7
			if back == 0 {
				back = 7
			}
			d.Date = c.Today.AddDate(0, 0, -back).Format("2006-01-02")
			d.Recognized = append(d.Recognized, tok)
			continue
		}
		if m := reISODate.FindStringSubmatch(low); m != nil {
			d.Date = low
			d.Recognized = append(d.Recognized, low)
			continue
		}
		if m := reDate.FindStringSubmatch(low); m != nil && amountSeen {
			if dt, ok := dayMonth(m, c.Today); ok {
				d.Date = dt
				d.Recognized = append(d.Recognized, dt)
				continue
			}
		}
		if m := reTime.FindStringSubmatch(low); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			if h < 24 && mi < 60 {
				d.Time = pad2(h) + ":" + pad2(mi)
				d.Recognized = append(d.Recognized, d.Time)
				continue
			}
		}
		if !amountSeen {
			if sign, cur, v, calc, ok := readAmount(tok); ok {
				amountSeen = true
				if sign == "+" {
					d.Kind = "income"
					d.Recognized = append(d.Recognized, "income")
				}
				// a currency word may follow: "20 eur", "20 maraka"
				if cur == "" && i+1 < len(tokens) {
					nx := fold(tokens[i+1])
					if code, ok := currencyWords[nx]; ok {
						cur = code
						i++
					} else if len(nx) == 3 && currency.Valid(nx) && strings.ToUpper(tokens[i+1]) == tokens[i+1] {
						cur = strings.ToUpper(nx)
						i++
					}
				}
				if cur != "" {
					d.Currency = cur
					d.Recognized = append(d.Recognized, cur)
				}
				minor := toMinor(v, d.Currency, c, d.AccountID)
				d.Amount = &minor
				d.Calc = calc
				continue
			}
		}
		words = append(words, tok)
	}

	// 3. what is left is the description (or extends the template's)
	rest := strings.TrimSpace(strings.Join(words, " "))
	if rest != "" {
		if tpl != nil {
			d.Description = rest
		} else {
			d.Description = capFirst(rest)
		}
	}
	if d.Kind == "transfer" && d.ToAccountID == 0 {
		d.Kind = "expense"
	}
	if d.Kind == "transfer" {
		d.CategoryID = 0
	}

	// 4. category: a category named in the text, else what this description usually gets
	if d.CategoryID == 0 && d.Kind != "transfer" {
		if id, name := categoryInText(c.Categories, words); id > 0 {
			d.CategoryID = id
			d.Recognized = append(d.Recognized, "category "+name)
		} else if c.Learn != nil && d.Description != "" {
			d.CategoryID = c.Learn(d.Description)
		}
	}
	return d
}

// readAmount reads "20", "€20", "20eur", "+1500" or a sum like "4800-10.23"
// (calc is the sum written out, empty for a plain number). "3x", "2kom" are
// not money.
func readAmount(tok string) (sign, cur string, v float64, calc string, ok bool) {
	m := reAmount.FindStringSubmatch(tok)
	if m == nil {
		if m = reExpr.FindStringSubmatch(tok); m == nil {
			return
		}
	}
	if m[2] != "" {
		cur = currencyWords[m[2]]
	}
	if suf := fold(strings.TrimSuffix(m[4], ".")); suf != "" {
		code, known := currencyWords[suf]
		if !known && len(suf) == 3 && currency.Valid(suf) {
			code, known = strings.ToUpper(suf), true
		}
		if !known {
			return "", "", 0, "", false
		}
		cur = code
	}
	if v, ok = parseNumber(m[3]); ok {
		return m[1], cur, v, "", true
	}
	if v, calc, ok = evalSum(m[3]); ok && v > 0 {
		return m[1], cur, v, calc, true
	}
	return "", "", 0, "", false
}

// joinSums glues a sum typed with spaces ("4800 - 10.23", "20 + 5 eur") into
// one token so it reads like "4800-10.23". Only joins what then reads as an
// amount, so "kafa 3 -2d" keeps its day offset.
func joinSums(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		best := i
		joined := tokens[i]
		for j := i + 1; j < len(tokens) && j <= i+8; j++ {
			// only across an operator: "5 2026-10-12" is an amount and a date
			if !strings.ContainsAny(lastRune(tokens[j-1])+firstRune(tokens[j]), "+-*/x×÷()") {
				break
			}
			joined += tokens[j]
			if _, _, _, calc, ok := readAmount(joined); ok && calc != "" {
				best = j
			}
		}
		out = append(out, strings.Join(tokens[i:best+1], ""))
		i = best
	}
	return out
}

func firstRune(s string) string {
	_, n := utf8.DecodeRuneInString(s)
	return s[:n]
}

func lastRune(s string) string {
	_, n := utf8.DecodeLastRuneInString(s)
	return s[len(s)-n:]
}

// evalSum works out + − × ÷ with brackets over numbers written the way
// parseNumber reads them; "x" and "×" multiply, "÷" divides. It needs at least
// one operator, and gives back the sum written out with spaces.
func evalSum(s string) (float64, string, bool) {
	var toks []string
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsDigit(r) || r == '.' || r == ',' {
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.' || s[j] == ',') {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
			continue
		}
		switch r {
		case '+', '-', '(', ')':
			toks = append(toks, string(r))
		case '*', 'x', '×':
			toks = append(toks, "×")
		case '/', '÷':
			toks = append(toks, "÷")
		default:
			return 0, "", false
		}
		i += size
	}
	p := sumParser{toks: toks}
	v := p.expr()
	if p.bad || p.pos != len(toks) || p.ops == 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, "", false
	}
	calc := strings.Join(toks, " ")
	calc = strings.NewReplacer("( ", "(", " )", ")", "-", "−").Replace(calc)
	return v, calc, true
}

type sumParser struct {
	toks []string
	pos  int
	ops  int
	bad  bool
}

func (p *sumParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *sumParser) expr() float64 {
	v := p.term()
	for op := p.peek(); op == "+" || op == "-"; op = p.peek() {
		p.pos++
		p.ops++
		if op == "+" {
			v += p.term()
		} else {
			v -= p.term()
		}
	}
	return v
}

func (p *sumParser) term() float64 {
	v := p.factor()
	for op := p.peek(); op == "×" || op == "÷"; op = p.peek() {
		p.pos++
		p.ops++
		f := p.factor()
		if op == "×" {
			v *= f
		} else if f == 0 {
			p.bad = true
		} else {
			v /= f
		}
	}
	return v
}

func (p *sumParser) factor() float64 {
	t := p.peek()
	p.pos++
	switch {
	case t == "(":
		v := p.expr()
		if p.peek() != ")" {
			p.bad = true
		}
		p.pos++
		return v
	case t != "" && t[0] >= '0' && t[0] <= '9':
		v, ok := parseNumber(t)
		if !ok {
			p.bad = true
		}
		return v
	}
	p.bad = true
	return 0
}

func looksLikeAmount(s string) bool {
	return reAmount.MatchString(strings.Fields(s)[0])
}

func parseNumber(s string) (float64, bool) {
	s = strings.ReplaceAll(s, " ", "")
	if reThousand.MatchString(s) {
		// 1.500 or 1.234,56 — dots group thousands
		s = strings.ReplaceAll(s, ".", "")
	}
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && v >= 0
}

func toMinor(v float64, code string, c Context, accountID int64) int64 {
	if code == "" {
		for _, a := range c.Accounts {
			if a.ID == accountID {
				code = a.Currency
			}
		}
	}
	return int64(math.Round(v * math.Pow10(currency.Decimals(code))))
}

func dayMonth(m []string, today time.Time) (string, bool) {
	day, _ := strconv.Atoi(m[1])
	mon, _ := strconv.Atoi(m[2])
	year := today.Year()
	if m[3] != "" {
		year, _ = strconv.Atoi(m[3])
	}
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return "", false
	}
	t := time.Date(year, time.Month(mon), day, 0, 0, 0, 0, time.Local)
	if m[3] == "" && t.After(today.AddDate(0, 0, 1)) {
		t = t.AddDate(-1, 0, 0) // "28.12." typed in January means last year
	}
	return t.Format("2006-01-02"), true
}

func findAccount(accts []Account, q string) *Account {
	q = fold(strings.ReplaceAll(q, "_", " "))
	for i := range accts {
		if fold(accts[i].Name) == q {
			return &accts[i]
		}
	}
	for i := range accts {
		n := fold(accts[i].Name)
		if strings.HasPrefix(n, q) {
			return &accts[i]
		}
		for _, w := range strings.Fields(n) {
			if strings.HasPrefix(w, q) && len(q) >= 3 {
				return &accts[i]
			}
		}
	}
	return nil
}

func categoryInText(cats []Named, words []string) (int64, string) {
	for _, w := range words {
		fw := fold(strings.Trim(w, ",.;:!?"))
		if len(fw) < 3 {
			continue
		}
		for _, c := range cats {
			if fold(c.Name) == fw {
				return c.ID, c.Name
			}
		}
	}
	return 0, ""
}

func capFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Suggestion is a template that fits the words being typed, with the line
// that books it: the template's name, then whatever else was typed (amount,
// day, #tags…).
type Suggestion struct {
	TemplateID int64  `json:"template_id"`
	Name       string `json:"name"`
	Alias      string `json:"alias,omitempty"` // the alias that matched, when it wasn't the name
	Line       string `json:"line"`
}

// Suggest lists the templates the leading words of a line could mean: names
// or aliases starting with them, names where every typed word starts a word
// ("athena" → "Plata Athena"), or a name followed by more words. Exact
// matches first, then prefixes, then the rest; ties keep the templates'
// order (pinned, then most used). query is the folded words it matched on.
func Suggest(input string, c Context) (query string, out []Suggestion) {
	raw := strings.TrimSpace(input)
	lead := ""
	if strings.HasPrefix(raw, "+") && len(raw) > 1 && !unicode.IsDigit(rune(raw[1])) {
		lead = "+"
		raw = strings.TrimSpace(raw[1:])
	}
	tokens := strings.Fields(raw)
	n := 0
	for n < len(tokens) && n < 5 && isNameWord(tokens[n]) {
		n++
	}
	if n == 0 {
		return "", nil
	}
	typed := tokens[:n]
	query = fold(strings.Join(typed, " "))
	qWords := strings.Fields(query)

	type hit struct {
		Suggestion
		score int
	}
	var hits []hit
	for _, t := range c.Templates {
		best := hit{score: 0}
		for ni, name := range t.Names {
			fn := fold(name)
			score, extra := 0, []string(nil)
			nw := len(strings.Fields(fn))
			switch {
			case fn == query:
				score = 4
			case strings.HasPrefix(fn, query):
				score = 3
			case wordsStart(strings.Fields(fn), qWords):
				score = 2
			case n > nw && fold(strings.Join(typed[:nw], " ")) == fn:
				score, extra = 1, typed[nw:]
			}
			if score > best.score {
				best = hit{Suggestion{TemplateID: t.ID, Name: t.Names[0]}, score}
				if ni > 0 {
					best.Alias = name
				}
				best.Line = strings.Join(append(append([]string{lead + t.Names[0]}, extra...), tokens[n:]...), " ")
			}
		}
		if best.score > 0 {
			hits = append(hits, best)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	for _, h := range hits {
		out = append(out, h.Suggestion)
	}
	return query, out
}

// isNameWord: a word that may still be part of a template name, not the
// amount, a day, @account, >account or #tag that follows it.
func isNameWord(tok string) bool {
	if strings.ContainsAny(tok, "0123456789") || strings.ContainsAny(firstRune(tok), "@#>→+-") {
		return false
	}
	low := fold(tok)
	if _, ok := dayWords[low]; ok {
		return false
	}
	if _, ok := weekdayWords[low]; ok && len(low) > 3 {
		return false
	}
	return true
}

// wordsStart: every typed word starts some word of the name, each a different one.
func wordsStart(name, typed []string) bool {
	used := make([]bool, len(name))
	for _, q := range typed {
		found := false
		for i, w := range name {
			if !used[i] && strings.HasPrefix(w, q) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
