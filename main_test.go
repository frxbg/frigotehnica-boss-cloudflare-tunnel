package main

import (
	"os"
	"path/filepath"
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
