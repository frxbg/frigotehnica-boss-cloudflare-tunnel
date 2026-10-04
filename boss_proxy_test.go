package main

import (
	"bytes"
	"crypto/sha256"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBossProxyUploadAndRemoteMode(t *testing.T) {
	payload := bytes.Repeat([]byte("test-upload\x00"), 100000)
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	file, err := writer.CreateFormFile("logo", "test.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(payload)
	_ = writer.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip != "127.0.0.2" {
			t.Errorf("BOSS sees %s, want 127.0.0.2", ip)
		}
		if r.Host != "boss.example.com" {
			t.Errorf("Host = %q", r.Host)
		}
		if r.URL.RequestURI() != "/boss/upload?kind=logo" {
			t.Errorf("URI = %s", r.URL)
		}
		for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Frigotehnica-Ajenti"} {
			if r.Header.Get(h) != "" {
				t.Errorf("forwarded untrusted header %s", h)
			}
		}
		if r.Header.Get("Cookie") != "JSESSIONID=test-session" {
			t.Error("session cookie lost")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		got, err := io.ReadAll(part)
		if err != nil || sha256.Sum256(got) != sha256.Sum256(payload) {
			t.Error("upload corrupted or truncated")
		}
		w.Header().Set("Location", "https://boss.example.com/boss/done")
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "new-session", Path: "/boss", Secure: true})
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer origin.Close()
	target, _ := url.Parse(origin.URL)
	transport := bossProxyTransport()
	defer transport.CloseIdleConnections()
	// Setting these must never divert the local BOSS traffic.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	r := httptest.NewRequest("POST", "http://boss.example.com/boss/upload?kind=logo", &form)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Cookie", "JSESSIONID=test-session")
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Frigotehnica-Ajenti"} {
		r.Header.Set(h, "untrusted")
	}
	w := httptest.NewRecorder()
	newBossProxy(target, transport).ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != "https://boss.example.com/boss/done" {
		t.Error("redirect lost")
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "JSESSIONID=new-session") {
		t.Error("response cookie lost")
	}
}

func TestBossProxyRejectsLocalHostsAndNonlocalClients(t *testing.T) {
	target, _ := url.Parse(bossOrigin)
	handler := newBossProxy(target, nil)
	for _, host := range []string{"localhost", "LOCALHOST.", "localhost:443", "127.0.0.1", "127.0.0.2:443", "[::1]:443", "0.0.0.0", "x.localhost", "bad/host", ""} {
		r := httptest.NewRequest("GET", "http://boss.example.com/boss/", nil)
		r.RemoteAddr, r.Host = "127.0.0.1:12345", host
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("host %q: status %d", host, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://boss.example.com/boss/", nil)
	r.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("nonloopback status = %d", w.Code)
	}
	for _, addr := range []string{"0.0.0.0:9081", ":9081", "192.0.2.1:9081", "localhost:9081"} {
		if err := runBossProxy([]string{"-listen", addr}); err == nil {
			t.Errorf("accepted listener %s", addr)
		}
	}
}

func TestBossProxyDownloadAndUnavailableOrigin(t *testing.T) {
	payload := bytes.Repeat([]byte("backup"), 200000)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="backup.bin"`)
		_, _ = w.Write(payload)
	}))
	target, _ := url.Parse(origin.URL)
	transport := bossProxyTransport()
	defer transport.CloseIdleConnections()
	handler := newBossProxy(target, transport)
	r := httptest.NewRequest("GET", "http://boss.example.com/boss/download", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), payload) {
		t.Fatal("download failed")
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Error("download filename lost")
	}
	origin.Close()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), target.Host) {
		t.Errorf("unsafe error response: %d %s", w.Code, w.Body.String())
	}
}
