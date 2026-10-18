package store

// User is an account that can sign in.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	CreatedAt    string `json:"created_at"`
}

// APIToken is a bearer token (only the prefix is ever shown again).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Settings are server-wide preferences plus integration credentials.
type Settings struct {
	Title         string `json:"title"`
	HomeCurrency  string `json:"home_currency"`
	DateFormat    string `json:"date_format"`
	TimeFormat    string `json:"time_format"`
	WeekStart     int    `json:"week_start"`      // 0 Sunday, 1 Monday
	MonthStartDay int    `json:"month_start_day"` // 1..28: a "month" runs from this day (payday months)
	FXAuto        bool   `json:"fx_auto"`
	DefaultAcct   int64  `json:"default_account_id"`
	QuickLang     string `json:"quick_lang"` // date words understood by quick add: en | bs | auto

	FireflyURL    string `json:"firefly_url"`
	FireflyToken  string `json:"firefly_token"`
	FireflyMirror bool   `json:"firefly_mirror"` // keep pulling from Firefly every 30 min (parallel run)
	PicoURL       string `json:"pico_url"`

	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"`
	NotifyHour   int    `json:"notify_hour"`
	NotifyDigest bool   `json:"notify_digest"` // weekly summary on Sunday evening

	AIURL        string `json:"ai_url"`
	AIKey        string `json:"ai_key"`
	AIModel      string `json:"ai_model"`
	AITranscribe string `json:"ai_transcribe_model"`
	AILanguage   string `json:"ai_language"` // ISO-639-1 hint for dictation, "" = auto
	AIContext    string `json:"ai_context"`  // extra words the model should know ("maraka = BAM")

	TravelURL       string `json:"travel_url"`
	TravelToken     string `json:"travel_token"`
	TravelPublicURL string `json:"travel_public_url"`
	TravelAutoTag   bool   `json:"travel_auto_tag"` // add the running trip's tag to new expenses

	SetupComplete bool `json:"setup_complete"`
}

// Account holds money in one currency.
type Account struct {
	ID              int64  `json:"id"`
	OwnerID         int64  `json:"owner_id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Currency        string `json:"currency"`
	OpeningBalance  int64  `json:"opening_balance"`
	OpeningDate     string `json:"opening_date"`
	Institution     string `json:"institution"`
	Number          string `json:"number"`
	Color           string `json:"color"`
	Icon            string `json:"icon"`
	CreditLimit     int64  `json:"credit_limit"`
	IncludeNetWorth bool   `json:"include_net_worth"`
	Archived        bool   `json:"archived"`
	SortOrder       int    `json:"sort_order"`
	Notes           string `json:"notes"`
	ExternalID      string `json:"external_id"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`

	Role        string          `json:"role,omitempty"` // the caller's: owner | editor | viewer
	Members     []AccountMember `json:"members,omitempty"`
	Balance     int64           `json:"balance"`
	BalanceHome int64           `json:"balance_home"`
	TxCount     int             `json:"tx_count"`
	LastTxDate  string          `json:"last_tx_date"`
}

// AccountMember shares an account with another user.
type AccountMember struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// Category groups transactions.
type Category struct {
	ID         int64  `json:"id"`
	ParentID   *int64 `json:"parent_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Icon       string `json:"icon"`
	Color      string `json:"color"`
	Archived   bool   `json:"archived"`
	SortOrder  int    `json:"sort_order"`
	ExternalID string `json:"external_id"`
	TxCount    int    `json:"tx_count"`
	FullName   string `json:"full_name"` // "Parent › Child"
}

// Tag is a free label ("lustica 2026", "skoda").
type Tag struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Archived   bool   `json:"archived"`
	ExternalID string `json:"external_id"`
	TxCount    int    `json:"tx_count"`
	LastUsed   string `json:"last_used"`
	CreatedAt  string `json:"created_at"`
}

// Transaction is one money movement.
type Transaction struct {
	ID              int64    `json:"id"`
	Kind            string   `json:"kind"`
	Date            string   `json:"date"`
	Time            string   `json:"time"`
	AccountID       int64    `json:"account_id"`
	Amount          int64    `json:"amount"`
	ToAccountID     *int64   `json:"to_account_id"`
	ToAmount        int64    `json:"to_amount"`
	ForeignAmount   int64    `json:"foreign_amount"`
	ForeignCurrency string   `json:"foreign_currency"`
	AmountHome      int64    `json:"amount_home"`
	FXRate          float64  `json:"fx_rate"`
	Description     string   `json:"description"`
	Payee           string   `json:"payee"`
	CategoryID      *int64   `json:"category_id"`
	Notes           string   `json:"notes"`
	SubscriptionID  *int64   `json:"subscription_id"`
	Source          string   `json:"source"`
	ExternalID      string   `json:"external_id"`
	Lat             *float64 `json:"lat"`
	Lng             *float64 `json:"lng"`
	CreatedBy       *int64   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`

	Tags            []string     `json:"tags"`
	AccountName     string       `json:"account_name"`
	Currency        string       `json:"currency"` // the account's
	ToAccountName   string       `json:"to_account_name,omitempty"`
	ToCurrency      string       `json:"to_currency,omitempty"`
	CategoryName    string       `json:"category_name"`
	CategoryIcon    string       `json:"category_icon"`
	CategoryColor   string       `json:"category_color"`
	SubscriptionNm  string       `json:"subscription_name,omitempty"`
	CreatedByName   string       `json:"created_by_name,omitempty"`
	Attachments     []Attachment `json:"attachments,omitempty"`
	AttachmentCount int          `json:"attachment_count"`
}

// Attachment is a receipt or document on a transaction.
type Attachment struct {
	ID            int64  `json:"id"`
	TransactionID int64  `json:"transaction_id"`
	Name          string `json:"name"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Path          string `json:"-"`
	CreatedAt     string `json:"created_at"`
}

// Template is a one-tap transaction.
type Template struct {
	ID          int64    `json:"id"`
	OwnerID     int64    `json:"owner_id"`
	Name        string   `json:"name"`
	Aliases     string   `json:"aliases"`
	Kind        string   `json:"kind"`
	Amount      *int64   `json:"amount"`
	Currency    string   `json:"currency"`
	AccountID   *int64   `json:"account_id"`
	ToAccountID *int64   `json:"to_account_id"`
	CategoryID  *int64   `json:"category_id"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Payee       string   `json:"payee"`
	Notes       string   `json:"notes"`
	Pinned      bool     `json:"pinned"`
	UseCount    int      `json:"use_count"`
	LastUsedAt  string   `json:"last_used_at"`
	SortOrder   int      `json:"sort_order"`
	ExternalID  string   `json:"external_id"`
	CreatedAt   string   `json:"created_at"`

	CategoryName string `json:"category_name,omitempty"`
	CategoryIcon string `json:"category_icon,omitempty"`
	AccountName  string `json:"account_name,omitempty"`
}

// Subscription is a bill, a subscription or any other repeating payment.
type Subscription struct {
	ID          int64    `json:"id"`
	OwnerID     int64    `json:"owner_id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Amount      int64    `json:"amount"`
	AmountMax   int64    `json:"amount_max"`
	Currency    string   `json:"currency"`
	AccountID   *int64   `json:"account_id"`
	ToAccountID *int64   `json:"to_account_id"`
	CategoryID  *int64   `json:"category_id"`
	Tags        []string `json:"tags"`
	Payee       string   `json:"payee"`
	Description string   `json:"description"`
	Freq        string   `json:"freq"`
	Every       int      `json:"every"`
	StartDate   string   `json:"start_date"`
	NextDate    string   `json:"next_date"`
	EndDate     string   `json:"end_date"`
	Mode        string   `json:"mode"`
	NotifyDays  int      `json:"notify_days"`
	MatchText   string   `json:"match_text"`
	URL         string   `json:"url"`
	Icon        string   `json:"icon"`
	Notes       string   `json:"notes"`
	Active      bool     `json:"active"`
	NotifiedFor string   `json:"notified_for"`
	ExternalID  string   `json:"external_id"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`

	CategoryName string `json:"category_name,omitempty"`
	CategoryIcon string `json:"category_icon,omitempty"`
	AccountName  string `json:"account_name,omitempty"`
	DaysLeft     int    `json:"days_left"`
	Status       string `json:"status"` // overdue | today | soon | upcoming | ended | paused
	MonthlyHome  int64  `json:"monthly_home"`
	YearlyHome   int64  `json:"yearly_home"`
	LastPaid     string `json:"last_paid"`
	LastAmount   int64  `json:"last_amount"`
	PaidCount    int    `json:"paid_count"`
}

// Budget limits spending per period.
type Budget struct {
	ID          int64    `json:"id"`
	OwnerID     int64    `json:"owner_id"`
	Name        string   `json:"name"`
	Amount      int64    `json:"amount"`
	Period      string   `json:"period"`
	Rollover    bool     `json:"rollover"`
	CategoryIDs []int64  `json:"category_ids"`
	TagNames    []string `json:"tag_names"`
	StartDate   string   `json:"start_date"`
	AlertPct    int      `json:"alert_pct"`
	Notified    string   `json:"notified"`
	Color       string   `json:"color"`
	Icon        string   `json:"icon"`
	Archived    bool     `json:"archived"`
	SortOrder   int      `json:"sort_order"`
	CreatedAt   string   `json:"created_at"`

	Status *BudgetStatus `json:"status,omitempty"`
}

// BudgetStatus is how a budget is doing in one period.
type BudgetStatus struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Spent       int64   `json:"spent"`
	Carry       int64   `json:"carry"`     // rolled over from earlier periods (+ left, − overspent)
	Available   int64   `json:"available"` // amount + carry
	Left        int64   `json:"left"`
	Pct         float64 `json:"pct"`         // spent / available
	ElapsedPct  float64 `json:"elapsed_pct"` // share of the period gone
	Projected   int64   `json:"projected"`   // spent at this pace by period end
	DailyLeft   int64   `json:"daily_left"`  // what can still be spent per remaining day
	State       string  `json:"state"`       // ok | warn | over
	History     []int64 `json:"history"`     // spent in the previous 6 periods, oldest first
	TxCount     int     `json:"tx_count"`
	PeriodLabel string  `json:"period_label"` // "Oct 2026"
}

// Goal is a savings target.
type Goal struct {
	ID          int64  `json:"id"`
	OwnerID     int64  `json:"owner_id"`
	Name        string `json:"name"`
	Target      int64  `json:"target"`
	Currency    string `json:"currency"`
	TargetDate  string `json:"target_date"`
	AccountID   *int64 `json:"account_id"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	Notes       string `json:"notes"`
	Archived    bool   `json:"archived"`
	CompletedAt string `json:"completed_at"`
	ExternalID  string `json:"external_id"`
	CreatedAt   string `json:"created_at"`

	Saved       int64       `json:"saved"`
	Pct         float64     `json:"pct"`
	MonthlyNeed int64       `json:"monthly_need"` // to reach the target by its date
	AccountName string      `json:"account_name,omitempty"`
	Entries     []GoalEntry `json:"entries,omitempty"`
	MonthsLeft  int         `json:"months_left"`
	OnTrack     *bool       `json:"on_track,omitempty"`
	AvgPerMonth int64       `json:"avg_per_month"`
	ETA         string      `json:"eta"` // at the average pace
}

// GoalEntry is a deposit into (or withdrawal from) a goal.
type GoalEntry struct {
	ID        int64  `json:"id"`
	GoalID    int64  `json:"goal_id"`
	Date      string `json:"date"`
	Amount    int64  `json:"amount"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// Rule auto-fills new transactions.
type Rule struct {
	ID         int64       `json:"id"`
	OwnerID    int64       `json:"owner_id"`
	Name       string      `json:"name"`
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
	Actions    RuleActions `json:"actions"`
	Active     bool        `json:"active"`
	Stop       bool        `json:"stop"`
	SortOrder  int         `json:"sort_order"`
	Hits       int         `json:"hits"`
	LastHitAt  string      `json:"last_hit_at"`
	CreatedAt  string      `json:"created_at"`
}

// Condition is one test of a rule.
type Condition struct {
	Field  string `json:"field"` // description | payee | notes | amount | account | kind | text (description+payee+notes)
	Op     string `json:"op"`    // contains | equals | starts | ends | regex | gt | lt | between
	Value  string `json:"value"`
	Value2 string `json:"value2,omitempty"`
}

// RuleActions are what a matching rule sets.
type RuleActions struct {
	CategoryID  *int64   `json:"category_id,omitempty"`
	AddTags     []string `json:"add_tags,omitempty"`
	Payee       string   `json:"payee,omitempty"`
	Description string   `json:"description,omitempty"`
	AppendNote  string   `json:"append_note,omitempty"`
}

// Activity is one line of the change log.
type Activity struct {
	ID        int64  `json:"id"`
	UserID    *int64 `json:"user_id"`
	UserName  string `json:"user_name"`
	Action    string `json:"action"`
	Subject   string `json:"subject"`
	CreatedAt string `json:"created_at"`
}

// Kinds and lists the UI offers.
var (
	AccountKinds      = []string{"checking", "savings", "cash", "credit", "investment", "loan", "other"}
	TransactionKinds  = []string{"expense", "income", "transfer"}
	Frequencies       = []string{"daily", "weekly", "monthly", "yearly"}
	SubscriptionModes = []string{"remind", "auto", "track"}
	BudgetPeriods     = []string{"weekly", "monthly", "yearly"}
)

// Valid reports whether v is in list.
func Valid(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
