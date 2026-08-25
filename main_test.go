package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
	token := strings.Repeat("a", 100)
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
