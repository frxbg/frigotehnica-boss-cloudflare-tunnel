package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the real installer in an isolated filesystem with fake OpenRC.
// Never touch /opt, /etc, the device network, or real services in this test.
func TestProxyOnlyInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installer requires POSIX utilities")
	}
	source, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name             string
		badHash, noStart bool
	}{
		{name: "install"}, {name: "staged", noStart: true}, {name: "checksum", badHash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			app, initDir, bin := filepath.Join(dir, "app"), filepath.Join(dir, "init"), filepath.Join(dir, "bin")
			for _, p := range []string{app, initDir, bin, filepath.Join(app, "config")} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0755); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range []string{"cloudflared", "frigotehnica-tunnel-ui", "config/tunnel.token", "config/admin.auth", "frigotehnica-boss-proxy"} {
				write(filepath.Join(app, p), "original")
			}
			write(filepath.Join(initDir, "frigotehnica-boss-proxy"), "original service")
			write(filepath.Join(bin, "id"), "#!/bin/sh\necho 0\n")
			write(filepath.Join(bin, "uname"), "#!/bin/sh\necho x86_64\n")
			write(filepath.Join(bin, "rc-service"), "#!/bin/sh\nprintf '%s %s\\n' \"$1\" \"$2\" >> \"$INSTALL_TEST_LOG\"\n")
			write(filepath.Join(bin, "rc-update"), "#!/bin/sh\nprintf 'update %s %s\\n' \"$1\" \"$2\" >> \"$INSTALL_TEST_LOG\"\n")
			write(filepath.Join(bin, "sleep"), "#!/bin/sh\nexit 0\n")
			asset := "#!/bin/sh\necho candidate\n"
			write(filepath.Join(dir, "frigotehnica-tunnel-ui-linux-amd64"), asset)
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(asset)))
			if tc.badHash {
				digest = strings.Repeat("0", 64)
			}
			script := strings.ReplaceAll(string(source), "/opt/frigotehnica", app)
			script = strings.ReplaceAll(script, "/etc/init.d", initDir)
			script = strings.ReplaceAll(script, "UI_AMD64_SHA256=PINNED_BY_RELEASE_WORKFLOW", "UI_AMD64_SHA256="+digest)
			path := filepath.Join(dir, "install.sh")
			write(path, script)
			args := []string{path, "--boss-proxy-only"}
			if tc.noStart {
				args = append(args, "--no-start", "--no-enable")
			}
			cmd := exec.Command("sh", args...)
			logPath := filepath.Join(dir, "services.log")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "INSTALL_TEST_LOG="+logPath)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.badHash {
				t.Fatalf("err=%v output=%s", err, output)
			}
			for _, p := range []string{"cloudflared", "frigotehnica-tunnel-ui", "config/tunnel.token", "config/admin.auth"} {
				got, _ := os.ReadFile(filepath.Join(app, p))
				if string(got) != "original" {
					t.Errorf("changed %s", p)
				}
			}
			calls, _ := os.ReadFile(logPath)
			if strings.Contains(string(calls), "cloudflared") || strings.Contains(string(calls), "tunnel-ui") {
				t.Fatalf("touched other services: %s", calls)
			}
			if tc.badHash {
				if len(calls) != 0 {
					t.Errorf("services touched before checksum validation: %s", calls)
				}
				got, _ := os.ReadFile(filepath.Join(app, "frigotehnica-boss-proxy"))
				if string(got) != "original" {
					t.Error("replaced unverified binary")
				}
				return
			}
			backups, _ := filepath.Glob(filepath.Join(app, "backups", "*", "boss-proxy.binary"))
			if len(backups) != 1 {
				t.Fatal("missing binary backup")
			}
			old, _ := os.ReadFile(backups[0])
			if string(old) != "original" {
				t.Error("invalid backup")
			}
			installed, _ := os.ReadFile(filepath.Join(app, "frigotehnica-boss-proxy"))
			if string(installed) != asset {
				t.Error("wrong installed binary")
			}
			if tc.noStart {
				if strings.Contains(string(calls), " start") || strings.Contains(string(calls), "update add") {
					t.Errorf("ignored no-start/no-enable: %s", calls)
				}
			} else if !strings.Contains(string(calls), "frigotehnica-boss-proxy start") || !strings.Contains(string(calls), "update add frigotehnica-boss-proxy") {
				t.Errorf("missing startup/boot registration: %s", calls)
			}
		})
	}
}
