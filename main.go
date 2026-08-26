package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

const (
	maxBody       = 8 << 10
	sessionMaxAge = 8 * time.Hour
	kdfRounds     = 200_000
)

type config struct {
	listen, authFile, bootstrapFile, proxySecretFile, tokenFile, cloudflared, service, logFile string
	siteName, hostname                                                                         string
	demo, demoBootstrap, demoAjenti                                                            bool
}

type session struct {
	csrf    string
	expires time.Time
}

type server struct {
	cfg      config
	tpl      *template.Template
	mu       sync.Mutex
	sessions map[string]session
	attempts map[string][]time.Time
	demoUp   bool
	started  time.Time
}

type statusResponse struct {
	State                  string   `json:"state"`
	StateLabel             string   `json:"stateLabel"`
	SiteName               string   `json:"siteName"`
	Hostname               string   `json:"hostname"`
	Version                string   `json:"version"`
	Architecture           string   `json:"architecture"`
	Uptime                 string   `json:"uptime"`
	LastCheck              string   `json:"lastCheck"`
	Connections            int      `json:"connections"`
	TokenPresent           bool     `json:"tokenPresent"`
	OriginOK               bool     `json:"originOK"`
	Diagnostics            []string `json:"diagnostics"`
	ServiceDetail          string   `json:"serviceDetail"`
	PasswordChangeRequired bool     `json:"passwordChangeRequired"`
	ExternalAuth           bool     `json:"externalAuth"`
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:9080", "listen address")
	flag.StringVar(&cfg.authFile, "auth-file", "/opt/frigotehnica/config/admin.auth", "admin password hash file")
	flag.StringVar(&cfg.bootstrapFile, "bootstrap-file", "/opt/frigotehnica/config/bootstrap.required", "one-time password marker file")
	flag.StringVar(&cfg.proxySecretFile, "proxy-secret-file", "", "trusted Ajenti proxy secret file")
	flag.StringVar(&cfg.tokenFile, "token-file", "/opt/frigotehnica/config/tunnel.token", "Cloudflare token file")
	flag.StringVar(&cfg.cloudflared, "cloudflared", "/opt/frigotehnica/cloudflared", "cloudflared binary")
	flag.StringVar(&cfg.service, "service", "cloudflared-frigotehnica", "OpenRC service")
	flag.StringVar(&cfg.logFile, "log-file", "/opt/frigotehnica/logs/cloudflared.log", "safe diagnostics source")
	flag.StringVar(&cfg.siteName, "site-name", "BOSS Test", "displayed site name")
	flag.StringVar(&cfg.hostname, "hostname", "boss-test.frigotehnica.dpdns.org", "displayed public hostname")
	flag.BoolVar(&cfg.demo, "demo", false, "local visual QA mode")
	flag.BoolVar(&cfg.demoBootstrap, "demo-bootstrap", false, "show forced password setup in demo mode")
	flag.BoolVar(&cfg.demoAjenti, "demo-ajenti", false, "simulate Ajenti authentication in demo mode")
	flag.Parse()

	if flag.NArg() == 1 && flag.Arg(0) == "generate-password" {
		password, err := randomHex(12)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(password)
		return
	}

	if flag.NArg() == 1 && flag.Arg(0) == "hash-password" {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err != nil {
			log.Fatal(err)
		}
		password = []byte(strings.TrimSpace(string(password)))
		if len(password) < 12 {
			log.Fatal("password must be at least 12 characters")
		}
		encoded, err := makePasswordHash(password)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(encoded)
		return
	}

	tpl := template.Must(template.ParseFS(webFS, "web/index.html", "web/login.html"))
	s := &server{cfg: cfg, tpl: tpl, sessions: map[string]session{}, attempts: map[string][]time.Time{}, demoUp: true, started: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.requireAuth(s.logout))
	mux.HandleFunc("GET /assets/app.css", s.asset("web/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /assets/app.js", s.asset("web/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /", s.requireAuth(s.index))
	mux.HandleFunc("GET /api/status", s.requireAuth(s.status))
	mux.HandleFunc("POST /api/check", s.requireAuth(s.requireCSRF(s.check)))
	mux.HandleFunc("POST /api/service/{action}", s.requireAuth(s.requireCSRF(s.serviceAction)))
	mux.HandleFunc("POST /api/token", s.requireAuth(s.requireCSRF(s.updateToken)))
	mux.HandleFunc("POST /api/password", s.requireAuth(s.requireCSRF(s.updatePassword)))

	h := s.securityHeaders(http.MaxBytesHandler(mux, maxBody))
	httpServer := &http.Server{Addr: cfg.listen, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("Frigotehnica Tunnel Control listening on %s", cfg.listen)
	log.Fatal(httpServer.ListenAndServe())
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if s.cfg.proxySecretFile != "" || s.cfg.demoAjenti {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
		} else {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) asset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentSession(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = s.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": r.URL.Query().Get("error") != "", "Bootstrap": s.passwordChangeRequired()})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.allowAttempt(ip) {
		http.Error(w, "Too many attempts. Please wait.", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	valid := s.cfg.demo && subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte("DemoPassword1234")) == 1
	var err error
	if !s.cfg.demo {
		valid, err = verifyPasswordFile(s.cfg.authFile, []byte(r.FormValue("password")))
	}
	if err != nil {
		log.Printf("authentication unavailable: %v", err)
	}
	if !valid {
		http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
		return
	}
	id, _ := randomHex(32)
	csrf, _ := randomHex(24)
	s.mu.Lock()
	s.sessions[id] = session{csrf: csrf, expires: time.Now().Add(sessionMaxAge)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "ft_session", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionMaxAge.Seconds())})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("ft_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "ft_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) allowAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	kept := s.attempts[ip][:0]
	for _, t := range s.attempts[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 8 {
		s.attempts[ip] = kept
		return false
	}
	s.attempts[ip] = append(kept, time.Now())
	return true
}

func (s *server) currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie("ft_session")
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[c.Value]
	if !ok || time.Now().After(sess.expires) {
		delete(s.sessions, c.Value)
		return session{}, false
	}
	return sess, true
}

func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.trustedProxyRequest(r) {
			next(w, r)
			return
		}
		if _, ok := s.currentSession(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		next(w, r)
	}
}

func (s *server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.trustedProxyRequest(r) {
			next(w, r)
			return
		}
		sess, ok := s.currentSession(r)
		if !ok || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.csrf)) != 1 || !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *server) trustedProxyRequest(r *http.Request) bool {
	if s.cfg.demo && s.cfg.demoAjenti {
		return isLoopbackRemote(r.RemoteAddr)
	}
	if s.cfg.proxySecretFile == "" || !isLoopbackRemote(r.RemoteAddr) {
		return false
	}
	want, err := os.ReadFile(s.cfg.proxySecretFile)
	if err != nil {
		return false
	}
	want = []byte(strings.TrimSpace(string(want)))
	got := []byte(r.Header.Get("X-Frigotehnica-Ajenti"))
	return len(want) >= 32 && len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func isLoopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host) && (u.Scheme == "http" || u.Scheme == "https")
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.currentSession(r)
	externalAuth := s.trustedProxyRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	_ = s.tpl.ExecuteTemplate(w, "index.html", map[string]any{"CSRF": sess.csrf, "SiteName": s.cfg.siteName, "Hostname": s.cfg.hostname, "ExternalAuth": externalAuth})
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.collectStatus(r.Context(), s.trustedProxyRequest(r)))
}

func (s *server) check(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.collectStatus(r.Context(), s.trustedProxyRequest(r)))
}

func (s *server) collectStatus(ctx context.Context, externalAuth bool) statusResponse {
	state := "disconnected"
	label := "Disconnected"
	detail := "OpenRC service is not running"
	if s.cfg.demo {
		if s.demoUp {
			state, label, detail = "connected", "Connected", "OpenRC service is running"
		}
	} else {
		c, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if err := exec.CommandContext(c, "rc-service", s.cfg.service, "status").Run(); err == nil {
			state, label, detail = "connected", "Connected", "OpenRC service is running"
		}
	}
	version := "unknown"
	if s.cfg.demo {
		version = "2026.8.2"
	} else if out, err := exec.CommandContext(ctx, s.cfg.cloudflared, "--version").Output(); err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "version" && i+1 < len(fields) {
				version = fields[i+1]
				break
			}
		}
	}
	connections, diagnostics := safeDiagnostics(s.cfg.logFile, s.cfg.demo)
	tokenPresent := filePresent(s.cfg.tokenFile)
	if !tokenPresent {
		state, label, detail = "disconnected", "Not configured", "Waiting for Cloudflare tunnel token"
		connections = 0
		diagnostics = []string{"Configure the Cloudflare tunnel token to start the service."}
	}
	originOK := probeOrigin()
	return statusResponse{State: state, StateLabel: label, SiteName: s.cfg.siteName, Hostname: s.cfg.hostname, Version: version, Architecture: runtime.GOARCH, Uptime: humanDuration(time.Since(s.started)), LastCheck: time.Now().Format("15:04:05"), Connections: connections, TokenPresent: tokenPresent, OriginOK: originOK, Diagnostics: diagnostics, ServiceDetail: detail, PasswordChangeRequired: !externalAuth && s.passwordChangeRequired(), ExternalAuth: externalAuth}
}

func filePresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (s *server) passwordChangeRequired() bool {
	return s.cfg.demoBootstrap || fileExists(s.cfg.bootstrapFile)
}

func probeOrigin() bool {
	c := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get("http://127.0.0.1/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func (s *server) serviceAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "start" && action != "restart" && action != "stop" {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if !s.trustedProxyRequest(r) && s.passwordChangeRequired() {
		http.Error(w, "Change the one-time administrator password first", http.StatusPreconditionRequired)
		return
	}
	if action != "stop" && !filePresent(s.cfg.tokenFile) && !s.cfg.demo {
		http.Error(w, "Configure the Cloudflare tunnel token first", http.StatusPreconditionRequired)
		return
	}
	if action == "stop" {
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Confirm {
			http.Error(w, "confirmation required", http.StatusBadRequest)
			return
		}
	}
	if s.cfg.demo {
		s.demoUp = action != "stop"
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "rc-service", s.cfg.service, action).Run(); err != nil {
		http.Error(w, "The operation was not successful", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) updateToken(w http.ResponseWriter, r *http.Request) {
	if !s.trustedProxyRequest(r) && s.passwordChangeRequired() {
		http.Error(w, "Change the one-time administrator password first", http.StatusPreconditionRequired)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(body.Token)
	if len(token) < 80 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\t ") {
		http.Error(w, "Invalid token", http.StatusBadRequest)
		return
	}
	dir := filepath.Dir(s.cfg.tokenFile)
	tmp, err := os.CreateTemp(dir, ".tunnel-token-*")
	if err != nil {
		http.Error(w, "The token could not be saved", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(token + "\n")
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, s.cfg.tokenFile)
	}
	if err != nil {
		http.Error(w, "The token could not be saved", http.StatusInternalServerError)
		return
	}
	if s.cfg.demo {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	action := "restart"
	if err := exec.CommandContext(ctx, "rc-service", s.cfg.service, "status").Run(); err != nil {
		action = "start"
	}
	if err := exec.CommandContext(ctx, "rc-service", s.cfg.service, action).Run(); err != nil {
		http.Error(w, "The token was saved, but the restart failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *server) updatePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 256 || body.Password != body.Confirm || body.Password == body.Current {
		http.Error(w, "The new password must match, differ from the current password, and contain at least 12 characters", http.StatusBadRequest)
		return
	}
	valid := s.cfg.demo && subtle.ConstantTimeCompare([]byte(body.Current), []byte("DemoPassword1234")) == 1
	var err error
	if !s.cfg.demo {
		valid, err = verifyPasswordFile(s.cfg.authFile, []byte(body.Current))
	}
	if err != nil || !valid {
		http.Error(w, "The current password is incorrect", http.StatusForbidden)
		return
	}
	encoded, err := makePasswordHash([]byte(body.Password))
	if err != nil || (!s.cfg.demo && writeSecretFile(s.cfg.authFile, encoded+"\n") != nil) {
		http.Error(w, "The password could not be saved", http.StatusInternalServerError)
		return
	}
	if !s.cfg.demo {
		if err := os.Remove(s.cfg.bootstrapFile); err != nil && !os.IsNotExist(err) {
			http.Error(w, "The password was saved, but setup could not be completed", http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	s.sessions = map[string]session{}
	s.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func writeSecretFile(path, value string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(value)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	return err
}

var tokenLike = regexp.MustCompile(`(?i)(token[=: ]+)[^ ]+|eyJ[A-Za-z0-9_.-]{40,}`)
var connectionIndex = regexp.MustCompile(`connIndex[^0-9]{1,6}([0-9]+)`)

func safeDiagnostics(path string, demo bool) (int, []string) {
	if demo {
		return 4, []string{"Connection check completed successfully.", "Connected to Cloudflare.", "The tunnel route is active.", "The local origin is responding."}
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, []string{"No diagnostic messages are available yet."}
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 128<<10)
	connections := map[string]bool{}
	lines := make([]string, 0, 12)
	for s.Scan() {
		line := tokenLike.ReplaceAllString(s.Text(), "$1[REDACTED]")
		if strings.Contains(line, "Registered tunnel connection") {
			if m := connectionIndex.FindStringSubmatch(line); len(m) == 2 {
				connections[m[1]] = true
			}
			lines = append(lines, "A secure Cloudflare connection was registered.")
		} else if strings.Contains(line, "ERR") {
			lines = append(lines, "Cloudflared reported an error. Check the system log over local SSH.")
		}
		if len(lines) > 10 {
			lines = lines[len(lines)-10:]
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "Cloudflared is running; there are no new safe messages.")
	}
	return len(connections), lines
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + " sec"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + " min"
	}
	return fmt.Sprintf("%d hr %d min", int(d.Hours()), int(d.Minutes())%60)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func makePasswordHash(password []byte) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := derive(password, salt)
	return fmt.Sprintf("v1:%d:%x:%x", kdfRounds, salt, h), nil
}

func verifyPasswordFile(path string, password []byte) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	p := strings.Split(strings.TrimSpace(string(b)), ":")
	if len(p) != 4 || p[0] != "v1" {
		return false, errors.New("unsupported auth file")
	}
	rounds, err := strconv.Atoi(p[1])
	if err != nil || rounds != kdfRounds {
		return false, errors.New("invalid auth rounds")
	}
	salt, err := hex.DecodeString(p[2])
	if err != nil {
		return false, err
	}
	want, err := hex.DecodeString(p[3])
	if err != nil {
		return false, err
	}
	got := derive(password, salt)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1, nil
}

func derive(password, salt []byte) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write(password)
	sum := h.Sum(nil)
	for i := 1; i < kdfRounds; i++ {
		h.Reset()
		h.Write(sum)
		h.Write(salt)
		h.Write(password)
		sum = h.Sum(nil)
	}
	return sum
}

