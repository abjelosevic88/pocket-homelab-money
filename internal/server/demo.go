package server

import (
	"context"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// seedDemo fills an empty database with half a year of a believable household.
func (s *Server) seedDemo(ctx context.Context) error {
	if s.store.Counts(ctx)["accounts"] > 0 {
		return nil
	}
	owner, err := s.importOwner(ctx)
	if err != nil {
		owner, err = s.firstOrSystemUser(ctx, "admin@localhost", "Admin")
		if err != nil {
			return err
		}
	}
	ctx = context.WithValue(ctx, userKey, owner)
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	st.HomeCurrency, st.Title = "EUR", store.DefaultTitle
	_ = s.store.SaveSettings(ctx, owner.ID, st, true)
	rng := rand.New(rand.NewSource(42))
	today := time.Now()
	start := today.AddDate(0, -6, 0)
	d := func(t time.Time) string { return t.Format("2006-01-02") }

	acct := func(name, kind, cur, icon string, opening int64) int64 {
		a := &store.Account{OwnerID: owner.ID, Name: name, Kind: kind, Currency: cur, Icon: icon, OpeningBalance: opening, OpeningDate: d(start), IncludeNetWorth: true, Everyday: kind != "savings"}
		_ = s.store.CreateAccount(ctx, a)
		return a.ID
	}
	main := acct("Main account", "checking", "EUR", "🏦", 2_400_00)
	savings := acct("Savings", "savings", "EUR", "🐷", 8_500_00)
	cash := acct("Cash wallet", "cash", "EUR", "💵", 480_00)
	card := acct("Credit card", "credit", "EUR", "💳", 0)
	usd := acct("USD account", "checking", "USD", "💵", 650_00)

	cat := func(name, icon, kind string) int64 {
		c := &store.Category{UserID: owner.ID, Name: name, Icon: icon, Kind: kind}
		_ = s.store.CreateCategory(ctx, c)
		return c.ID
	}
	groceries := cat("Groceries", "🛒", "expense")
	bakery := cat("Bakery", "🥐", "expense")
	restaurants := cat("Restaurants", "🍽️", "expense")
	coffee := cat("Coffee", "☕", "expense")
	transport := cat("Transport", "🚇", "expense")
	fuel := cat("Fuel", "⛽", "expense")
	rent := cat("Rent", "🏠", "expense")
	utilities := cat("Utilities", "🧾", "expense")
	subsCat := cat("Subscriptions", "📱", "expense")
	health := cat("Health", "💊", "expense")
	shopping := cat("Shopping", "🛍️", "expense")
	travel := cat("Travel", "✈️", "expense")
	gifts := cat("Gifts", "🎁", "expense")
	salary := cat("Salary", "💰", "income")
	side := cat("Side income", "💼", "income")
	_ = s.store.UpdateCategory(ctx, &store.Category{ID: bakery, UserID: owner.ID, Name: "Bakery", Icon: "🥐", Kind: "expense", ParentID: &groceries})

	add := func(kind, date, tm string, account int64, amount int64, cur string, desc, payee string, category int64, tags ...string) {
		in := txIn{Kind: &kind, Date: &date, Time: &tm, AccountID: &account, Amount: &amount, Description: &desc, Payee: &payee, Tags: &tags, Source: "manual", SkipRules: true}
		if cur != "" {
			in.Currency = &cur
		}
		if category > 0 {
			in.CategoryID = &category
		}
		_, _, _ = s.createTx(ctx, &in)
	}
	clock := func() string {
		h := 7 + rng.Intn(14)
		return time.Date(2000, 1, 1, h, rng.Intn(60), 0, 0, time.UTC).Format("15:04")
	}
	price := func(lo, hi int) int64 { return int64(lo + rng.Intn(hi-lo+1)) }

	// subscriptions first so their payments link up
	sub := func(name string, amount int64, cat int64, day int, freq string, mode string, notify int, icon string) {
		next := time.Date(today.Year(), today.Month(), day, 0, 0, 0, 0, time.Local)
		if freq == "yearly" {
			next = time.Date(today.Year(), today.Month()+2, day, 0, 0, 0, 0, time.Local)
		}
		first := next.AddDate(0, -6, 0)
		if freq == "yearly" {
			first = next.AddDate(-1, 0, 0)
		}
		x := &store.Subscription{OwnerID: owner.ID, Name: name, Kind: "expense", Amount: amount, Currency: "EUR", AccountID: &main, CategoryID: &cat, Tags: []string{},
			Freq: freq, Every: 1, StartDate: d(first), NextDate: d(first), Mode: mode, NotifyDays: notify, Active: true, Icon: icon}
		if err := s.store.CreateSubscription(ctx, x); err != nil {
			return
		}
		// book the past ones
		for n := 0; n < 8; n++ {
			cur, _ := s.store.GetSubscription(ctx, x.ID)
			if cur.NextDate > d(today) {
				break
			}
			_, _, _ = s.bookSubscription(ctx, cur, cur.NextDate, nil, nil)
		}
	}
	sub("Rent", 950_00, rent, 1, "monthly", "auto", 3, "🏠")
	sub("Electricity", 64_00, utilities, 12, "monthly", "remind", 2, "⚡")
	sub("Netflix", 13_99, subsCat, 17, "monthly", "auto", -1, "🎬")
	sub("Spotify", 10_99, subsCat, 5, "monthly", "auto", -1, "🎧")
	sub("Gym", 39_00, health, 3, "monthly", "auto", -1, "🏋️")
	sub("Phone", 25_00, utilities, 20, "monthly", "auto", -1, "📞")
	sub("Car insurance", 420_00, transport, 9, "yearly", "remind", 14, "🛡️")
	// a price change the insights should notice
	if subs, _ := s.store.ListSubscriptions(ctx, owner.ID); len(subs) > 0 {
		for _, x := range subs {
			if x.Name == "Netflix" {
				x.Amount = 12_99
				_ = s.store.UpdateSubscription(ctx, &x)
			}
		}
	}

	for day := start; !day.After(today); day = day.AddDate(0, 0, 1) {
		ds := d(day)
		wd := day.Weekday()
		if day.Day() == 25 {
			add("income", ds, "09:00", main, 3_850_00, "", "Salary", "Acme GmbH", salary)
		}
		if day.Day() == 6 {
			// pay the credit card off in full
			if c, err := s.store.GetAccount(ctx, card); err == nil && c.Balance < 0 {
				kind, amt, desc, tm := "transfer", -c.Balance, "Credit card repayment", "09:00"
				_, _, _ = s.createTx(ctx, &txIn{Kind: &kind, Date: &ds, Time: &tm, AccountID: &main, ToAccountID: &card, Amount: &amt, Description: &desc, SkipRules: true})
			}
		}
		if day.Day() == 10 && day.Month()%2 == 0 {
			kind, amt, desc, tm := "transfer", int64(200_00), "ATM", "17:40"
			_, _, _ = s.createTx(ctx, &txIn{Kind: &kind, Date: &ds, Time: &tm, AccountID: &main, ToAccountID: &cash, Amount: &amt, Description: &desc, SkipRules: true})
		}
		if day.Day() == 26 {
			kind, amt, desc, tm := "transfer", int64(400_00), "Monthly savings", "10:00"
			_, _, _ = s.createTx(ctx, &txIn{Kind: &kind, Date: &ds, Time: &tm, AccountID: &main, ToAccountID: &savings, Amount: &amt, Description: &desc, SkipRules: true})
		}
		if rng.Float64() < 0.75 {
			add("expense", ds, "08:"+[]string{"05", "15", "40"}[rng.Intn(3)], card, price(240, 420), "", "Coffee", "Café Central", coffee)
		}
		if rng.Float64() < 0.35 {
			add("expense", ds, clock(), cash, price(180, 650), "", "Bakery", "Padaria", bakery)
		}
		if wd == time.Saturday || (wd == time.Wednesday && rng.Float64() < 0.6) {
			add("expense", ds, clock(), card, price(3800, 11200), "", "Weekly shop", "Continente", groceries)
		}
		if (wd == time.Friday || wd == time.Saturday) && rng.Float64() < 0.6 {
			add("expense", ds, "20:"+[]string{"10", "30", "45"}[rng.Intn(3)], card, price(2200, 7800), "", "Dinner", []string{"Taberna", "Sushi Bar", "Pizzeria Roma", "Tasca"}[rng.Intn(4)], restaurants)
		}
		if wd >= time.Monday && wd <= time.Friday && rng.Float64() < 0.5 {
			add("expense", ds, "12:"+[]string{"20", "35", "50"}[rng.Intn(3)], card, price(850, 1450), "", "Lunch", "", restaurants)
		}
		if wd == time.Monday && rng.Float64() < 0.8 {
			add("expense", ds, clock(), main, price(5500, 7200), "", "Fuel", "Galp", fuel)
		}
		if rng.Float64() < 0.12 {
			add("expense", ds, clock(), card, price(1200, 3200), "", "Uber", "Uber", transport)
		}
		if rng.Float64() < 0.06 {
			add("expense", ds, clock(), card, price(1500, 12000), "", []string{"Shoes", "Books", "Headphones", "Kitchen stuff"}[rng.Intn(4)], "", shopping)
		}
		if rng.Float64() < 0.04 {
			add("expense", ds, clock(), main, price(900, 4500), "", "Pharmacy", "Farmácia", health)
		}
		if rng.Float64() < 0.03 {
			add("expense", ds, clock(), cash, price(2000, 6000), "", "Birthday present", "", gifts)
		}
		if day.Day() == 14 && rng.Float64() < 0.5 {
			add("income", ds, "16:00", usd, price(30000, 60000), "", "Freelance invoice", "Client Inc.", side)
		}
	}
	// a weekend in Porto with its own tag, partly paid in USD from abroad
	trip := today.AddDate(0, -2, -3)
	for i := 0; i < 3; i++ {
		ds := d(trip.AddDate(0, 0, i))
		add("expense", ds, "13:10", card, price(2500, 4800), "", "Lunch in Porto", "", restaurants, "porto 2026")
		add("expense", ds, "21:00", card, price(3500, 6500), "", "Dinner in Porto", "", restaurants, "porto 2026")
	}
	add("expense", d(trip), "08:30", main, 189_00, "", "Hotel Ribeira", "Booking.com", travel, "porto 2026")
	add("expense", d(trip.AddDate(0, 0, -20)), "19:30", usd, 86_40, "", "Train tickets", "CP", travel, "porto 2026")
	add("expense", d(today.AddDate(0, 0, -9)), "18:20", card, 59_00, "USD", "App Store", "Apple", subsCat)

	// salary as a tracked recurring income, so the forecast knows pay is coming
	payday := time.Date(today.Year(), today.Month(), 25, 0, 0, 0, 0, time.Local)
	if today.Day() > 25 {
		payday = payday.AddDate(0, 1, 0)
	}
	_ = s.store.CreateSubscription(ctx, &store.Subscription{OwnerID: owner.ID, Name: "Salary", Kind: "income", Amount: 3_850_00, Currency: "EUR", AccountID: &main, CategoryID: &salary,
		Tags: []string{}, Freq: "monthly", Every: 1, StartDate: d(payday), NextDate: d(payday), Mode: "track", NotifyDays: -1, Active: true, Icon: "💰", MatchText: "salary"})

	// templates for one-tap entry
	tpl := func(name string, amount int64, cat int64, account int64, aliases string, pinned bool) {
		a := amount
		t := &store.Template{OwnerID: owner.ID, Name: name, Kind: "expense", CategoryID: &cat, AccountID: &account, Tags: []string{}, Aliases: aliases, Pinned: pinned}
		if amount > 0 {
			t.Amount = &a
		}
		_ = s.store.CreateTemplate(ctx, t)
	}
	tpl("Coffee", 2_80, coffee, card, "kafa, cafe", true)
	tpl("Bakery", 0, bakery, cash, "", true)
	tpl("Groceries", 0, groceries, card, "shop", true)
	tpl("Fuel", 0, fuel, main, "gas", false)
	tpl("Lunch", 0, restaurants, card, "", false)
	tpl("Uber", 0, transport, card, "taxi", false)

	// budgets
	month, _ := periodBounds(d(today), "monthly", 1, 1)
	bud := func(name string, amount int64, cats []int64, rollover bool, icon string) {
		_ = s.store.CreateBudget(ctx, &store.Budget{OwnerID: owner.ID, Name: name, Amount: amount, Period: "monthly", Rollover: rollover, CategoryIDs: cats, TagNames: []string{}, StartDate: d(start), AlertPct: 80, Icon: icon})
	}
	bud("Food at home", 650_00, []int64{groceries}, false, "🛒")
	bud("Eating out", 480_00, []int64{restaurants, coffee}, false, "🍽️")
	bud("Getting around", 320_00, []int64{transport, fuel}, false, "🚗")
	bud("Shopping", 220_00, []int64{shopping, gifts}, true, "🛍️")
	_ = month

	// goals
	g1 := &store.Goal{OwnerID: owner.ID, Name: "Japan 2027", Target: 4_000_00, Currency: "EUR", TargetDate: d(today.AddDate(0, 7, 0)), Icon: "🗾"}
	_ = s.store.CreateGoal(ctx, g1)
	for i := 5; i >= 0; i-- {
		_ = s.store.AddGoalEntry(ctx, &store.GoalEntry{GoalID: g1.ID, Date: d(today.AddDate(0, -i, -2)), Amount: price(25000, 40000), Note: "monthly"})
	}
	_ = s.store.CreateGoal(ctx, &store.Goal{OwnerID: owner.ID, Name: "Emergency fund", Target: 12_000_00, Currency: "EUR", AccountID: &savings, Icon: "🛟"})

	// a rule
	cid := transport
	_ = s.store.CreateRule(ctx, &store.Rule{OwnerID: owner.ID, Name: "Ride-hailing is transport", Match: "any", Active: true,
		Conditions: []store.Condition{{Field: "text", Op: "contains", Value: "uber"}, {Field: "text", Op: "contains", Value: "bolt"}}, Actions: store.RuleActions{CategoryID: &cid}})
	_ = cash
	s.log.Info("demo data seeded")
	return nil
}
