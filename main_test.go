package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testTunnelID = "123e4567-e89b-42d3-a456-426614174000"

func testTunnelToken() string {
	payload := `{"a":"account-tag","s":"MDEyMzQ1Njc4OWFiY2RlZg==","t":"` + testTunnelID + `"}`
	return base64.StdEncoding.EncodeToString([]byte(payload))
}

func installFakeRCService(t *testing.T, initiallyRunning bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake rc-service test requires a POSIX shell")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "running")
	logFile := filepath.Join(dir, "commands.log")
	if initiallyRunning {
		if err := os.WriteFile(state, []byte("running\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
echo "$2" >> "$RC_LOG"
case "$2" in
  status) test -f "$RC_STATE" ;;
  start) printf running > "$RC_STATE" ;;
  stop) rm -f "$RC_STATE" ;;
  *) exit 1 ;;
esac
`
	path := filepath.Join(dir, "rc-service")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RC_STATE", state)
	t.Setenv("RC_LOG", logFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func TestRunOpenRCRestartStopsStartsAndVerifies(t *testing.T) {
	logFile := installFakeRCService(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runOpenRCAction(ctx, "cloudflared-frigotehnica", "restart"); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(commands))
	joined := strings.Join(got, ",")
	if !strings.HasPrefix(joined, "status,stop,status,start,status") || strings.Count(joined, "status") < 3 {
		t.Fatalf("unexpected OpenRC sequence: %v", got)
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := makePasswordHash([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "admin.auth")
	if err := os.WriteFile(p, []byte(h+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ok, err := verifyPasswordFile(p, []byte("correct horse battery staple"))
	if err != nil || !ok {
		t.Fatalf("valid password rejected: %v", err)
	}
	ok, _ = verifyPasswordFile(p, []byte("wrong password"))
	if ok {
		t.Fatal("invalid password accepted")
	}
}

func TestTokenRedactionPattern(t *testing.T) {
	in := "error token=secret-value eyJabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := tokenLike.ReplaceAllString(in, "$1[REDACTED]")
	if strings.Contains(out, "secret-value") || strings.Contains(out, "eyJabc") {
		t.Fatalf("secret was not redacted: %s", out)
	}
}

func TestConnectionIndexFormats(t *testing.T) {
	for _, line := range []string{`connIndex=3 connection=abc`, `{"connIndex":2,"message":"Registered tunnel connection"}`} {
		m := connectionIndex.FindStringSubmatch(line)
		if len(m) != 2 {
			t.Fatalf("connection index not parsed from %q", line)
		}
	}
}

func TestDiagnosticsDiscardConnectionsFromPreviousTunnelSession(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "cloudflared.log")
	content := strings.Join([]string{
		`INF Registered tunnel connection connIndex=0`,
		`INF Registered tunnel connection connIndex=1`,
		`INF Starting tunnel tunnelID=old-session-is-over`,
		`ERR Failed to connect to edge`,
	}, "\n")
	if err := os.WriteFile(logFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	connections, _ := safeDiagnostics(logFile, false)
	if connections != 0 {
		t.Fatalf("stale connections were reported after a new session started: %d", connections)
	}
	if err := os.WriteFile(logFile, []byte(content+"\nINF Registered tunnel connection connIndex=0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	connections, _ = safeDiagnostics(logFile, false)
	if connections != 1 {
		t.Fatalf("current session connection was not reported: %d", connections)
	}
}

func TestEmptyBootstrapMarkerRequiresPasswordChange(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "bootstrap.required")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config{bootstrapFile: marker}}
	if !s.passwordChangeRequired() {
		t.Fatal("empty bootstrap marker was ignored")
	}
}

func TestUpdatePasswordRemovesBootstrapMarker(t *testing.T) {
	dir := t.TempDir()
	authFile := filepath.Join(dir, "admin.auth")
	marker := filepath.Join(dir, "bootstrap.required")
	hash, err := makePasswordHash([]byte("TemporaryPassword123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authFile, []byte(hash+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cfg:      config{authFile: authFile, bootstrapFile: marker},
		sessions: map[string]session{"old": {}},
	}
	body := bytes.NewBufferString(`{"current":"TemporaryPassword123","password":"PermanentPassword456","confirm":"PermanentPassword456"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/password", body)
	w := httptest.NewRecorder()
	s.updatePassword(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("password update failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("bootstrap marker was not removed")
	}
	ok, err := verifyPasswordFile(authFile, []byte("PermanentPassword456"))
	if err != nil || !ok {
		t.Fatalf("new password was not saved: %v", err)
	}
	if len(s.sessions) != 0 {
		t.Fatal("active sessions were not invalidated")
	}
}

func TestUpdateTokenCreatesProtectedFileInDemo(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "tunnel.token")
	s := &server{cfg: config{demo: true, tokenFile: tokenFile}}
	token := testTunnelToken()
	req := httptest.NewRequest(http.MethodPost, "/api/token", bytes.NewBufferString(`{"token":"`+token+`"}`))
	w := httptest.NewRecorder()
	s.updateToken(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("token update failed: %d %s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(b)) != token {
		t.Fatalf("token was not saved correctly: %v", err)
	}
	info, err := os.Stat(tokenFile)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
		t.Fatalf("token permissions are not 0600: %v %v", info.Mode().Perm(), err)
	}
}

func TestUpdateTokenPersistsThenRestartsOpenRC(t *testing.T) {
	logFile := installFakeRCService(t, true)
	tokenFile := filepath.Join(t.TempDir(), "tunnel.token")
	s := &server{cfg: config{tokenFile: tokenFile, service: "cloudflared-frigotehnica"}}
	token := testTunnelToken()
	req := httptest.NewRequest(http.MethodPost, "/api/token", bytes.NewBufferString(`{"token":"`+token+`"}`))
	w := httptest.NewRecorder()
	s.updateToken(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("token update failed: %d %s", w.Code, w.Body.String())
	}
	saved, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(saved)) != token {
		t.Fatalf("new token was not persisted: %v", err)
	}
	commands, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(commands))
	joined := strings.Join(got, ",")
	if !strings.HasPrefix(joined, "status,stop,status,start,status") || strings.Count(joined, "status") < 3 {
		t.Fatalf("token update did not perform a verified restart: %v", got)
	}
}

func TestParseTunnelTokenReturnsEmbeddedTunnelID(t *testing.T) {
	id, err := parseTunnelToken(testTunnelToken())
	if err != nil {
		t.Fatal(err)
	}
	if id != testTunnelID {
		t.Fatalf("unexpected tunnel ID: got %q want %q", id, testTunnelID)
	}
	if _, err := parseTunnelToken(strings.Repeat("a", 100)); err == nil {
		t.Fatal("malformed token was accepted")
	}
}

func TestTrustedAjentiProxyRequiresLoopbackAndMatchingSecret(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "ajenti-proxy.secret")
	secret := strings.Repeat("a", 64)
	if err := os.WriteFile(secretFile, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config{proxySecretFile: secretFile}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:43120"
	req.Header.Set("X-Frigotehnica-Ajenti", secret)
	if !s.trustedProxyRequest(req) {
		t.Fatal("valid loopback Ajenti proxy request was rejected")
	}
	req.RemoteAddr = "192.168.1.20:43120"
	if s.trustedProxyRequest(req) {
		t.Fatal("non-loopback proxy request was accepted")
	}
	req.RemoteAddr = "127.0.0.1:43120"
	req.Header.Set("X-Frigotehnica-Ajenti", strings.Repeat("b", 64))
	if s.trustedProxyRequest(req) {
		t.Fatal("proxy request with wrong secret was accepted")
	}
}

func TestAjentiAuthenticationBypassesStandaloneSession(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "ajenti-proxy.secret")
	secret := strings.Repeat("c", 64)
	if err := os.WriteFile(secretFile, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	s := &server{cfg: config{proxySecretFile: secretFile}, sessions: map[string]session{}}
	called := false
	h := s.requireAuth(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:41234"
	req.Header.Set("X-Frigotehnica-Ajenti", secret)
	h(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("Ajenti-authenticated request did not reach the handler")
	}
}
