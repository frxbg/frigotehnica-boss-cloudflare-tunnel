package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Keep the origin and source fixed: this is not a general-purpose open proxy.
// BOSS 1.15 treats source 127.0.0.1 as its local console and hides file inputs.
// A different loopback source preserves remote file handling without a LAN IP,
// forwarded-header trust, firewall changes, or modifications to CAREL files.
const bossOrigin = "https://127.0.0.1:443"

func bossProxyTransport() *http.Transport {
	dialer := &net.Dialer{
		LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")},
		Timeout:   10 * time.Second, KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		// Never honor HTTP_PROXY/HTTPS_PROXY for this local, fixed origin.
		DialContext: dialer.DialContext,
		TLSClientConfig: &tls.Config{
			// The vendor's self-signed certificate is used only over loopback.
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		},
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   16,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
}

func validBossProxyHost(host string) bool {
	if strings.ContainsAny(host, "/\\@?# \t\r\n") || host == "" {
		return false
	}
	if strings.Contains(host, ":") {
		var err error
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			return false
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsUnspecified()
	}
	return true
}

func newBossProxy(origin *url.URL, transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(origin)
			// BOSS builds absolute links from Host and the upstream TLS scheme.
			p.Out.Host = p.In.Host
			// Rewrite already strips Forwarded and X-Forwarded-* headers.
			// Do not restore client-provided forwarding/authentication headers.
			p.Out.Header.Del("X-Frigotehnica-Ajenti")
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Do not log URLs, cookies, query parameters, or uploaded content.
			log.Print("BOSS origin request failed")
			http.Error(w, "BOSS interface is unavailable", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(ip).IsLoopback() {
			http.Error(w, "Loopback connections only", http.StatusForbidden)
			return
		}
		// A localhost Host header would independently trigger BOSS console mode.
		if !validBossProxyHost(r.Host) {
			http.Error(w, "Use the public BOSS hostname; leave HTTP Host Header unset in Cloudflare", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func runBossProxy(args []string) error {
	flags := flag.NewFlagSet("boss-proxy", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:9081", "loopback-only BOSS proxy listener")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected boss-proxy arguments")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("BOSS proxy must listen on a loopback IP address")
	}
	origin, _ := url.Parse(bossOrigin)
	transport := bossProxyTransport()
	defer transport.CloseIdleConnections()
	server := &http.Server{
		Addr: *listen, Handler: newBossProxy(origin, transport),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second,
		MaxHeaderBytes: 64 << 10,
		// Stream uploads/downloads without the management UI's 8 KiB body cap
		// or its short total read/write deadlines. BOSS enforces its own limits.
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.ListenAndServe()
	}()
	log.Printf("BOSS upload proxy listening on %s", *listen)
	select {
	case err = <-serveError:
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		err = <-serveError
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
