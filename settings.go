package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const updateServiceName = "frigotehnica-cloudflared-update"

type settingsResponse struct {
	SiteName          string `json:"siteName"`
	Hostname          string `json:"hostname"`
	AutoUpdateEnabled bool   `json:"autoUpdateEnabled"`
	IntervalHours     int    `json:"intervalHours"`
}

func (s *server) settingsPath() string {
	if s.cfg.updateConfigFile != "" {
		return s.cfg.updateConfigFile
	}
	return "/opt/frigotehnica/config/updates.json"
}

func (s *server) siteIdentity() (string, string) {
	name, host := s.cfg.siteName, s.cfg.hostname
	c, err := readUpdateConfig(s.settingsPath())
	if err == nil {
		if c.SiteName != "" {
			name = c.SiteName
		}
		if c.Hostname != "" {
			host = c.Hostname
		}
	}
	return name, host
}

func (s *server) settings(c updateConfig) settingsResponse {
	name, host := s.cfg.siteName, s.cfg.hostname
	if c.SiteName != "" {
		name = c.SiteName
	}
	if c.Hostname != "" {
		host = c.Hostname
	}
	if host == "not-configured" {
		host = ""
	}
	return settingsResponse{SiteName: name, Hostname: host, AutoUpdateEnabled: c.Enabled, IntervalHours: c.IntervalHours}
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	c, err := readUpdateConfig(s.settingsPath())
	if err != nil {
		http.Error(w, "Settings could not be loaded", http.StatusInternalServerError)
		return
	}
	writeJSON(w, s.settings(c))
}

func normalizeSite(name, host string) (string, string, error) {
	name = strings.TrimSpace(name)
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", "", errors.New("Site name must contain 1 to 100 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", "", errors.New("Site name must not contain control characters")
		}
	}
	if host == "" {
		host = "not-configured"
	}
	if len(host) > 253 {
		return "", "", errors.New("Hostname is too long")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", errors.New("Enter a hostname without https://, a port, or a path")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", "", errors.New("Enter a hostname without https://, a port, or a path")
			}
		}
	}
	return name, host, nil
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.trustedProxyRequest(r) && s.passwordChangeRequired() {
		http.Error(w, "Change the one-time administrator password first", http.StatusPreconditionRequired)
		return
	}
	var body struct {
		SiteName          string `json:"siteName"`
		Hostname          string `json:"hostname"`
		AutoUpdateEnabled *bool  `json:"autoUpdateEnabled"`
		IntervalHours     int    `json:"intervalHours"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.AutoUpdateEnabled == nil {
		http.Error(w, "Invalid settings", http.StatusBadRequest)
		return
	}
	name, host, err := normalizeSite(body.SiteName, body.Hostname)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	previous, err := readUpdateConfig(s.settingsPath())
	if err != nil {
		http.Error(w, "Settings could not be loaded", http.StatusInternalServerError)
		return
	}
	next := previous
	next.SiteName, next.Hostname, next.Enabled, next.IntervalHours = name, host, *body.AutoUpdateEnabled, body.IntervalHours
	if err := next.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	encoded, _ := json.MarshalIndent(next, "", "  ")
	if err := writeSecretFile(s.settingsPath(), string(encoded)+"\n"); err != nil {
		http.Error(w, "Settings could not be saved", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	syncService := s.updateService
	if syncService == nil {
		syncService = syncUpdaterService
	}
	if !s.cfg.demo {
		if err := syncService(ctx, next.Enabled); err != nil {
			old, _ := json.MarshalIndent(previous, "", "  ")
			if rollbackErr := writeSecretFile(s.settingsPath(), string(old)+"\n"); rollbackErr != nil {
				http.Error(w, "Settings were saved, but the update service could not be configured; check the system service", http.StatusInternalServerError)
				return
			}
			recovery, cancelRecovery := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancelRecovery()
			_ = syncService(recovery, previous.Enabled)
			http.Error(w, "The update service could not be configured; previous settings were restored", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, s.settings(next))
}

func syncUpdaterService(ctx context.Context, enabled bool) error {
	if _, err := os.Stat("/etc/init.d/" + updateServiceName); err != nil {
		return errors.New("updater service is not installed; run the full installer")
	}
	if enabled {
		if out, err := exec.CommandContext(ctx, "rc-update", "add", updateServiceName, "default").CombinedOutput(); err != nil {
			return fmt.Errorf("enable updater: %w: %s", err, out)
		}
		if exec.CommandContext(ctx, "rc-service", updateServiceName, "status").Run() == nil {
			return nil
		}
		return runOpenRCAction(ctx, updateServiceName, "start")
	}
	if exec.CommandContext(ctx, "rc-service", updateServiceName, "status").Run() == nil {
		if err := runOpenRCAction(ctx, updateServiceName, "stop"); err != nil {
			return err
		}
	}
	// Removing an already absent runlevel entry may return nonzero.
	_ = exec.CommandContext(ctx, "rc-update", "del", updateServiceName, "default").Run()
	return nil
}
