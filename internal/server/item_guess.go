package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/fiscal"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// ---- items for hand-typed expenses ----
//
// Right after an expense is typed (created or edited, wherever: the web app,
// quick add, templates, the phone app) the AI reads it in the background
// and, when the description names what was bought ("pica i krofna", 8 KM), writes receipt
// lines for it (Pica 5.00, Krofna 3.00) that add up to the amount. They are
// stored as a receipt of provider "ai" on the transaction, so products,
// groups and their statistics count them like scanned ones. Descriptions
// that name a place or nothing in particular ("Pekara", "Nabavka za kuci")
// get no items. Each transaction is asked about once per amount and
// description; editing either drops the items and asks again. A scanned
// receipt for the same purchase replaces them.

const itemGuessPrompt = `You turn one person's hand-typed expenses into the lines a shop's fiscal receipt would print. They live in Bosnia and Herzegovina and type short notes in Serbian/Bosnian (Latin script, often without č, ć, š, ž, đ).

For every expense decide whether its description (or note) names the concrete things that were bought, e.g. "pica i krofna", "2 kafe i sok", "pita", "sendvic", "cvijece".
- If it does: one line per thing, with a quantity and a unit price, so that the lines add up to the expense's total exactly. "2 kafe" is quantity 2. Spread the total by what such things usually cost there; when the person bought the same product before, use its name exactly as listed and stay close to its last price. Otherwise name it the way a receipt would, short, in the person's language with proper letters ("Krofna", "Pica", "Sendvič", "Cvijeće"). Never invent brands, sizes or flavours that are not in the description.
- If it does not, answer "items": [] for it: a shop or place ("Pekara", "Konzum", "Restoran"), a vague word ("Nabavka za kuci", "hrana", "ručak", "sitnice"), money given to someone ("dao baki", "prebacio Mariji"), bills, rent, services, fuel, fees, subscriptions.

Answer only JSON: {"expenses":[{"id":<expense id>,"items":[{"name":"<product>","quantity":<number>,"unit_price":<number in the expense's currency>}],"note":"<under 10 words, English>"}]}.`

var externalIDNote = regexp.MustCompile(`\[external_id:[^\]]*\]`)

// guessRun is one run's tally, kept for the jobs page.
type guessRun struct {
	Trigger  string `json:"trigger"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Checked  int    `json:"checked"`
	Itemized int    `json:"itemized"`
	None     int    `json:"none"`
	Failed   int    `json:"failed"`
	Error    string `json:"error,omitempty"`
}

var guessMu sync.Mutex // one run at a time: the daily one, "Run now" and the editor's button

type guessLine struct {
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// guessBasis is the amount and currency the person typed.
func guessBasis(t *store.Transaction) (int64, string) {
	if t.ForeignAmount != 0 && t.ForeignCurrency != "" {
		return abs64(t.ForeignAmount), t.ForeignCurrency
	}
	return t.Amount, t.Currency
}

// guessItems asks the AI about these transactions (the person's own typed
// expenses) and stores the items it names. Returns what happened per id.
func (s *Server) guessItems(ctx context.Context, ids []int64, trigger string) (*guessRun, map[int64]store.ItemGuess, error) {
	guessMu.Lock()
	defer guessMu.Unlock()
	uid := actorID(ctx)
	client, detail := s.receiptAI(ctx)
	if client == nil {
		return nil, nil, fmt.Errorf("AI is not available: %s", detail)
	}
	run := &guessRun{Trigger: trigger, Start: store.Now()}
	result := map[int64]store.ItemGuess{}
	var txs []*store.Transaction
	for _, id := range ids {
		t, err := s.store.GetTransaction(ctx, id)
		if err != nil || t.UserID != uid || t.Kind != "expense" || strings.TrimSpace(t.Description) == "" {
			continue
		}
		if t.ReceiptID != nil && !t.ReceiptAI {
			continue // a scanned receipt has the real items
		}
		if trigger != "manual" {
			if g, err := s.store.GetItemGuess(ctx, t.ID); err == nil && g.Asked(t) {
				continue // answered already (a save right after the first one)
			}
		}
		txs = append(txs, t)
	}
	run.Checked = len(txs)
	known, _ := s.store.KnownProducts(ctx, uid, 150)
	var kb strings.Builder
	if len(known) > 0 {
		kb.WriteString("Products the person bought before (name, last unit price, times bought):\n")
		for _, k := range known {
			fmt.Fprintf(&kb, "- %s", k.Name)
			if k.Price > 0 {
				fmt.Fprintf(&kb, " · %s %s", money(k.Price), k.Currency)
			}
			fmt.Fprintf(&kb, " · ×%d\n", k.Bought)
		}
	}
	var productIDs []int64
	for start := 0; start < len(txs); start += 20 {
		batch := txs[start:min(start+20, len(txs))]
		byID := map[int64]*store.Transaction{}
		var list strings.Builder
		for _, t := range batch {
			byID[t.ID] = t
			total, cur := guessBasis(t)
			when := t.Date
			if d, err := time.Parse("2006-01-02", t.Date); err == nil {
				when = d.Format("Mon 2006-01-02")
			}
			fmt.Fprintf(&list, "#%d %s %s · total %s %s · description %q", t.ID, when, firstNonBlank(t.Time, "--:--"), money(total), cur, t.Description)
			if t.Payee != "" {
				fmt.Fprintf(&list, " · payee %q", t.Payee)
			}
			if t.CategoryName != "" {
				fmt.Fprintf(&list, " · category %q", t.CategoryName)
			}
			if n := clip(strings.Join(strings.Fields(externalIDNote.ReplaceAllString(t.Notes, "")), " "), 160); n != "" {
				fmt.Fprintf(&list, " · note %q", n)
			}
			list.WriteString("\n")
		}
		var out struct {
			Expenses []struct {
				ID    FlexID      `json:"id"`
				Items []guessLine `json:"items"`
				Note  string      `json:"note"`
			} `json:"expenses"`
		}
		actx, cancel := context.WithTimeout(ctx, 90*time.Second)
		usage, err := client.JSON(actx, itemGuessPrompt, "The expenses:\n"+list.String()+"\n"+kb.String(), &out)
		cancel()
		s.store.LogAI(ctx, uid, "item-guess", usage.Model, usage.PromptTokens, usage.CompletionTokens)
		if err != nil {
			run.Error = err.Error()
			for _, t := range batch {
				g := store.ItemGuess{TransactionID: t.ID, UserID: uid, Amount: t.Amount, Description: t.Description, Status: "error", Note: clip(err.Error(), 160)}
				_ = s.store.SaveItemGuess(ctx, g)
				result[t.ID] = g
				run.Failed++
			}
			continue
		}
		answered := map[int64]bool{}
		for _, a := range out.Expenses {
			t := byID[int64(a.ID)]
			if t == nil || answered[t.ID] {
				continue
			}
			answered[t.ID] = true
			g := store.ItemGuess{TransactionID: t.ID, UserID: uid, Amount: t.Amount, Description: t.Description, Status: "none", Note: clip(strings.TrimSpace(a.Note), 160)}
			total, cur := guessBasis(t)
			if items := receiptLines(a.Items, total); len(items) > 0 {
				rec, err := s.store.SaveGuessedItems(ctx, t, cur, total, items)
				if err != nil {
					s.log.Warn("item guess: save", "tx", t.ID, "err", err)
					g.Status, g.Note = "error", clip(err.Error(), 160)
					run.Failed++
				} else {
					g.Status = "items"
					run.Itemized++
					for _, it := range rec.Items {
						if it.ProductID != nil && (it.GroupID == nil || it.CategoryID == nil) {
							productIDs = append(productIDs, *it.ProductID)
						}
					}
				}
			} else {
				_ = s.store.DeleteGuessedItems(ctx, t.ID) // an earlier guess for other words
				run.None++
			}
			_ = s.store.SaveItemGuess(ctx, g)
			result[t.ID] = g
		}
		for _, t := range batch {
			if !answered[t.ID] {
				g := store.ItemGuess{TransactionID: t.ID, UserID: uid, Amount: t.Amount, Description: t.Description, Status: "none", Note: "no answer"}
				_ = s.store.SaveItemGuess(ctx, g)
				result[t.ID] = g
				run.None++
			}
		}
	}
	s.placeGuessedProducts(ctx, productIDs)
	run.End = store.Now()
	if b, err := json.Marshal(run); err == nil {
		_ = s.store.CachePut(ctx, fmt.Sprintf("itemguess:last:%d", uid), string(b))
	}
	return run, result, nil
}

// placeGuessedProducts gives new products a group and a category the way a
// scan does (grouping on scan, AI sorting), when those are on.
func (s *Server) placeGuessedProducts(ctx context.Context, ids []int64) {
	if len(ids) == 0 {
		return
	}
	rs := s.store.GetReceiptSettings(ctx, actorID(ctx))
	if rs.GroupOnScan {
		mode := "keywords"
		if rs.GroupJob == "ai" {
			mode = "ai"
		}
		if _, err := s.runProductCheckMode(ctx, "scan", ids, mode); err != nil {
			s.log.Warn("item guess: grouping", "err", err)
		}
	}
	if rs.AIItems {
		if _, _, err := s.sortProducts(ctx, ids, 200); err != nil {
			s.log.Warn("item guess: AI sorting", "err", err)
		}
	}
}

// receiptLines turns the AI's lines into receipt items that add up to total
// exactly: the AI's prices only set the proportions, the rounding remainder
// goes to the biggest line.
func receiptLines(lines []guessLine, total int64) []fiscal.Item {
	type part struct {
		name string
		q    float64
		raw  float64
	}
	var parts []part
	sum := 0.0
	for _, l := range lines {
		name := ownCase(clip(strings.Join(strings.Fields(l.Name), " "), 60))
		if name == "" {
			continue
		}
		q := l.Quantity
		if q <= 0 || q > 1000 || math.IsNaN(q) {
			q = 1
		}
		q = math.Round(q*1000) / 1000
		raw := q * l.UnitPrice
		if raw < 0 || math.IsNaN(raw) || math.IsInf(raw, 0) {
			raw = 0
		}
		parts = append(parts, part{name, q, raw})
		sum += raw
		if len(parts) == 20 {
			break
		}
	}
	if len(parts) == 0 || total <= 0 {
		return nil
	}
	if sum <= 0 { // no prices: by quantity
		for i := range parts {
			parts[i].raw = parts[i].q
			sum += parts[i].q
		}
	}
	items := make([]fiscal.Item, 0, len(parts))
	var got int64
	for _, p := range parts {
		t := int64(math.Round(p.raw / sum * float64(total)))
		if t <= 0 {
			continue
		}
		items = append(items, fiscal.Item{Name: p.name, Quantity: p.q, Total: t})
		got += t
	}
	if len(items) == 0 {
		return nil
	}
	big := 0
	for i := range items {
		if items[i].Total > items[big].Total {
			big = i
		}
	}
	items[big].Total += total - got
	if items[big].Total <= 0 {
		return nil
	}
	for i := range items {
		items[i].UnitPrice = int64(math.Round(float64(items[i].Total) / items[i].Quantity))
	}
	return items
}

// guessWindow is how far back the retry looks.
const guessWindow = 3

// onEntrySources are the typed sources asked about the moment they are
// saved; Firefly rows only come in by import, which must not set off
// hundreds of AI calls.
var onEntrySources = map[string]bool{"": true, "manual": true, "web": true, "quick": true, "template": true, "api": true, "firefly_api": true}

// guessOnEntry asks about a typed expense that was just created or changed,
// in the background, when the person has it on and an AI is ready.
func (s *Server) guessOnEntry(ctx context.Context, t *store.Transaction) {
	if t == nil || t.Kind != "expense" || !onEntrySources[t.Source] || strings.TrimSpace(t.Description) == "" || t.Amount <= 0 {
		return
	}
	if t.ReceiptID != nil && !t.ReceiptAI || t.UserID != actorID(ctx) {
		return
	}
	if g, err := s.store.GetItemGuess(ctx, t.ID); err == nil && g.Asked(t) {
		return
	}
	if !s.store.GetReceiptSettings(ctx, t.UserID).GuessItems {
		return
	}
	if client, _ := s.receiptAI(ctx); client == nil {
		return // the hourly retry asks once an AI is there
	}
	bg := context.WithoutCancel(ctx)
	s.receiptWork.Add(1)
	go func() {
		defer s.receiptWork.Done()
		if _, _, err := s.guessItems(bg, []int64{t.ID}, "entry"); err != nil {
			s.log.Warn("item guess", "tx", t.ID, "err", err)
		}
	}()
}

// retryItemGuesses runs at most hourly from the background round: typed
// expenses of the last days that were never asked about (no AI at the time)
// or whose answer failed.
func (s *Server) retryItemGuesses(ctx context.Context) {
	uid := actorID(ctx)
	if !s.store.GetReceiptSettings(ctx, uid).GuessItems {
		return
	}
	now := time.Now()
	key, hour := fmt.Sprintf("itemguess:retry:%d", uid), now.Format("2006-01-02T15")
	if last, _, _ := s.store.CacheGet(ctx, key); last == hour {
		return
	}
	if client, _ := s.receiptAI(ctx); client == nil {
		return
	}
	_ = s.store.CachePut(ctx, key, hour)
	ids, err := s.store.ItemGuessCandidates(ctx, uid, now.AddDate(0, 0, -guessWindow).Format("2006-01-02"), now.Format("2006-01-02"), 100)
	if err != nil || len(ids) == 0 {
		return
	}
	run, _, err := s.guessItems(ctx, ids, "retry")
	if err != nil {
		s.log.Warn("item guess retry", "user", uid, "err", err)
		return
	}
	s.log.Info("item guess retry", "user", uid, "checked", run.Checked, "itemized", run.Itemized, "none", run.None, "failed", run.Failed)
}

// lastGuessRun is the last run's tally, nil when there was none.
func (s *Server) lastGuessRun(ctx context.Context) *guessRun {
	v, _, ok := s.store.CacheGet(ctx, fmt.Sprintf("itemguess:last:%d", actorID(ctx)))
	if !ok || v == "" {
		return nil
	}
	var run guessRun
	if json.Unmarshal([]byte(v), &run) != nil {
		return nil
	}
	return &run
}

// guessPending counts typed expenses of the last days still to be asked about.
func (s *Server) guessPending(ctx context.Context, days int) int {
	now := time.Now()
	ids, _ := s.store.ItemGuessCandidates(ctx, actorID(ctx), now.AddDate(0, 0, -days).Format("2006-01-02"), now.Format("2006-01-02"), 2000)
	return len(ids)
}

// ---- handlers ----

// handleGuessTxItems asks the AI about one transaction now, even when it
// was asked before (a new try). Answers the transaction and its items.
func (s *Server) handleGuessTxItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := s.loadTx(w, r, true)
	if t == nil {
		return
	}
	switch {
	case t.UserID != actorID(ctx):
		writeErr(w, http.StatusForbidden, "only the person who booked it can do this")
		return
	case t.Kind != "expense":
		writeErr(w, http.StatusBadRequest, "only expenses get items")
		return
	case t.ReceiptID != nil && !t.ReceiptAI:
		writeErr(w, http.StatusConflict, "it has a scanned receipt with the real items")
		return
	case strings.TrimSpace(t.Description) == "":
		writeErr(w, http.StatusBadRequest, "it needs a description to guess from")
		return
	}
	_, result, err := s.guessItems(ctx, []int64{t.ID}, "manual")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g := result[t.ID]
	t, _ = s.store.GetTransaction(ctx, t.ID)
	out := map[string]any{"transaction": t, "status": g.Status, "note": g.Note}
	if g.Status == "items" {
		if rec, err := s.store.ReceiptForTransaction(ctx, t.ID); err == nil {
			out["receipt"] = rec
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRunItemGuess asks about the typed expenses of the last days (1–90)
// not asked about yet, in the background.
func (s *Server) handleRunItemGuess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Days int `json:"days"`
	}
	_ = decode(r, &in)
	if in.Days <= 0 {
		in.Days = guessWindow
	}
	if in.Days > 90 {
		in.Days = 90
	}
	if client, detail := s.receiptAI(ctx); client == nil {
		writeErr(w, http.StatusBadRequest, "AI is not available: "+detail)
		return
	}
	now := time.Now()
	ids, err := s.store.ItemGuessCandidates(ctx, actorID(ctx), now.AddDate(0, 0, -in.Days).Format("2006-01-02"), now.Format("2006-01-02"), 2000)
	if err != nil {
		s.fail(w, err, "item guess")
		return
	}
	if len(ids) > 0 {
		bg := context.WithoutCancel(ctx)
		s.receiptWork.Add(1)
		go func() {
			defer s.receiptWork.Done()
			if _, _, err := s.guessItems(bg, ids, "manual"); err != nil {
				s.log.Warn("item guess by hand", "err", err)
			}
		}()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": len(ids) > 0, "expenses": len(ids)})
}
