package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const cloudflaredLatest = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"
const maxUpdateBinary = 128 << 20

type updateOptions struct {
	configPath        string
	hours             int
	enabled, legacyCA bool
}

type updateConfig struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"intervalHours"`
	LegacyCA      bool `json:"legacyCA"`
}

type updateResult struct {
	CheckedAt time.Time `json:"checkedAt"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
}

type cloudflareRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	} `json:"assets"`
}

type cloudflareUpdater struct {
	binary, service, releaseURL string
	client                      *http.Client
	version                     func(context.Context, string) (string, error)
	running                     func(context.Context) bool
	action                      func(context.Context, string) error
}

func readUpdateConfig(path string) (updateConfig, error) {
	c := updateConfig{Enabled: true, IntervalHours: 24}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, c.validate()
}

func (c updateConfig) validate() error {
	if c.IntervalHours < 1 || c.IntervalHours > 8760 {
		return errors.New("update interval must be between 1 and 8760 hours")
	}
	return nil
}

func (o updateOptions) command(cfg config, args []string) (bool, error) {
	if len(args) != 1 {
		return false, nil
	}
	command := args[0]
	switch command {
	case "configure-updates", "update-cloudflared", "check-cloudflared", "update-loop", "update-status":
	default:
		return false, nil
	}
	c, err := readUpdateConfig(o.configPath)
	if err != nil {
		return true, err
	}
	if command == "update-status" {
		b, err := os.ReadFile(o.configPath + ".status")
		if err != nil && !os.IsNotExist(err) {
			return true, err
		}
		fmt.Printf("Schedule: enabled=%t, interval=%dh, legacyCA=%t\n%s\n", c.Enabled, c.IntervalHours, c.LegacyCA, b)
		return true, nil
	}
	if command == "configure-updates" {
		flag.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "update-interval-hours":
				c.IntervalHours = o.hours
			case "update-enabled":
				c.Enabled = o.enabled
			case "update-legacy-ca":
				c.LegacyCA = o.legacyCA
			}
		})
		if err := c.validate(); err != nil {
			return true, err
		}
		if err := os.MkdirAll(filepath.Dir(o.configPath), 0700); err != nil {
			return true, err
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		return true, writeSecretFile(o.configPath, string(b)+"\n")
	}
	if command == "update-loop" {
		return true, o.loop(cfg)
	}
	u := newCloudflareUpdater(cfg, c)
	result, err := u.run(context.Background(), command == "check-cloudflared")
	if command != "check-cloudflared" {
		_ = saveUpdateResult(o.configPath, result, err)
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	return true, err
}

func newCloudflareUpdater(cfg config, c updateConfig) *cloudflareUpdater {
	if c.LegacyCA {
		log.Print("Legacy CA mode: TLS identity is not verified; release digests detect corruption but do not authenticate the release")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.LegacyCA} // Explicit legacy CA opt-in.
	return &cloudflareUpdater{
		binary: cfg.cloudflared, service: cfg.service, releaseURL: cloudflaredLatest,
		client: &http.Client{Transport: transport, Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" {
				return errors.New("invalid download redirect")
			}
			return nil
		}},
		version: cloudflaredVersion,
		running: func(ctx context.Context) bool {
			return exec.CommandContext(ctx, "rc-service", cfg.service, "status").Run() == nil
		},
		action: func(ctx context.Context, action string) error { return runOpenRCAction(ctx, cfg.service, action) },
	}
}

func (o updateOptions) loop(cfg config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Initial delay keeps boot and remote installation responsive. Persisted
	// check times prevent a service restart from forcing another update.
	next := time.Now().Add(2 * time.Minute)
	for {
		c, err := readUpdateConfig(o.configPath)
		if err != nil {
			log.Printf("Update schedule: %v", err)
		} else if c.Enabled {
			var last updateResult
			if b, err := os.ReadFile(o.configPath + ".status"); err == nil && json.Unmarshal(b, &last) == nil && !last.CheckedAt.IsZero() {
				next = last.CheckedAt.Add(time.Duration(c.IntervalHours) * time.Hour)
			}
			if !time.Now().Before(next) {
				result, updateErr := newCloudflareUpdater(cfg, c).run(ctx, false)
				if err := saveUpdateResult(o.configPath, result, updateErr); err != nil {
					log.Printf("Save update status: %v", err)
				}
				if updateErr != nil {
					log.Printf("Cloudflared update: %v", updateErr)
				} else {
					log.Printf("Cloudflared update: %s (%s -> %s)", result.State, result.Current, result.Latest)
				}
				next = time.Now().Add(time.Duration(c.IntervalHours) * time.Hour)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

func saveUpdateResult(path string, result updateResult, err error) error {
	if err != nil {
		result.State, result.Message = "error", err.Error()
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	return writeSecretFile(path+".status", string(b)+"\n")
}

var stableCloudflaredVersion = regexp.MustCompile(`^v?(\d{4})\.(\d{1,2})\.(\d+)$`)

func newerCloudflared(latest, current string) (bool, error) {
	a, b := stableCloudflaredVersion.FindStringSubmatch(latest), stableCloudflaredVersion.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false, fmt.Errorf("cannot compare stable versions %q and %q", latest, current)
	}
	for i := 1; i <= 3; i++ {
		x, err := strconv.Atoi(a[i])
		if err != nil {
			return false, err
		}
		y, err := strconv.Atoi(b[i])
		if err != nil {
			return false, err
		}
		if x != y {
			return x > y, nil
		}
	}
	return false, nil
}

func cloudflaredVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) && stableCloudflaredVersion.MatchString(fields[i+1]) {
			return strings.TrimPrefix(fields[i+1], "v"), nil
		}
	}
	return "", errors.New("cloudflared returned an unrecognized version")
}

func cloudflaredAsset(arch string) (string, error) {
	switch arch {
	case "amd64":
		return "cloudflared-linux-amd64", nil
	case "arm":
		return "cloudflared-linux-armhf", nil
	default:
		return "", fmt.Errorf("unsupported cloudflared architecture %s", arch)
	}
}

func (u *cloudflareUpdater) get(ctx context.Context, target string) (*http.Response, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" {
		return nil, errors.New("update downloads must use HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Frigotehnica-Cloudflared-Updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("update download returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (u *cloudflareUpdater) run(ctx context.Context, checkOnly bool) (result updateResult, err error) {
	result.CheckedAt = time.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	// Kernel lock is automatically released if the updater is killed.
	unlock, err := lockUpdate(u.binary + ".update-lock")
	if err != nil {
		return result, err
	}
	defer unlock()
	result.Current, err = u.version(ctx, u.binary)
	if err != nil {
		return result, fmt.Errorf("read installed version: %w", err)
	}
	resp, err := u.get(ctx, u.releaseURL)
	if err != nil {
		return result, err
	}
	var release cloudflareRelease
	err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release)
	resp.Body.Close()
	if err != nil {
		return result, err
	}
	if release.Draft || release.Prerelease {
		return result, errors.New("latest release is not stable")
	}
	result.Latest = strings.TrimPrefix(release.Tag, "v")
	newer, err := newerCloudflared(result.Latest, result.Current)
	if err != nil {
		return result, err
	}
	if !newer {
		result.State = "up-to-date"
		return result, nil
	}
	result.State = "available"
	if checkOnly {
		return result, nil
	}
	assetName, err := cloudflaredAsset(runtime.GOARCH)
	if err != nil {
		return result, err
	}
	for _, asset := range release.Assets {
		if asset.Name != assetName {
			continue
		}
		if !strings.HasPrefix(asset.Digest, "sha256:") || len(asset.Digest) != 71 {
			return result, errors.New("release is missing a SHA-256 asset digest")
		}
		want, err := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil || len(want) != 32 {
			return result, errors.New("invalid asset SHA-256 digest")
		}
		if asset.Size <= 0 || asset.Size > maxUpdateBinary {
			return result, errors.New("invalid update asset size")
		}
		if !strings.HasPrefix(asset.URL, "https://github.com/cloudflare/cloudflared/releases/download/"+release.Tag+"/") {
			return result, errors.New("unexpected Cloudflare asset URL")
		}
		staged, err := u.download(ctx, asset.URL, asset.Size, want)
		if err != nil {
			return result, err
		}
		defer os.Remove(staged)
		gotVersion, err := u.version(ctx, staged)
		if err != nil || gotVersion != result.Latest {
			return result, errors.New("downloaded binary version does not match release")
		}
		if err := u.replace(ctx, staged); err != nil {
			return result, err
		}
		result.State = "updated"
		return result, nil
	}
	return result, fmt.Errorf("release does not provide %s", assetName)
}

func (u *cloudflareUpdater) download(ctx context.Context, target string, size int64, want []byte) (string, error) {
	resp, err := u.get(ctx, target)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp(filepath.Dir(u.binary), ".cloudflared-update-*")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, size+1))
	if err != nil {
		return "", err
	}
	if n != size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), hex.EncodeToString(want)) {
		return "", errors.New("cloudflared download failed size/SHA-256 verification")
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := verifyUpdateELF(f.Name()); err != nil {
		return "", err
	}
	if err := os.Chmod(f.Name(), 0755); err != nil {
		return "", err
	}
	ok = true
	return f.Name(), nil
}

func verifyUpdateELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("invalid Linux binary: %w", err)
	}
	defer f.Close()
	if (runtime.GOARCH == "amd64" && f.Machine == elf.EM_X86_64 && f.Class == elf.ELFCLASS64) || (runtime.GOARCH == "arm" && f.Machine == elf.EM_ARM && f.Class == elf.ELFCLASS32) {
		return nil
	}
	return errors.New("downloaded cloudflared binary has the wrong architecture")
}

func (u *cloudflareUpdater) replace(ctx context.Context, staged string) error {
	backup := u.binary + ".previous"
	// Copy the old executable so the live path remains intact until rename.
	in, err := os.Open(u.binary)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	wasRunning := u.running(ctx)
	if err := os.Rename(staged, u.binary); err != nil {
		return err
	}
	if !wasRunning {
		return nil
	}
	if err := u.action(ctx, "restart"); err != nil {
		// Give recovery its own timeout even if the update deadline expired.
		recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer recoveryCancel()
		_ = u.action(recoveryCtx, "stop")
		if restoreErr := os.Rename(backup, u.binary); restoreErr != nil {
			return fmt.Errorf("restart failed: %v; rollback failed: %w", err, restoreErr)
		}
		if recoveryErr := u.action(recoveryCtx, "start"); recoveryErr != nil {
			return fmt.Errorf("old binary restored but service recovery failed: %w", recoveryErr)
		}
		return fmt.Errorf("update rolled back after service restart failed: %w", err)
	}
	return nil
}
