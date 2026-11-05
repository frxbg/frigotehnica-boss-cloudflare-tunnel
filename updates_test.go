package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloudflaredVersionComparison(t *testing.T) {
	for _, tt := range []struct {
		latest, current string
		newer           bool
	}{
		{"2026.10.2", "2026.9.9", true}, {"2026.10.2", "2026.10.2", false},
		{"2026.10.1", "2026.10.2", false}, {"v2027.1.0", "2026.12.99", true},
	} {
		got, err := newerCloudflared(tt.latest, tt.current)
		if err != nil || got != tt.newer {
			t.Fatalf("%+v: got %v, %v", tt, got, err)
		}
	}
	if _, err := newerCloudflared("2026.11.0-rc1", "2026.10.2"); err == nil {
		t.Fatal("prerelease accepted")
	}
}

func TestUpdateScheduleValidationAndPersistence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "updates.json")
	c, err := readUpdateConfig(p)
	if err != nil || !c.Enabled || c.IntervalHours != 24 {
		t.Fatal(c, err)
	}
	for _, hours := range []int{-1, 0, 8761} {
		if (updateConfig{IntervalHours: hours}).validate() == nil {
			t.Fatal("invalid interval accepted", hours)
		}
	}
	if err := writeSecretFile(p, `{"enabled":false,"intervalHours":48,"legacyCA":true}`); err != nil {
		t.Fatal(err)
	}
	c, err = readUpdateConfig(p)
	if err != nil || c.Enabled || c.IntervalHours != 48 || !c.LegacyCA {
		t.Fatal(c, err)
	}
}

func testUpdater(t *testing.T, current, digest string, binary []byte, running bool, restartFail bool) (*cloudflareUpdater, *[]string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(p, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	asset, _ := cloudflaredAsset(runtime.GOARCH)
	actions := []string{}
	u := &cloudflareUpdater{
		binary: p, releaseURL: cloudflaredLatest,
		version: func(ctx context.Context, path string) (string, error) {
			if path == p {
				return current, nil
			}
			return "2026.10.2", nil
		},
		running: func(context.Context) bool { return running },
		action: func(ctx context.Context, action string) error {
			actions = append(actions, action)
			if action == "restart" && restartFail {
				return fmt.Errorf("start failed")
			}
			return nil
		},
		client: &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
			content := string(binary)
			if r.URL.String() == cloudflaredLatest {
				content = fmt.Sprintf(`{"tag_name":"2026.10.2","assets":[{"name":%q,"browser_download_url":%q,"digest":%q,"size":%d}]}`, asset, "https://github.com/cloudflare/cloudflared/releases/download/2026.10.2/"+asset, "sha256:"+digest, len(binary))
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(content)), Header: http.Header{}}, nil
		})},
	}
	return u, &actions
}

func TestUpdaterNoChangeAndCheckOnlyNeverRestart(t *testing.T) {
	for _, tt := range []struct {
		current string
		check   bool
		state   string
	}{
		{"2026.10.2", false, "up-to-date"}, {"2026.11.0", false, "up-to-date"}, {"2026.9.0", true, "available"},
	} {
		u, actions := testUpdater(t, tt.current, "", nil, true, false)
		result, err := u.run(context.Background(), tt.check)
		if err != nil || result.State != tt.state || len(*actions) != 0 {
			t.Fatal(result, err, *actions)
		}
		b, _ := os.ReadFile(u.binary)
		if string(b) != "old binary" {
			t.Fatal("binary changed")
		}
	}
}

func TestUpdaterRejectsCorruptDownloadBeforeReplacement(t *testing.T) {
	u, actions := testUpdater(t, "2026.9.0", strings.Repeat("0", 64), []byte("corrupt"), true, false)
	_, err := u.run(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatal(err)
	}
	if len(*actions) != 0 {
		t.Fatal("service changed on corrupt download", *actions)
	}
	b, _ := os.ReadFile(u.binary)
	if string(b) != "old binary" {
		t.Fatal("old binary changed")
	}
}

func TestUpdaterRejectsMissingAssetDigest(t *testing.T) {
	u, actions := testUpdater(t, "2026.9.0", "", []byte("payload"), true, false)
	_, err := u.run(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatal(err)
	}
	if len(*actions) != 0 {
		t.Fatal("service changed before verification", *actions)
	}
}

func TestUpdaterInstallsVerifiedELFAndRollsBack(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("successful ELF replacement is tested on Linux CI")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary)
	for _, tt := range []struct {
		running, fail bool
		actions       string
	}{
		{true, false, "restart"}, {false, false, ""}, {true, true, "restart,stop,start"},
	} {
		u, actions := testUpdater(t, "2026.9.0", hex.EncodeToString(hash[:]), binary, tt.running, tt.fail)
		result, err := u.run(context.Background(), false)
		if tt.fail {
			if err == nil || !strings.Contains(err.Error(), "rolled back") {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(u.binary)
			if string(b) != "old binary" {
				t.Fatal("rollback did not restore old binary")
			}
		} else {
			if err != nil || result.State != "updated" {
				t.Fatal(result, err)
			}
			b, _ := os.ReadFile(u.binary)
			if string(b) != string(binary) {
				t.Fatal("new binary was not installed")
			}
		}
		if strings.Join(*actions, ",") != tt.actions {
			t.Fatal(*actions)
		}
	}
}

func TestUpdateLockExcludesConcurrentUpdater(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	release, err := lockUpdate(p)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockUpdate(p); err == nil {
		second()
		t.Fatal("second updater acquired lock")
	}
	release()
	release, err = lockUpdate(p)
	if err != nil {
		t.Fatal("lock was not released", err)
	}
	release()
}
