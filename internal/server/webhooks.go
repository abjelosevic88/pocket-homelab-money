package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha3"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

var webhookHTTP = &http.Client{Timeout: 10 * time.Second}

// firefly trigger names for the transaction events
var ffTriggers = map[string]string{"transaction.created": "STORE_TRANSACTION", "transaction.updated": "UPDATE_TRANSACTION", "transaction.deleted": "DESTROY_TRANSACTION"}

func wants(h *store.Webhook, event string) bool {
	if !h.Active {
		return false
	}
	for _, e := range h.Events {
		if e == event || e == "*" {
			return true
		}
	}
	return false
}

// emitTx tells every webhook whose owner can see the transaction.
func (s *Server) emitTx(ctx context.Context, event string, t *store.Transaction) {
	if t == nil {
		return
	}
	hooks, err := s.store.ListWebhooks(ctx, 0)
	if err != nil || len(hooks) == 0 {
		return
	}
	for i := range hooks {
		h := hooks[i]
		if !wants(&h, event) {
			continue
		}
		see := s.store.AccountRole(ctx, t.AccountID, h.OwnerID) != ""
		if !see && t.ToAccountID != nil {
			see = s.store.AccountRole(ctx, *t.ToAccountID, h.OwnerID) != ""
		}
		if !see {
			continue
		}
		var body any
		if h.Format == "firefly" {
			body = map[string]any{"uuid": store.RandomToken(16), "user_id": h.OwnerID, "trigger": ffTriggers[event], "response": "TRANSACTIONS",
				"url": h.URL, "version": "v0", "content": s.ffGroup(t)}
		} else {
			body = map[string]any{"id": store.RandomToken(12), "event": event, "created_at": store.Now(), "data": map[string]any{"transaction": t}}
		}
		s.dispatch(h, event, fmt.Sprintf("#%d %s", t.ID, t.Description), body)
	}
}

// emit sends a non-transaction event (budget.alert, subscription.due) to one owner's webhooks.
func (s *Server) emit(ctx context.Context, ownerID int64, event, subject string, data any) {
	hooks, err := s.store.ListWebhooks(ctx, ownerID)
	if err != nil {
		return
	}
	for i := range hooks {
		h := hooks[i]
		if !wants(&h, event) || h.Format == "firefly" {
			continue
		}
		s.dispatch(h, event, subject, map[string]any{"id": store.RandomToken(12), "event": event, "created_at": store.Now(), "data": data})
	}
}

// dispatch delivers in the background: 3 attempts (now, +5 s, +30 s).
func (s *Server) dispatch(h store.Webhook, event, subject string, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	go func() {
		ctx := context.Background()
		d := &store.Delivery{WebhookID: h.ID, Event: event, Subject: subject}
		start := time.Now()
		for i, wait := range []time.Duration{0, 5 * time.Second, 30 * time.Second} {
			time.Sleep(wait)
			d.Attempts = i + 1
			d.Status, d.Error = s.post(ctx, &h, event, raw)
			if d.Status >= 200 && d.Status < 300 {
				d.Error = ""
				break
			}
		}
		d.DurationMS = int(time.Since(start).Milliseconds())
		s.store.LogDelivery(ctx, d)
	}()
}

// post sends one request and returns the status (0 = no answer).
func (s *Server) post(ctx context.Context, h *store.Webhook, event string, raw []byte) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(raw))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PocketMoney/"+Version)
	req.Header.Set("X-Pocket-Event", event)
	if h.Secret != "" {
		m := hmac.New(sha256.New, []byte(h.Secret))
		m.Write(raw)
		req.Header.Set("X-Pocket-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
		if h.Format == "firefly" {
			// Firefly III's scheme: t=<unix>,v1=HMAC-SHA3-256(secret, "<unix>.<body>")
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			m3 := hmac.New(func() hashHash { return sha3.New256() }, []byte(h.Secret))
			m3.Write([]byte(ts + "."))
			m3.Write(raw)
			req.Header.Set("Signature", "t="+ts+",v1="+hex.EncodeToString(m3.Sum(nil)))
		}
	}
	res, err := webhookHTTP.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
	if res.StatusCode >= 300 {
		return res.StatusCode, strings.TrimSpace(string(b))
	}
	return res.StatusCode, ""
}

// ---- handlers ----

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.store.ListWebhooks(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "webhooks")
		return
	}
	for i := range hooks {
		hooks[i].Secret = mask(hooks[i].Secret)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": hooks, "events": store.WebhookEvents})
}

type webhookIn struct {
	Name   *string   `json:"name"`
	URL    *string   `json:"url"`
	Secret *string   `json:"secret"`
	Events *[]string `json:"events"`
	Format *string   `json:"format"`
	Active *bool     `json:"active"`
}

func (in *webhookIn) apply(h *store.Webhook) string {
	if in.Name != nil {
		h.Name = strings.TrimSpace(*in.Name)
	}
	if in.URL != nil {
		h.URL = strings.TrimSpace(*in.URL)
	}
	if in.Secret != nil && !strings.Contains(*in.Secret, "••") {
		h.Secret = strings.TrimSpace(*in.Secret)
	}
	if in.Events != nil {
		var ev []string
		for _, e := range *in.Events {
			if store.Valid(store.WebhookEvents, e) {
				ev = append(ev, e)
			}
		}
		h.Events = ev
	}
	if in.Format != nil {
		h.Format = *in.Format
	}
	if in.Active != nil {
		h.Active = *in.Active
	}
	if h.Format != "firefly" {
		h.Format = "native"
	}
	u, err := url.Parse(h.URL)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return "the URL must start with http:// or https://"
	case len(h.Events) == 0:
		return "pick at least one event"
	}
	return ""
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var in webhookIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	h := &store.Webhook{OwnerID: actorID(r.Context()), Active: true, Format: "native", Events: []string{"transaction.created", "transaction.updated", "transaction.deleted"}}
	if msg := in.apply(h); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if h.Secret == "" {
		h.Secret = store.RandomToken(20)
	}
	if err := s.store.CreateWebhook(r.Context(), h); err != nil {
		s.fail(w, err, "create webhook")
		return
	}
	writeJSON(w, http.StatusCreated, h) // the secret is shown in full once
}

func (s *Server) ownWebhook(w http.ResponseWriter, r *http.Request) *store.Webhook {
	h, err := s.store.GetWebhook(r.Context(), idParam(r, "id"))
	if err != nil || h.OwnerID != actorID(r.Context()) {
		writeErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return h
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	h := s.ownWebhook(w, r)
	if h == nil {
		return
	}
	var in webhookIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(h); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateWebhook(r.Context(), h); err != nil {
		s.fail(w, err, "update webhook")
		return
	}
	h.Secret = mask(h.Secret)
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	h := s.ownWebhook(w, r)
	if h == nil {
		return
	}
	if err := s.store.DeleteWebhook(r.Context(), h.ID); err != nil {
		s.fail(w, err, "delete webhook")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestWebhook sends a "ping" (or the newest transaction in Firefly format) right away.
func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := s.ownWebhook(w, r)
	if h == nil {
		return
	}
	var body any = map[string]any{"id": store.RandomToken(12), "event": "ping", "created_at": store.Now(), "data": map[string]any{"message": "Pocket Money webhook test"}}
	event := "ping"
	if h.Format == "firefly" {
		txs, _, _ := s.store.ListTransactions(ctx, store.TxFilter{AccountIDs: s.visibleIDs(ctx), Limit: 1})
		if len(txs) > 0 {
			event = "transaction.created"
			body = map[string]any{"uuid": store.RandomToken(16), "user_id": h.OwnerID, "trigger": "STORE_TRANSACTION", "response": "TRANSACTIONS", "url": h.URL, "version": "v0", "content": s.ffGroup(&txs[0])}
		}
	}
	raw, _ := json.Marshal(body)
	start := time.Now()
	status, msg := s.post(ctx, h, event, raw)
	d := &store.Delivery{WebhookID: h.ID, Event: "test", Subject: event, Status: status, Error: msg, Attempts: 1, DurationMS: int(time.Since(start).Milliseconds())}
	s.store.LogDelivery(ctx, d)
	writeJSON(w, http.StatusOK, map[string]any{"ok": status >= 200 && status < 300, "status": status, "error": msg})
}

func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	h := s.ownWebhook(w, r)
	if h == nil {
		return
	}
	out, err := s.store.ListDeliveries(r.Context(), h.ID, 50)
	if err != nil {
		s.fail(w, err, "deliveries")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type hashHash = hash.Hash
