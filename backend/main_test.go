package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func testApp(t *testing.T) *App {
	t.Helper()
	c := Config{Env: "test", Addr: "127.0.0.1:0", PublicURL: "http://panel.test", DataDir: t.TempDir()}
	c.MasterKey = ""
	a, err := newApp(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	return a
}

func TestAESGCMRoundTripAndTamper(t *testing.T) {
	a := testApp(t)
	plain := []byte("secret-value")
	ciphertext, nonce, err := a.seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(a.key)
	gcm, _ := cipher.NewGCM(block)
	got, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip got %q, err=%v", got, err)
	}
	ciphertext[0] ^= 1
	if _, err = gcm.Open(nil, nonce, ciphertext, nil); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestRepositoryAndPathValidation(t *testing.T) {
	for _, bad := range []string{"file:///tmp/repo", "https://user:pass@example.com/repo", "https://127.0.0.1/repo", "https://localhost/repo", "http://example.com/repo"} {
		if validRepo(bad) {
			t.Errorf("accepted unsafe repository %q", bad)
		}
	}
	for _, p := range []string{"../escape", "/absolute", `C:\\outside`, "a/../../b"} {
		if safeRelative(p) {
			t.Errorf("accepted unsafe relative path %q", p)
		}
	}
	if !safeRelative("Dockerfile") || !safeRelative(".") {
		t.Fatal("rejected ordinary project paths")
	}
	if validDomain("localhost") || validDomain("bad..example.com") || !validDomain("app.example.com") {
		t.Fatal("domain validation mismatch")
	}
}

func TestLoginSessionCSRFAndOrigin(t *testing.T) {
	a := testApp(t)
	id := randomID()
	password := "correct horse battery staple"
	ph := argon2.IDKey([]byte(password), []byte(id), 2, 64*1024, 2, 32)
	if _, err := a.db.Exec("INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,?,?,?)", id, "admin", ph, now()); err != nil {
		t.Fatal(err)
	}
	loginBody, _ := json.Marshal(map[string]string{"Username": "admin", "Password": password})
	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	a.mux.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var response struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &response); err != nil || response.CSRF == "" {
		t.Fatalf("missing CSRF: %v", err)
	}
	cookie := loginRec.Result().Cookies()[0]
	me := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	me.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	a.mux.ServeHTTP(meRec, me)
	if meRec.Code != http.StatusOK {
		t.Fatalf("session rejected: %d", meRec.Code)
	}
	var meData struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(meRec.Body.Bytes(), &meData)
	if meData.CSRF != response.CSRF {
		t.Fatal("GET /me did not return stable CSRF token")
	}
	post := func(origin, csrf string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewBufferString(`{}`))
		r.AddCookie(cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		a.mux.ServeHTTP(w, r)
		return w.Code
	}
	if got := post("", response.CSRF); got != http.StatusForbidden {
		t.Fatalf("missing Origin status=%d", got)
	}
	if got := post("http://panel.test", ""); got != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", got)
	}
	if got := post("http://panel.test", response.CSRF); got != http.StatusBadRequest {
		t.Fatalf("valid guard should reach validation, got=%d", got)
	}
}

func TestSessionExpiry(t *testing.T) {
	a := testApp(t)
	id := randomID()
	token := randomID()
	csrf := csrfFor(token)
	_, err := a.db.Exec("INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,?,?,?)", id, "a", make([]byte, 32), now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.db.Exec("INSERT INTO sessions(id,token_hash,admin_id,csrf_hash,expires_at,created_at) VALUES(?,?,?,?,?,?)", randomID(), hash(token), id, hash(csrf), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), now())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	w := httptest.NewRecorder()
	a.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status=%d", w.Code)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorUsesLowercaseContract(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, http.StatusBadRequest, "invalid_request", "bad input")
	var value map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["error"]["code"] != "invalid_request" || value["error"]["message"] != "bad input" {
		t.Fatalf("unexpected API error JSON: %s", w.Body.String())
	}
}

func TestAdminSeedCreatesAccountOnce(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = seedAdmin(db, "owner", "a-long-test-password"); err != nil {
		t.Fatal(err)
	}
	var id string
	var stored []byte
	if err = db.QueryRow("SELECT id,password_hash FROM admins WHERE username=?", "owner").Scan(&id, &stored); err != nil {
		t.Fatal(err)
	}
	if !equal(stored, argon2.IDKey([]byte("a-long-test-password"), []byte(id), 2, 64*1024, 2, 32)) {
		t.Fatal("seeded password hash does not verify")
	}
	if err = seedAdmin(db, "another", "another-long-test-password"); err == nil {
		t.Fatal("seeder must refuse a second admin")
	}
	if err = seedAdmin(db, "", "short"); err == nil {
		t.Fatal("seeder must reject missing/short credentials")
	}
}
