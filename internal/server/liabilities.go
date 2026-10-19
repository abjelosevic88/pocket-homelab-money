package server

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// outstanding is how much is still owed on a liability, as a positive number
// (I owe it: the negative balance; owed to me: the positive balance).
func outstanding(a *store.Account) int64 {
	if a.Direction == "owed" {
		return max(0, a.Balance)
	}
	return max(0, -a.Balance)
}

// AmortRow is one month of a payoff schedule.
type AmortRow struct {
	Date      string `json:"date"`
	Payment   int64  `json:"payment"`
	Interest  int64  `json:"interest"`
	Principal int64  `json:"principal"`
	Remaining int64  `json:"remaining"`
}

// Payoff is where a liability stands and where it is heading.
type Payoff struct {
	Direction     string     `json:"direction"`
	Outstanding   int64      `json:"outstanding"`
	Principal     int64      `json:"principal"`
	Repaid        int64      `json:"repaid"`
	Pct           float64    `json:"pct"`
	Rate          float64    `json:"rate"`
	Payment       int64      `json:"payment"`
	PaymentGuess  bool       `json:"payment_estimated"`
	MonthsLeft    int        `json:"months_left"`
	PayoffDate    string     `json:"payoff_date"`
	InterestLeft  int64      `json:"interest_left"`
	TotalLeft     int64      `json:"total_left"`
	MonthInterest int64      `json:"month_interest"` // at today's balance
	Never         bool       `json:"never"`          // the payment does not cover the interest
	Schedule      []AmortRow `json:"schedule"`
}

// paymentGuess averages what came in (I owe) or went out (owed to me) over the
// last 90 days when no regular payment is set.
func (s *Server) paymentGuess(ctx context.Context, a *store.Account) int64 {
	from := addDays(store.Today(), -90)
	var sum int64
	q := `SELECT COALESCE(SUM(to_amount), 0) FROM transactions WHERE to_account_id = ? AND kind = 'transfer' AND date >= ?`
	if a.Direction == "owed" {
		q = `SELECT COALESCE(SUM(amount), 0) FROM transactions WHERE account_id = ? AND (kind = 'transfer' OR kind = 'expense') AND date >= ?`
	}
	_ = s.store.DB.QueryRowContext(ctx, q, a.ID, from).Scan(&sum)
	return sum / 3
}

func (s *Server) payoff(ctx context.Context, a *store.Account) Payoff {
	p := Payoff{Direction: a.Direction, Outstanding: outstanding(a), Principal: a.Principal, Rate: a.InterestRate, Payment: a.Payment, Schedule: []AmortRow{}}
	if p.Direction == "" {
		p.Direction = "owe"
	}
	if p.Principal > 0 {
		p.Repaid = max(0, p.Principal-p.Outstanding)
		p.Pct = math.Round(float64(p.Repaid)/float64(p.Principal)*1000) / 10
	}
	r := a.InterestRate / 100 / 12
	p.MonthInterest = int64(math.Round(float64(p.Outstanding) * r))
	if p.Payment <= 0 {
		p.Payment, p.PaymentGuess = s.paymentGuess(ctx, a), true
	}
	if p.Outstanding == 0 {
		return p
	}
	if p.Payment <= 0 || p.Payment <= p.MonthInterest {
		p.Never = true
		return p
	}
	// first payment: the next payment day (or a month from today)
	now := time.Now()
	day := a.PaymentDay
	if day <= 0 {
		day = now.Day()
	}
	next := time.Date(now.Year(), now.Month(), min(day, 28), 0, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.AddDate(0, 1, 0)
	}
	rem := p.Outstanding
	for i := 0; rem > 0 && i < 600; i++ {
		interest := int64(math.Round(float64(rem) * r))
		pay := min(p.Payment, rem+interest)
		princ := pay - interest
		rem -= princ
		p.InterestLeft += interest
		p.TotalLeft += pay
		p.MonthsLeft++
		d := next.AddDate(0, i, 0).Format("2006-01-02")
		if len(p.Schedule) < 36 {
			p.Schedule = append(p.Schedule, AmortRow{Date: d, Payment: pay, Interest: interest, Principal: princ, Remaining: max(0, rem)})
		}
		p.PayoffDate = d
	}
	if rem > 0 {
		p.Never = true
	}
	return p
}

func (s *Server) handlePayoff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if !s.canSee(ctx, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, s.payoff(ctx, a))
}

// bookInterest adds a month of interest to a liability: an expense when I owe
// it (the debt grows), income when it is owed to me.
func (s *Server) bookInterest(ctx context.Context, a *store.Account, month string) (*store.Transaction, string) {
	amt := int64(math.Round(float64(outstanding(a)) * a.InterestRate / 100 / 12))
	if amt <= 0 {
		return nil, "nothing to book (no balance or no interest rate)"
	}
	owner, err := s.store.GetUser(ctx, a.OwnerID)
	if err != nil {
		return nil, "no owner"
	}
	uctx := context.WithValue(ctx, userKey, owner)
	kind, catKind := "expense", "expense"
	if a.Direction == "owed" {
		kind, catKind = "income", "income"
	}
	cat, _ := s.store.EnsureCategory(ctx, a.OwnerID, "Interest", catKind)
	date := store.Today()
	desc := "Interest · " + a.Name
	empty := ""
	tags := []string{}
	in := txIn{Kind: &kind, Date: &date, Time: &empty, AccountID: &a.ID, AccountAmount: &amt, Description: &desc, CategoryID: &cat, Tags: &tags, Source: "interest", SkipRules: true}
	t, msg, err := s.createTx(uctx, &in)
	if msg != "" || err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return nil, msg
	}
	a.InterestBooked = month
	_ = s.store.UpdateAccount(ctx, a)
	return t, ""
}

// bookDueInterest runs from the janitor: once a month on the payment day.
func (s *Server) bookDueInterest(ctx context.Context) {
	accts, err := s.store.AllAccounts(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	month := now.Format("2006-01")
	for i := range accts {
		a := &accts[i]
		if !store.IsLiability(a.Kind) || !a.InterestAuto || a.InterestRate <= 0 || a.Archived || a.InterestBooked == month {
			continue
		}
		if now.Day() < max(1, a.PaymentDay) {
			continue
		}
		full, err := s.store.GetAccount(ctx, a.ID)
		if err != nil {
			continue
		}
		if _, msg := s.bookInterest(ctx, full, month); msg != "" {
			s.log.Warn("interest", "account", a.Name, "msg", msg)
		}
	}
}

func (s *Server) handleBookInterest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := idParam(r, "id")
	if role := s.store.AccountRole(ctx, id, actorID(ctx)); role != "owner" && role != "editor" {
		accessDenied(w, role, "read-only account")
		return
	}
	a, err := s.store.GetAccount(ctx, id)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	if !store.IsLiability(a.Kind) {
		writeErr(w, http.StatusBadRequest, "only loans, mortgages and debts carry interest")
		return
	}
	t, msg := s.bookInterest(ctx, a, time.Now().Format("2006-01"))
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}
