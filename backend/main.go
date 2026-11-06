package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	panelweb "deploy-panel/backend/web"
	"github.com/coder/websocket"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	_ "modernc.org/sqlite"
)

type Config struct {
	Env, Addr, PublicURL, DataDir, MasterKey, DockerNetwork, TraefikDir string
	BuildTimeout, HealthTimeout                                         time.Duration
	RetainImages                                                        int
	MaxLogBytes                                                         int64
}
type App struct {
	db           *sql.DB
	cfg          Config
	key          []byte
	secureCookie bool
	mux          *http.ServeMux
}
type Project struct {
	ID               string  `json:"id"`
	VPSID            string  `json:"vps_id"`
	ProjectPath      string  `json:"project_path"`
	Name             string  `json:"name"`
	RepoURL          string  `json:"repo_url"`
	Branch           string  `json:"branch"`
	Dockerfile       string  `json:"dockerfile_path"`
	BuildContext     string  `json:"build_context"`
	Domain           string  `json:"domain"`
	ContainerPort    int     `json:"container_port"`
	HealthPath       string  `json:"health_path"`
	HealthTimeout    int     `json:"health_timeout_seconds"`
	Memory           int     `json:"runtime_memory_mb"`
	CPU              float64 `json:"runtime_cpu_limit"`
	Active           *string `json:"active_deployment_id"`
	ContainerStatus  string  `json:"container_status"`
	DeploymentStatus string  `json:"deployment_status"`
	Created          string  `json:"created_at"`
}
type VPS struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Fingerprint string `json:"host_fingerprint"`
	Status      string `json:"status"`
	Created     string `json:"created_at"`
}
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var keyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	envFile := flag.String("env-file", "", "read KEY=VALUE configuration from this file (OS environment takes precedence)")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: panel [--env-file FILE] <serve|keygen|admin create|admin seed|doctor>")
		os.Exit(2)
	}
	switch args[0] {
	case "keygen":
		b := make([]byte, 32)
		_, err := rand.Read(b)
		must(err)
		fmt.Println(base64.RawStdEncoding.EncodeToString(b))
		return
	case "admin":
		if len(args) < 2 || (args[1] != "create" && args[1] != "seed") {
			fatal("usage: panel admin create|seed")
		}
		must(loadEnv(*envFile))
		c := readConfig()
		db := openDB(c)
		defer db.Close()
		if err := migrate(db); err != nil {
			fatal(err.Error())
		}
		if args[1] == "create" {
			must(createAdmin(db))
		} else {
			must(seedAdmin(db, os.Getenv("PANEL_ADMIN_USERNAME"), os.Getenv("PANEL_ADMIN_PASSWORD")))
			fmt.Println("Admin seeded. Remove PANEL_ADMIN_PASSWORD from the env file now.")
		}
		return
	case "serve":
		must(loadEnv(*envFile))
		c := readConfig()
		a, err := newApp(c)
		if err != nil {
			fatal(err.Error())
		}
		defer a.db.Close()
		a.serve()
	case "doctor":
		must(loadEnv(*envFile))
		c := readConfig()
		doctor(c)
	default:
		fatal("unknown command: " + args[0])
	}
}
func must(e error) {
	if e != nil {
		fatal(e.Error())
	}
}
func fatal(s string) { fmt.Fprintln(os.Stderr, "panel:", s); os.Exit(1) }
func loadEnv(file string) error {
	if file == "" {
		return nil
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	sc := bufioScanner(f)
	for _, line := range sc {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.SplitN(line, "=", 2)
		if len(p) != 2 {
			return fmt.Errorf("invalid env-file line")
		}
		k, v := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		v = strings.Trim(v, "\"'")
		if _, ok := os.LookupEnv(k); !ok {
			if e = os.Setenv(k, v); e != nil {
				return e
			}
		}
	}
	return nil
}
func bufioScanner(r io.Reader) []string {
	b, e := io.ReadAll(r)
	if e != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func readConfig() Config {
	bt, _ := time.ParseDuration(env("PANEL_BUILD_TIMEOUT", "20m"))
	ht, _ := time.ParseDuration(env("PANEL_HEALTH_TIMEOUT", "60s"))
	ri, _ := strconv.Atoi(env("PANEL_RETAIN_IMAGES", "5"))
	lb, _ := strconv.ParseInt(env("PANEL_MAX_LOG_BYTES", "10485760"), 10, 64)
	return Config{Env: env("PANEL_ENV", "development"), Addr: env("PANEL_ADDR", "127.0.0.1:8080"), PublicURL: env("PANEL_PUBLIC_URL", "http://localhost:5173"), DataDir: env("PANEL_DATA_DIR", "./data"), MasterKey: os.Getenv("PANEL_MASTER_KEY"), DockerNetwork: env("PANEL_DOCKER_NETWORK", "panel_apps"), TraefikDir: env("PANEL_TRAEFIK_CONFIG_DIR", filepath.Join(env("PANEL_DATA_DIR", "./data"), "traefik", "dynamic")), BuildTimeout: bt, HealthTimeout: ht, RetainImages: ri, MaxLogBytes: lb}
}
func openDB(c Config) *sql.DB {
	if e := os.MkdirAll(c.DataDir, 0700); e != nil {
		fatal(e.Error())
	}
	d, e := sql.Open("sqlite", filepath.Join(c.DataDir, "panel.db"))
	if e != nil {
		fatal(e.Error())
	}
	d.SetMaxOpenConns(1)
	return d
}
func newApp(c Config) (*App, error) {
	if c.Env == "production" && c.MasterKey == "" {
		return nil, errors.New("PANEL_MASTER_KEY is required in production")
	}
	k, e := base64.RawStdEncoding.DecodeString(c.MasterKey)
	if e != nil && c.MasterKey != "" {
		k, e = hex.DecodeString(c.MasterKey)
	}
	if e != nil {
		return nil, errors.New("PANEL_MASTER_KEY must be base64 or hex encoded")
	}
	if c.MasterKey != "" && len(k) != 32 {
		return nil, errors.New("PANEL_MASTER_KEY must contain exactly 32 bytes")
	}
	if len(k) == 0 {
		k, e = loadOrCreateDevelopmentKey(c.DataDir)
		if e != nil {
			return nil, e
		}
		slog.Warn("using local development encryption key from data directory; keep it with the local database")
	}
	d := openDB(c)
	if e = migrate(d); e != nil {
		return nil, e
	}
	a := &App{db: d, cfg: c, key: k, secureCookie: c.Env == "production", mux: http.NewServeMux()}
	a.routes()
	return a, nil
}
func loadOrCreateDevelopmentKey(dataDir string) ([]byte, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "development.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, errors.New("development key file must contain exactly 32 bytes")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		key, err = os.ReadFile(path)
		if err == nil && len(key) != 32 {
			err = errors.New("development key file must contain exactly 32 bytes")
		}
		return key, err
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return key, nil
}
func migrate(d *sql.DB) error {
	_, e := d.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS vps(id TEXT PRIMARY KEY,name TEXT NOT NULL,host TEXT NOT NULL,port INTEGER NOT NULL,username TEXT NOT NULL,host_fingerprint TEXT NOT NULL,password_encrypted BLOB NOT NULL,password_nonce BLOB NOT NULL,status TEXT NOT NULL DEFAULT 'connected',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS admins(id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash BLOB NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, token_hash TEXT UNIQUE NOT NULL, admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE, csrf_hash TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,repo_url TEXT NOT NULL,branch TEXT NOT NULL,dockerfile_path TEXT NOT NULL,build_context TEXT NOT NULL,domain TEXT UNIQUE NOT NULL,container_port INTEGER NOT NULL,health_path TEXT NOT NULL,health_timeout_seconds INTEGER NOT NULL,runtime_memory_mb INTEGER NOT NULL,runtime_cpu_limit REAL NOT NULL,active_deployment_id TEXT,container_status TEXT NOT NULL DEFAULT 'missing',deployment_status TEXT NOT NULL DEFAULT 'never',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,project_path TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS project_env(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,key TEXT NOT NULL,encrypted_value BLOB NOT NULL,nonce BLOB NOT NULL,key_version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL,UNIQUE(project_id,key));
CREATE TABLE IF NOT EXISTS project_volumes(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,docker_volume_name TEXT NOT NULL,mount_target TEXT NOT NULL,UNIQUE(project_id,mount_target));
CREATE TABLE IF NOT EXISTS deployments(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),kind TEXT NOT NULL,source_deployment_id TEXT,status TEXT NOT NULL,requested_ref TEXT NOT NULL,commit_sha TEXT,image_tag TEXT,image_id TEXT,container_id TEXT,previous_deployment_id TEXT,config_snapshot_encrypted BLOB,log_path TEXT,error_code TEXT,error_message TEXT,created_at TEXT NOT NULL,started_at TEXT,finished_at TEXT);
CREATE INDEX IF NOT EXISTS deployments_project_created ON deployments(project_id,created_at DESC);
CREATE TABLE IF NOT EXISTS queue(id INTEGER PRIMARY KEY AUTOINCREMENT,deployment_id TEXT UNIQUE NOT NULL REFERENCES deployments(id),status TEXT NOT NULL DEFAULT 'queued',created_at TEXT NOT NULL);
INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(1,datetime('now'));`)
	if e != nil {
		return e
	}
	rows, e := d.Query("PRAGMA table_info(projects)")
	if e != nil {
		return e
	}
	hasVPS := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var def any
		if rows.Scan(&cid, &name, &typ, &notnull, &def, &pk) == nil && name == "vps_id" {
			hasVPS = true
		}
	}
	rows.Close()
	if !hasVPS {
		_, e = d.Exec("ALTER TABLE projects ADD COLUMN vps_id TEXT REFERENCES vps(id)")
	}
	if e != nil {
		return e
	}
	rows, e = d.Query("PRAGMA table_info(projects)")
	if e != nil {
		return e
	}
	hasPath := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var def any
		if rows.Scan(&cid, &name, &typ, &notnull, &def, &pk) == nil && name == "project_path" {
			hasPath = true
		}
	}
	rows.Close()
	if !hasPath {
		_, e = d.Exec("ALTER TABLE projects ADD COLUMN project_path TEXT NOT NULL DEFAULT ''")
	}
	return e
}
func createAdmin(db *sql.DB) error {
	var n int
	if e := db.QueryRow("SELECT count(*) FROM admins").Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return errors.New("admin already exists")
	}
	var username, password string
	fmt.Print("Username: ")
	if _, e := fmt.Scanln(&username); e != nil {
		return e
	}
	fmt.Print("Password: ")
	pass, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if e != nil {
		return errors.New("password must be entered from an interactive terminal")
	}
	password = string(pass)
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	id := randomID()
	hash := argon2.IDKey([]byte(password), []byte(id), 2, 64*1024, 2, 32)
	_, e = db.Exec("INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,?,?,?)", id, username, hash, now())
	return e
}
func seedAdmin(db *sql.DB, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 {
		return errors.New("PANEL_ADMIN_USERNAME must contain 1 to 64 characters")
	}
	if len(password) < 12 {
		return errors.New("PANEL_ADMIN_PASSWORD must be at least 12 characters")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM admins").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("admin already exists; refusing to replace it")
	}
	id := randomID()
	passwordHash := argon2.IDKey([]byte(password), []byte(id), 2, 64*1024, 2, 32)
	_, err := db.Exec("INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,?,?,?)", id, username, passwordHash, now())
	return err
}
func randomID() string { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func now() string      { return time.Now().UTC().Format(time.RFC3339Nano) }
func (a *App) routes() {
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	a.mux.HandleFunc("POST /api/auth/login", a.login)
	a.mux.HandleFunc("POST /api/auth/logout", a.auth(a.logout))
	a.mux.HandleFunc("GET /api/auth/me", a.auth(a.me))
	a.mux.HandleFunc("GET /api/projects", a.auth(a.projects))
	a.mux.HandleFunc("GET /api/vps", a.auth(a.vps))
	a.mux.HandleFunc("POST /api/vps", a.auth(a.vps))
	a.mux.HandleFunc("POST /api/vps/{id}/test", a.auth(a.testVPS))
	a.mux.HandleFunc("POST /api/projects", a.auth(a.projects))
	a.mux.HandleFunc("GET /api/projects/{id}", a.auth(a.project))
	a.mux.HandleFunc("PATCH /api/projects/{id}", a.auth(a.project))
	a.mux.HandleFunc("DELETE /api/projects/{id}", a.auth(a.project))
	a.mux.HandleFunc("GET /api/projects/{id}/env", a.auth(a.envs))
	a.mux.HandleFunc("PUT /api/projects/{id}/env", a.auth(a.envs))
	a.mux.HandleFunc("DELETE /api/projects/{id}/env/{key}", a.auth(a.envs))
	a.mux.HandleFunc("GET /api/projects/{id}/volumes", a.auth(a.volumes))
	a.mux.HandleFunc("PUT /api/projects/{id}/volumes", a.auth(a.volumes))
	a.mux.HandleFunc("GET /api/projects/{id}/deployments", a.auth(a.deployments))
	a.mux.HandleFunc("POST /api/projects/{id}/deployments", a.auth(a.deployments))
	a.mux.HandleFunc("GET /api/projects/{id}/terminal", a.auth(a.terminal))
	a.mux.HandleFunc("GET /api/deployments/{id}", a.auth(a.deployment))
	a.mux.HandleFunc("GET /api/deployments/{id}/logs", a.auth(a.deployment))
	a.mux.HandleFunc("/", a.static)
}
func (a *App) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, 404, "not_found", "Endpoint tidak ditemukan")
		return
	}
	path := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if path == "." || path == "" {
		path = "index.html"
	}
	f, err := panelweb.Files.Open("dist/" + path)
	if err == nil {
		f.Close()
		http.FileServer(http.FS(mustSub(panelweb.Files, "dist"))).ServeHTTP(w, r)
		return
	}
	if !strings.Contains(filepath.Base(path), ".") {
		http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(readIndex()))
		return
	}
	http.NotFound(w, r)
}
func mustSub(f fs.FS, dir string) fs.FS { sub, _ := fs.Sub(f, dir); return sub }
func readIndex() string                 { b, _ := panelweb.Files.ReadFile("dist/index.html"); return string(b) }
func (a *App) serve() {
	srv := &http.Server{Addr: a.cfg.Addr, Handler: a.mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	slog.Info("panel listening", "addr", a.cfg.Addr)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		fatal(e.Error())
	}
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, msg string) {
	var x apiError
	x.Error.Code = code
	x.Error.Message = msg
	write(w, status, x)
}
func body(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func hash(s string) string        { x := sha256.Sum256([]byte(s)); return hex.EncodeToString(x[:]) }
func csrfFor(token string) string { return hash("deploy-panel-csrf:" + token) }
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var q struct{ Username, Password string }
	if body(r, &q) != nil {
		fail(w, 400, "invalid_request", "Permintaan tidak valid")
		return
	}
	var id string
	var p []byte
	e := a.db.QueryRow("SELECT id,password_hash FROM admins WHERE username=?", q.Username).Scan(&id, &p)
	if e != nil || len(p) != 32 || !equal(p, argon2.IDKey([]byte(q.Password), []byte(id), 2, 64*1024, 2, 32)) {
		fail(w, 401, "invalid_credentials", "Username atau kata sandi salah")
		return
	}
	token := randomID() + randomID()
	csrf := csrfFor(token)
	_, e = a.db.Exec("INSERT INTO sessions(id,token_hash,admin_id,csrf_hash,expires_at,created_at) VALUES(?,?,?,?,?,?)", randomID(), hash(token), id, hash(csrf), time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), now())
	if e != nil {
		fail(w, 500, "session_error", "Sesi tidak dapat dibuat")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "panel_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour)})
	write(w, 200, map[string]any{"admin": map[string]string{"username": q.Username}, "csrf_token": csrf})
}
func equal(a, b []byte) bool { return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1 }
func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("panel_session")
		if e != nil {
			fail(w, 401, "unauthorized", "Silakan masuk")
			return
		}
		var id, csrf, exp string
		e = a.db.QueryRow("SELECT id,csrf_hash,expires_at FROM sessions WHERE token_hash=?", hash(c.Value)).Scan(&id, &csrf, &exp)
		expires, parseErr := time.Parse(time.RFC3339, exp)
		if e != nil || parseErr != nil || !time.Now().UTC().Before(expires) {
			fail(w, 401, "unauthorized", "Sesi tidak berlaku")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			u, _ := url.Parse(a.cfg.PublicURL)
			if origin == "" || u == nil || origin != u.Scheme+"://"+u.Host {
				fail(w, 403, "origin_denied", "Origin tidak diizinkan")
				return
			}
			if hash(r.Header.Get("X-CSRF-Token")) != csrf {
				fail(w, 403, "csrf_failed", "Token CSRF tidak berlaku")
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey("admin"), id))
		next(w, r)
	}
}

type ctxKey string

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	id := r.Context().Value(ctxKey("admin")).(string)
	var username string
	_ = a.db.QueryRow("SELECT username FROM admins WHERE id=?", id).Scan(&username)
	c, _ := r.Cookie("panel_session")
	csrf := csrfFor(c.Value)
	write(w, 200, map[string]any{"admin": map[string]string{"username": username}, "csrf_token": csrf})
}
func cookieHash(r *http.Request) string {
	c, e := r.Cookie("panel_session")
	if e != nil {
		return ""
	}
	return hash(c.Value)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	_, _ = a.db.Exec("DELETE FROM sessions WHERE token_hash=?", cookieHash(r))
	http.SetCookie(w, &http.Cookie{Name: "panel_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	write(w, 200, map[string]string{"status": "ok"})
}
func validRepo(s string) bool {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return false
	}
	ips, e := net.LookupIP(host)
	if e == nil {
		for _, ip := range ips {
			if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				return false
			}
		}
	}
	return true
}
func validDomain(s string) bool {
	return len(s) < 254 && regexp.MustCompile(`^(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`).MatchString(s)
}
func validHost(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	return len(s) < 254 && regexp.MustCompile(`^(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`).MatchString(s)
}
func safeVPSProjectPath(s string) bool {
	if len(s) == 0 || len(s) > 512 || strings.Contains(s, "//") || strings.Contains(s, "\\") || !regexp.MustCompile(`^/?[A-Za-z0-9._/-]+$`).MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}
func sshLogin(host string, port int, username, password, fingerprint string) (string, error) {
	client, seenFingerprint, err := sshConnect(host, port, username, password, fingerprint)
	if err != nil {
		return "", err
	}
	return seenFingerprint, client.Close()
}
func sshConnect(host string, port int, username, password, fingerprint string) (*ssh.Client, string, error) {
	if !validHost(host) || port < 1 || port > 65535 || username == "" || len(username) > 128 || password == "" || len(password) > 4096 || (fingerprint != "" && !regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{20,}={0,2}$`).MatchString(fingerprint)) {
		return nil, "", errors.New("invalid SSH connection details")
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	seenFingerprint := ""
	config := &ssh.ClientConfig{User: username, Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		seenFingerprint = ssh.FingerprintSHA256(key)
		if fingerprint != "" && seenFingerprint != fingerprint {
			return errors.New("SSH host fingerprint mismatch")
		}
		return nil
	}, Timeout: 8 * time.Second}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(clientConn, chans, reqs), seenFingerprint, nil
}
func (a *App) checkVPSDirectory(vpsID, projectPath string) error {
	var host, username, fingerprint string
	var port int
	var ciphertext, nonce []byte
	if err := a.db.QueryRow("SELECT host,port,username,host_fingerprint,password_encrypted,password_nonce FROM vps WHERE id=?", vpsID).Scan(&host, &port, &username, &fingerprint, &ciphertext, &nonce); err != nil {
		return err
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	password, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	client, _, err := sshConnect(host, port, username, string(password), fingerprint)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Run(remoteProjectPrefix(projectPath) + "pwd")
}
func remoteProjectPrefix(projectPath string) string {
	quotedPath := "'" + strings.ReplaceAll(projectPath, "'", "'\\''") + "'"
	if strings.HasPrefix(projectPath, "/") {
		return "cd " + quotedPath + " || exit; "
	}
	return "case \"$PWD/\" in *'/" + projectPath + "/') : ;; *) cd " + quotedPath + " || exit ;; esac; "
}
func (a *App) terminal(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != a.cfg.PublicURL {
		fail(w, 403, "origin_denied", "Origin tidak diizinkan")
		return
	}
	csrfToken := ""
	for _, proto := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		proto = strings.TrimSpace(proto)
		if strings.HasPrefix(proto, "csrf.") {
			csrfToken = strings.TrimPrefix(proto, "csrf.")
		}
	}
	cookie, err := r.Cookie("panel_session")
	if err != nil || csrfToken == "" {
		fail(w, 403, "csrf_failed", "Token CSRF tidak berlaku")
		return
	}
	var csrfHash string
	if err = a.db.QueryRow("SELECT csrf_hash FROM sessions WHERE token_hash=?", hash(cookie.Value)).Scan(&csrfHash); err != nil || hash(csrfToken) != csrfHash {
		fail(w, 403, "csrf_failed", "Token CSRF tidak berlaku")
		return
	}
	var host, username, fingerprint, projectPath string
	var port int
	var ciphertext, nonce []byte
	err = a.db.QueryRow(`SELECT v.host,v.port,v.username,v.host_fingerprint,v.password_encrypted,v.password_nonce,p.project_path FROM projects p JOIN vps v ON v.id=p.vps_id WHERE p.id=? AND p.deleted_at IS NULL`, r.PathValue("id")).Scan(&host, &port, &username, &fingerprint, &ciphertext, &nonce, &projectPath)
	if err != nil || projectPath == "" {
		fail(w, 404, "not_found", "Folder project VPS tidak ditemukan")
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"iploy-terminal"}})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "terminal closed")
	ws.SetReadLimit(64 * 1024)
	ctx := r.Context()
	setupError := func(message string) {
		_ = ws.Write(ctx, websocket.MessageText, []byte("\r\n[terminal] "+message+"\r\n"))
		_ = ws.Close(websocket.StatusInternalError, "terminal setup failed")
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		setupError("Kredensial VPS tidak dapat dibuka.")
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		setupError("Kredensial VPS tidak dapat dibuka.")
		return
	}
	password, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		setupError("Kredensial VPS tidak dapat dibuka; periksa master key panel.")
		return
	}
	client, _, err := sshConnect(host, port, username, string(password), fingerprint)
	if err != nil {
		setupError("Login SSH gagal. Periksa jaringan, kredensial, atau fingerprint host VPS.")
		return
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		setupError("Shell SSH tidak dapat dibuka oleh server.")
		return
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		setupError("Input terminal tidak tersedia.")
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		setupError("Output terminal tidak tersedia.")
		return
	}
	if err = session.RequestPty("xterm", 24, 100, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); err != nil {
		setupError("VPS tidak mengizinkan terminal interaktif (PTY).")
		return
	}
	_ = session.Setenv("TERM", "xterm")
	if err = session.Start(remoteProjectPrefix(projectPath) + "exec bash -i"); err != nil {
		setupError("Shell SSH tidak dapat dijalankan.")
		return
	}
	var writeMu sync.Mutex
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				writeMu.Lock()
				writeErr := ws.Write(ctx, websocket.MessageText, buf[:n])
				writeMu.Unlock()
				if writeErr != nil {
					return
				}
			}
			if readErr != nil {
				_ = ws.Close(websocket.StatusNormalClosure, "shell ended")
				return
			}
		}
	}()
	for {
		typ, msg, readErr := ws.Read(ctx)
		if readErr != nil {
			return
		}
		if typ != websocket.MessageText && typ != websocket.MessageBinary {
			continue
		}
		if strings.HasPrefix(string(msg), "\x00resize:") {
			parts := strings.Split(strings.TrimPrefix(string(msg), "\x00resize:"), ":")
			if len(parts) == 2 {
				cols, colErr := strconv.Atoi(parts[0])
				rows, rowErr := strconv.Atoi(parts[1])
				if colErr == nil && rowErr == nil && cols >= 10 && cols <= 500 && rows >= 5 && rows <= 300 {
					_ = session.WindowChange(rows, cols)
				}
			}
			continue
		}
		if _, err = stdin.Write(msg); err != nil {
			return
		}
	}
}
func (a *App) vps(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, err := a.db.Query("SELECT id,name,host,port,username,host_fingerprint,status,created_at FROM vps ORDER BY created_at DESC")
		if err != nil {
			fail(w, 500, "database_error", "Gagal membaca daftar VPS")
			return
		}
		defer rows.Close()
		items := []VPS{}
		for rows.Next() {
			var item VPS
			if rows.Scan(&item.ID, &item.Name, &item.Host, &item.Port, &item.Username, &item.Fingerprint, &item.Status, &item.Created) == nil {
				items = append(items, item)
			}
		}
		write(w, 200, items)
		return
	}
	var input struct {
		Name        string `json:"name"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		Fingerprint string `json:"host_fingerprint"`
	}
	if body(r, &input) != nil {
		fail(w, 400, "validation_error", "Data VPS tidak valid")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.Fingerprint = strings.TrimSpace(input.Fingerprint)
	if input.Port == 0 {
		input.Port = 22
	}
	if input.Name == "" || len(input.Name) > 100 || input.Password == "" {
		fail(w, 400, "validation_error", "Isi nama VPS dan password SSH")
		return
	}
	seenFingerprint, err := sshLogin(input.Host, input.Port, input.Username, input.Password, input.Fingerprint)
	if err != nil {
		fail(w, 400, "ssh_connection_failed", "Login SSH gagal. Periksa host, port, username, password, dan host fingerprint.")
		return
	}
	input.Fingerprint = seenFingerprint
	ct, nonce, err := a.seal([]byte(input.Password))
	if err != nil {
		fail(w, 500, "encryption_error", "Kredensial VPS tidak dapat diamankan")
		return
	}
	item := VPS{ID: randomID(), Name: input.Name, Host: input.Host, Port: input.Port, Username: input.Username, Fingerprint: input.Fingerprint, Status: "connected", Created: now()}
	_, err = a.db.Exec("INSERT INTO vps(id,name,host,port,username,host_fingerprint,password_encrypted,password_nonce,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", item.ID, item.Name, item.Host, item.Port, item.Username, item.Fingerprint, ct, nonce, item.Status, item.Created, item.Created)
	if err != nil {
		fail(w, 409, "conflict", "VPS gagal disimpan")
		return
	}
	write(w, 201, item)
}
func (a *App) testVPS(w http.ResponseWriter, r *http.Request) {
	var host, username, fingerprint string
	var port int
	var ciphertext, nonce []byte
	err := a.db.QueryRow("SELECT host,port,username,host_fingerprint,password_encrypted,password_nonce FROM vps WHERE id=?", r.PathValue("id")).Scan(&host, &port, &username, &fingerprint, &ciphertext, &nonce)
	if err != nil {
		fail(w, 404, "not_found", "VPS tidak ditemukan")
		return
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		fail(w, 500, "encryption_error", "Kredensial VPS tidak dapat dibuka")
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		fail(w, 500, "encryption_error", "Kredensial VPS tidak dapat dibuka")
		return
	}
	password, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		fail(w, 500, "encryption_error", "Kredensial VPS tidak dapat dibuka")
		return
	}
	if _, err = sshLogin(host, port, username, string(password), fingerprint); err != nil {
		_, _ = a.db.Exec("UPDATE vps SET status='disconnected',updated_at=? WHERE id=?", now(), r.PathValue("id"))
		fail(w, 502, "ssh_connection_failed", "Koneksi SSH gagal. Periksa akses jaringan dan kredensial VPS.")
		return
	}
	_, _ = a.db.Exec("UPDATE vps SET status='connected',updated_at=? WHERE id=?", now(), r.PathValue("id"))
	write(w, 200, map[string]string{"status": "connected"})
}
func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, e := a.db.Query(`SELECT id,COALESCE(vps_id,''),project_path,name,repo_url,branch,dockerfile_path,build_context,domain,container_port,health_path,health_timeout_seconds,runtime_memory_mb,runtime_cpu_limit,active_deployment_id,container_status,deployment_status,created_at FROM projects WHERE deleted_at IS NULL ORDER BY created_at DESC`)
		if e != nil {
			fail(w, 500, "database_error", "Gagal membaca project")
			return
		}
		defer rows.Close()
		out := []Project{}
		for rows.Next() {
			var p Project
			if rows.Scan(&p.ID, &p.VPSID, &p.ProjectPath, &p.Name, &p.RepoURL, &p.Branch, &p.Dockerfile, &p.BuildContext, &p.Domain, &p.ContainerPort, &p.HealthPath, &p.HealthTimeout, &p.Memory, &p.CPU, &p.Active, &p.ContainerStatus, &p.DeploymentStatus, &p.Created) == nil {
				out = append(out, p)
			}
		}
		write(w, 200, out)
		return
	}
	var p Project
	if body(r, &p) != nil || strings.TrimSpace(p.Name) == "" || len(strings.TrimSpace(p.Name)) > 100 || p.VPSID == "" || !safeVPSProjectPath(p.ProjectPath) {
		fail(w, 400, "validation_error", "Isi nama project, pilih VPS, dan masukkan nama folder atau path project")
		return
	}
	var exists int
	if e := a.db.QueryRow("SELECT count(*) FROM vps WHERE id=?", p.VPSID).Scan(&exists); e != nil || exists == 0 {
		fail(w, 400, "validation_error", "Pilih VPS yang terdaftar")
		return
	}
	if e := a.checkVPSDirectory(p.VPSID, p.ProjectPath); e != nil {
		fail(w, 400, "remote_path_unavailable", "Login SSH berhasil, tetapi folder project tidak dapat diakses pada VPS")
		return
	}
	p.ID = randomID()
	p.Created = now()
	p.Domain = "linked-" + p.ID + ".invalid"
	p.ContainerPort = 0
	p.ContainerStatus = "connected"
	p.DeploymentStatus = "linked"
	_, e := a.db.Exec(`INSERT INTO projects(id,vps_id,project_path,name,repo_url,branch,dockerfile_path,build_context,domain,container_port,health_path,health_timeout_seconds,runtime_memory_mb,runtime_cpu_limit,container_status,deployment_status,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.VPSID, p.ProjectPath, p.Name, "", "", "", "", p.Domain, p.ContainerPort, "", 0, 0, 0, p.ContainerStatus, p.DeploymentStatus, p.Created, p.Created)
	if e != nil {
		fail(w, 409, "conflict", "Project gagal disimpan")
		return
	}
	write(w, 201, p)
}
func safeRelative(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.HasPrefix(path, "/") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean != ".." && !strings.HasPrefix(clean, "../")
}
func (a *App) project(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.Method {
	case "GET":
		var p Project
		e := a.db.QueryRow(`SELECT id,COALESCE(vps_id,''),project_path,name,repo_url,branch,dockerfile_path,build_context,domain,container_port,health_path,health_timeout_seconds,runtime_memory_mb,runtime_cpu_limit,active_deployment_id,container_status,deployment_status,created_at FROM projects WHERE id=? AND deleted_at IS NULL`, id).Scan(&p.ID, &p.VPSID, &p.ProjectPath, &p.Name, &p.RepoURL, &p.Branch, &p.Dockerfile, &p.BuildContext, &p.Domain, &p.ContainerPort, &p.HealthPath, &p.HealthTimeout, &p.Memory, &p.CPU, &p.Active, &p.ContainerStatus, &p.DeploymentStatus, &p.Created)
		if e != nil {
			fail(w, 404, "not_found", "Project tidak ditemukan")
			return
		}
		write(w, 200, p)
	case "PATCH":
		var p Project
		if body(r, &p) != nil {
			fail(w, 400, "invalid_request", "Permintaan tidak valid")
			return
		}
		if !validRepo(p.RepoURL) || !validDomain(p.Domain) || p.ContainerPort < 1 || p.ContainerPort > 65535 {
			fail(w, 400, "validation_error", "Konfigurasi tidak valid")
			return
		}
		_, e := a.db.Exec(`UPDATE projects SET name=?,repo_url=?,branch=?,dockerfile_path=?,build_context=?,domain=?,container_port=?,health_path=?,health_timeout_seconds=?,runtime_memory_mb=?,runtime_cpu_limit=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, p.Name, p.RepoURL, p.Branch, p.Dockerfile, p.BuildContext, p.Domain, p.ContainerPort, p.HealthPath, p.HealthTimeout, p.Memory, p.CPU, now(), id)
		if e != nil {
			fail(w, 409, "conflict", "Konfigurasi konflik")
			return
		}
		write(w, 200, map[string]string{"status": "updated"})
	case "DELETE":
		_, e := a.db.Exec("UPDATE projects SET deleted_at=?,updated_at=? WHERE id=? AND active_deployment_id IS NULL", now(), now(), id)
		if e != nil {
			fail(w, 409, "conflict", "Project memiliki resource aktif")
			return
		}
		write(w, 200, map[string]string{"status": "deleted"})
	}
}
func (a *App) seal(plain []byte) ([]byte, []byte, error) {
	b, e := aes.NewCipher(a.key)
	if e != nil {
		return nil, nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, nil, e
	}
	return g.Seal(nil, n, plain, nil), n, nil
}
func (a *App) envs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "GET" {
		rows, e := a.db.Query("SELECT key,updated_at FROM project_env WHERE project_id=? ORDER BY key", id)
		if e != nil {
			fail(w, 500, "database_error", "Gagal membaca environment")
			return
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var k, t string
			_ = rows.Scan(&k, &t)
			out = append(out, map[string]string{"key": k, "value": "••••••••", "updated_at": t})
		}
		write(w, 200, out)
		return
	}
	if r.Method == "DELETE" {
		_, e := a.db.Exec("DELETE FROM project_env WHERE project_id=? AND key=?", id, r.PathValue("key"))
		if e != nil {
			fail(w, 500, "database_error", "Gagal menghapus secret")
			return
		}
		write(w, 200, map[string]string{"status": "deleted"})
		return
	}
	var v map[string]string
	if body(r, &v) != nil {
		fail(w, 400, "invalid_request", "Env harus berupa object string")
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "database_error", "Database sibuk")
		return
	}
	defer tx.Rollback()
	for k, val := range v {
		if !keyRE.MatchString(k) {
			fail(w, 400, "validation_error", "Nama key environment tidak valid")
			return
		}
		ct, n, e := a.seal([]byte(val))
		if e != nil {
			fail(w, 500, "encryption_error", "Secret tidak dapat dienkripsi")
			return
		}
		_, e = tx.Exec(`INSERT INTO project_env(id,project_id,key,encrypted_value,nonce,key_version,updated_at) VALUES(?,?,?,?,?,1,?) ON CONFLICT(project_id,key) DO UPDATE SET encrypted_value=excluded.encrypted_value,nonce=excluded.nonce,key_version=1,updated_at=excluded.updated_at`, randomID(), id, k, ct, n, now())
		if e != nil {
			fail(w, 500, "database_error", "Secret tidak dapat disimpan")
			return
		}
	}
	if tx.Commit() != nil {
		fail(w, 500, "database_error", "Secret tidak dapat disimpan")
		return
	}
	write(w, 200, map[string]string{"status": "saved"})
}
func (a *App) volumes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "GET" {
		rows, e := a.db.Query("SELECT docker_volume_name,mount_target FROM project_volumes WHERE project_id=? ORDER BY mount_target", id)
		if e != nil {
			fail(w, 500, "database_error", "Gagal membaca volume")
			return
		}
		defer rows.Close()
		type V struct {
			Name   string `json:"docker_volume_name"`
			Target string `json:"mount_target"`
		}
		out := []V{}
		for rows.Next() {
			var v V
			_ = rows.Scan(&v.Name, &v.Target)
			out = append(out, v)
		}
		write(w, 200, out)
		return
	}
	var vs []struct {
		Name   string `json:"docker_volume_name"`
		Target string `json:"mount_target"`
	}
	if body(r, &vs) != nil {
		fail(w, 400, "invalid_request", "Format volume tidak valid")
		return
	}
	for _, v := range vs {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`).MatchString(v.Name) || !filepath.IsAbs(v.Target) || v.Target == "/" || strings.Contains(v.Target, "..") || strings.HasPrefix(v.Target, "/proc") || strings.HasPrefix(v.Target, "/sys") || strings.HasPrefix(v.Target, "/dev") {
			fail(w, 400, "validation_error", "Target mount tidak valid")
			return
		}
	}
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "database_error", "Database sibuk")
		return
	}
	defer tx.Rollback()
	_, _ = tx.Exec("DELETE FROM project_volumes WHERE project_id=?", id)
	for _, v := range vs {
		if _, e = tx.Exec("INSERT INTO project_volumes(id,project_id,docker_volume_name,mount_target) VALUES(?,?,?,?)", randomID(), id, v.Name, v.Target); e != nil {
			fail(w, 400, "validation_error", "Konfigurasi volume bentrok")
			return
		}
	}
	if tx.Commit() != nil {
		fail(w, 500, "database_error", "Volume tidak tersimpan")
		return
	}
	write(w, 200, map[string]string{"status": "saved"})
}
func (a *App) deployments(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		rows, e := a.db.Query("SELECT id,kind,status,requested_ref,commit_sha,created_at,started_at,finished_at,error_code,error_message FROM deployments WHERE project_id=? ORDER BY created_at DESC LIMIT 100", r.PathValue("id"))
		if e != nil {
			fail(w, 500, "database_error", "Gagal membaca deployment")
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, kind, status, ref, created string
			var sha, started, finished, code, msg sql.NullString
			_ = rows.Scan(&id, &kind, &status, &ref, &sha, &created, &started, &finished, &code, &msg)
			out = append(out, map[string]any{"id": id, "kind": kind, "status": status, "requested_ref": ref, "commit_sha": sha, "created_at": created, "started_at": started, "finished_at": finished, "error_code": code, "error_message": msg})
		}
		write(w, 200, out)
		return
	}
	var p Project
	var projectPath string
	e := a.db.QueryRow("SELECT id,branch,project_path FROM projects WHERE id=? AND deleted_at IS NULL", r.PathValue("id")).Scan(&p.ID, &p.Branch, &projectPath)
	if e != nil {
		fail(w, 404, "not_found", "Project tidak ditemukan")
		return
	}
	if projectPath != "" {
		fail(w, 409, "manual_project", "Project ini hanya ditautkan ke folder VPS; deployment panel belum tersedia untuknya")
		return
	}
	id := randomID()
	t := now()
	tx, e := a.db.Begin()
	if e != nil {
		fail(w, 500, "database_error", "Database sibuk")
		return
	}
	_, e = tx.Exec("INSERT INTO deployments(id,project_id,kind,status,requested_ref,created_at,log_path) VALUES(?,?, 'deploy','queued',?,?,?)", id, p.ID, p.Branch, t, filepath.Join(a.cfg.DataDir, "logs", id+".log"))
	if e == nil {
		_, e = tx.Exec("INSERT INTO queue(deployment_id,status,created_at) VALUES(?,'queued',?)", id, t)
	}
	if e == nil {
		_, e = tx.Exec("UPDATE projects SET deployment_status='queued',updated_at=? WHERE id=?", t, p.ID)
	}
	if e == nil {
		e = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if e != nil {
		fail(w, 500, "queue_error", "Deployment tidak dapat dimasukkan ke antrean")
		return
	}
	write(w, 202, map[string]string{"id": id, "status": "queued"})
}
func (a *App) deployment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p, kind, status, ref, created string
	var sha, started, finished, code, msg, logPath sql.NullString
	e := a.db.QueryRow("SELECT project_id,kind,status,requested_ref,commit_sha,created_at,started_at,finished_at,error_code,error_message,log_path FROM deployments WHERE id=?", id).Scan(&p, &kind, &status, &ref, &sha, &created, &started, &finished, &code, &msg, &logPath)
	if e != nil {
		fail(w, 404, "not_found", "Deployment tidak ditemukan")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/logs") {
		b, e := os.ReadFile(logPath.String)
		if e != nil && !os.IsNotExist(e) {
			fail(w, 500, "log_error", "Log tidak dapat dibaca")
			return
		}
		if len(b) > 100000 {
			b = b[len(b)-100000:]
		}
		write(w, 200, map[string]any{"text": string(b), "cursor": len(b)})
		return
	}
	write(w, 200, map[string]any{"id": id, "project_id": p, "kind": kind, "status": status, "requested_ref": ref, "commit_sha": sha, "created_at": created, "started_at": started, "finished_at": finished, "error_code": code, "error_message": msg})
}
func doctor(c Config) {
	fmt.Println("Deploy Panel doctor")
	fmt.Println("Go binary:", mustLook("git"), mustLook("docker"))
	d := openDB(c)
	defer d.Close()
	if e := migrate(d); e != nil {
		fatal("SQLite: " + e.Error())
	}
	fmt.Println("SQLite: OK")
	if c.Env == "production" && len(c.MasterKey) == 0 {
		fatal("PANEL_MASTER_KEY required in production")
	}
	fmt.Println("Config: OK (Docker engine availability is checked by deployment jobs)")
}
func mustLook(s string) string {
	p, e := exec.LookPath(s)
	if e != nil {
		return "missing"
	}
	return p
}
