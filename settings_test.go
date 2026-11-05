package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func settingsServer(t *testing.T) *server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "updates.json")
	if err := writeSecretFile(path, `{"enabled":true,"intervalHours":24,"legacyCA":true}`); err != nil {
		t.Fatal(err)
	}
	return &server{cfg: config{siteName: "BOSS Site", hostname: "not-configured", updateConfigFile: path}, sessions: map[string]session{}}
}

const settingsBody = `{"siteName":"Warehouse BOSS","hostname":"BOSS.Example.com","autoUpdateEnabled":true,"intervalHours":48}`

func TestSettingsPersistIdentityIntervalAndLegacyMode(t *testing.T) {
	s := settingsServer(t)
	called := false
	s.updateService = func(ctx context.Context, enabled bool) error { called = enabled; return nil }
	w := httptest.NewRecorder()
	s.saveSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(settingsBody)))
	if w.Code != http.StatusOK || !called {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	c, err := readUpdateConfig(s.settingsPath())
	if err != nil || c.IntervalHours != 48 || c.SiteName != "Warehouse BOSS" || c.Hostname != "boss.example.com" || !c.LegacyCA {
		t.Fatal(c, err)
	}
	// Recreate the server as after a backend restart: persisted identity wins
	// over the original installer arguments.
	restarted := &server{cfg: s.cfg}
	name, host := restarted.siteIdentity()
	if name != "Warehouse BOSS" || host != "boss.example.com" {
		t.Fatal(name, host)
	}
	response := httptest.NewRecorder()
	restarted.getSettings(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var settings settingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil || settings.IntervalHours != 48 {
		t.Fatal(settings, err)
	}
}

func TestInvalidSettingsCannotModifySavedConfiguration(t *testing.T) {
	s := settingsServer(t)
	original, _ := os.ReadFile(s.settingsPath())
	s.updateService = func(context.Context, bool) error { t.Fatal("service called for invalid input"); return nil }
	for _, body := range []string{
		strings.Replace(settingsBody, `"intervalHours":48`, `"intervalHours":0`, 1),
		strings.Replace(settingsBody, `BOSS.Example.com`, `https://boss.example.com/boss`, 1),
		strings.Replace(settingsBody, `Warehouse BOSS`, ``, 1),
	} {
		w := httptest.NewRecorder()
		s.saveSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
		got, _ := os.ReadFile(s.settingsPath())
		if string(got) != string(original) {
			t.Fatal("invalid settings changed the file")
		}
	}
}

func TestFailedUpdateServiceRestoresPreviousSettings(t *testing.T) {
	s := settingsServer(t)
	s.updateService = func(context.Context, bool) error { return errors.New("service unavailable") }
	w := httptest.NewRecorder()
	s.saveSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(settingsBody)))
	if w.Code != http.StatusInternalServerError {
		t.Fatal(w.Code)
	}
	c, err := readUpdateConfig(s.settingsPath())
	if err != nil || c.IntervalHours != 24 || c.SiteName != "" || !c.LegacyCA {
		t.Fatal(c, err)
	}
}

func TestSettingsSaveRequiresSessionAndCSRF(t *testing.T) {
	s := settingsServer(t)
	s.cfg.demo = true
	handler := s.requireAuth(s.requireCSRF(s.saveSettings))
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(settingsBody)))
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated save accepted", w.Code)
	}
	s.sessions["valid"] = session{csrf: "csrf", expires: time.Now().Add(time.Hour)}
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(settingsBody))
	request.AddCookie(&http.Cookie{Name: "ft_session", Value: "valid"})
	w = httptest.NewRecorder()
	handler(w, request)
	if w.Code != http.StatusForbidden {
		t.Fatal("save without CSRF accepted", w.Code)
	}
}
