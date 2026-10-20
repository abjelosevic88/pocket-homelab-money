package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-money/internal/config"
	"github.com/abjelosevic88/pocket-homelab-money/internal/store"
)

// fakeIdP is a tiny OpenID provider: it signs whoever `email` says.
type fakeIdP struct {
	*httptest.Server
	key, forger *rsa.PrivateKey
	email       string
	verified    bool
	forge       bool
	nonces      map[string]string // code → nonce
	challenges  map[string]string // code → PKCE challenge
}

func newFakeIdP(t *testing.T) *fakeIdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &fakeIdP{key: k, forger: f, verified: true, nonces: map[string]string{}, challenges: map[string]string{}}
	mux := http.NewServeMux()
	p.Server = httptest.NewServer(mux)
	b64 := base64.RawURLEncoding.EncodeToString
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token", "jwks_uri": p.URL + "/jwks"})
	})
	mux.HandleFunc("/api/v1/auth/subject", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "iss": p.URL})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "kid": "k1", "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "money" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad request", 400)
			return
		}
		code := store.RandomToken(8)
		p.nonces[code], p.challenges[code] = q.Get("nonce"), q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		code := r.FormValue("code")
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if id != "money" || secret != "shh" || p.challenges[code] != b64(sum[:]) {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
		body, _ := json.Marshal(map[string]any{"iss": p.URL, "sub": strings.ToLower(p.email), "auth_revision": int64(1), "aud": "money", "exp": time.Now().Add(time.Hour).Unix(), "nonce": p.nonces[code], "email": p.email, "email_verified": p.verified, "name": "Aleksandar"})
		signing := b64(head) + "." + b64(body)
		h := sha256.Sum256([]byte(signing))
		signer := p.key
		if p.forge {
			signer = p.forger
		}
		sig, _ := rsa.SignPKCS1v15(rand.Reader, signer, crypto.SHA256, h[:])
		json.NewEncoder(w).Encode(map[string]string{"id_token": signing + "." + b64(sig), "access_token": "at", "token_type": "Bearer"})
	})
	t.Cleanup(p.Close)
	return p
}

func TestOIDCSignIn(t *testing.T) {
	idp := newFakeIdP(t)
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, DBPath: dir + "/t.db", FilesDir: dir + "/files", AuthMode: "local", DefaultCurrency: "BAM", FXProvider: "none", MaxUploadBytes: 1 << 20, NotifyHour: 9, UserAgent: "test",
		SessionTTL: time.Hour, OIDCIssuer: idp.URL, OIDCClientID: "money", OIDCClientSecret: "shh", OIDCName: "PocketID", PasswordLogin: false}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewUnstartedServer(nil)
	cfg.BaseURL = "http://" + ts.Listener.Addr().String()
	s := New(cfg, st, log, nil)
	if err := s.core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Router()
	ts.Start()
	t.Cleanup(ts.Close)
	ctx := context.Background()
	_ = st.CreateUser(ctx, &store.User{Email: "aleksandar@example.com", Name: "A", Role: "admin", PasswordHash: "x"})

	// signIn runs the browser dance and returns where it ended up plus the client.
	signIn := func(next string) (*url.URL, *http.Client) {
		jar, _ := cookiejar.New(nil)
		var last *url.URL
		cl := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			last = req.URL
			if strings.HasPrefix(req.URL.String(), ts.URL) && !strings.HasPrefix(req.URL.Path, "/api/") {
				return http.ErrUseLastResponse // back in the app
			}
			return nil
		}}
		res, err := cl.Get(ts.URL + "/api/v1/auth/oidc/start?next=" + url.QueryEscape(next))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return last, cl
	}
	me := func(cl *http.Client) int {
		res, err := cl.Get(ts.URL + "/api/v1/auth/me")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	idp.email = "Aleksandar@Example.com"
	end, cl := signIn("/budgets")
	if end == nil || end.Path != "/budgets" || me(cl) != 200 {
		t.Fatalf("happy path ended at %v, me=%d", end, me(cl))
	}

	// passwords are off; the status tells the login page
	res, _ := http.Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"email":"aleksandar@example.com","password":"x"}`))
	if res.StatusCode != 403 {
		t.Errorf("password login with PASSWORD_LOGIN=false: %d", res.StatusCode)
	}
	var status map[string]any
	res, _ = http.Get(ts.URL + "/api/v1/auth/status")
	json.NewDecoder(res.Body).Decode(&status)
	if status["oidc"] != true || status["password_login"] != false || status["oidc_name"] != "PocketID" {
		t.Errorf("status %v", status)
	}

	for _, c := range []struct {
		name, email string
		verified    bool
		forge       bool
		next, want  string
	}{
		{"unknown email", "stranger@example.com", true, false, "/", "No Pocket Money account"},
		{"unverified email", "aleksandar@example.com", false, false, "/", "verified email"},
		{"forged signature", "aleksandar@example.com", true, true, "/", "did not check out"},
	} {
		idp.email, idp.verified, idp.forge = c.email, c.verified, c.forge
		end, cl := signIn(c.next)
		if end == nil || end.Path != "/login" || !strings.Contains(end.Query().Get("error"), c.want) || me(cl) != 401 {
			t.Errorf("%s: ended at %v", c.name, end)
		}
	}

	// the return path cannot leave the site
	idp.email, idp.verified, idp.forge = "aleksandar@example.com", true, false
	if end, _ := signIn("//evil.example/x"); end == nil || end.Host != strings.TrimPrefix(ts.URL, "http://") || end.Path != "/" {
		t.Errorf("open redirect: %v", end)
	}
	// a callback without the browser's cookie is refused
	res, _ = (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(ts.URL + "/api/v1/auth/oidc/callback?code=x&state=y")
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Errorf("cookieless callback → %q", loc)
	}
}
