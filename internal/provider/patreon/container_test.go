package patreon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestContainerSavedSessionRun(t *testing.T) {
	if os.Getenv("SERIAL_SYNC_TEST_CONTAINER") != "1" {
		t.Skip("run in a disposable runtime container with --network none --add-host www.patreon.com:127.0.0.1 and SERIAL_SYNC_TEST_CONTAINER=1")
	}
	root := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic fixture"}, DNSNames: []string{"www.patreon.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(root, "fixture.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	var expired atomic.Bool
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("User-Agent") != "synthetic-browser-agent" {
			t.Errorf("CLI lost captured user agent")
		}
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value != "synthetic-only" {
			t.Errorf("CLI lost session cookie")
		}
		if expired.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/current_user":
			fmt.Fprint(w, `{"data":{"id":"reader"},"included":[{"id":"42","type":"campaign","attributes":{"vanity":"ExampleCreator","name":"Fixture"}}]}`)
		case "/api/posts":
			fmt.Fprint(w, `{"data":[{"id":"123"}],"links":{"next":""}}`)
		case "/api/posts/123":
			fmt.Fprint(w, `{"data":{"id":"123","type":"post","attributes":{"title":"Chapter 1","post_type":"text","current_user_can_view":true,"url":"https://www.patreon.com/posts/chapter-1-123","content":"<p>Synthetic chapter for offline container qualification.</p>","published_at":"2026-04-01T00:00:00Z"},"relationships":{"campaign":{"data":{"id":"42"}},"user":{"data":{"id":"author"}}}},"included":[{"id":"author","type":"user","attributes":{"full_name":"Fixture Author"}}]}`)
		default:
			t.Errorf("unexpected fixture request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	server.Listener.Close()
	server.Listener, err = net.Listen("tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	sessionPath := filepath.Join(root, "session.json")
	bundle := sessionBundle{Provider: "patreon", UserAgent: "synthetic-browser-agent", Cookies: []sessionCookie{{Name: "session_id", Value: "synthetic-only", Domain: ".patreon.com", Path: "/", Secure: true, HTTPOnly: true}}}
	if err := saveSessionBundle(sessionPath, bundle); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.toml")
	config := fmt.Sprintf(`[runtime]
store_dsn = %q
artifact_root = %q
log_root = %q
support_root = %q
[[auth_profiles]]
id = "synthetic"
provider = "patreon"
mode = "username_password"
username_env = "FIXTURE_USERNAME"
password_env = "FIXTURE_PASSWORD"
session_path = %q
[[publishers]]
id = "files"
kind = "filesystem"
path = %q
enabled = true
[[sources]]
id = "fixture"
provider = "patreon"
url = "https://www.patreon.com/c/ExampleCreator/posts"
auth_profile = "synthetic"
enabled = true
[[series]]
id = "fixture"
title = "Fixture"
authors = ["Fixture Author"]
[series.output]
format = "epub"
preface_mode = "none"
[[series.inputs]]
source = "fixture"
priority = 1
match_type = "fallback"
release_role = "chapter"
content_strategy = "text_post"
`, filepath.Join(root, "state.db"), filepath.Join(root, "artifacts"), filepath.Join(root, "logs"), filepath.Join(root, "support"), sessionPath, filepath.Join(root, "publish"))
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "browser-launched")
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "google-chrome"), []byte("#!/bin/sh\ntouch "+marker+"\nexit 72\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/local/bin/serial-sync", append([]string{"--config", configPath}, args...)...)
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caPath, "HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=", "NO_PROXY=*", "PATH="+bin+":"+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := run("run"); err != nil {
		t.Fatalf("CLI saved-session run: %v\n%s", err, output)
	}
	files, err := filepath.Glob(filepath.Join(root, "publish", "*", "*", "*.epub"))
	if err != nil || len(files) != 1 {
		t.Fatalf("published EPUBs: %v %v", files, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("saved-session run launched browser")
	}
	t.Logf("saved-session CLI run published %d EPUB; requests=%d; browser launches=0; runtime processes=%v", len(files), requests.Load(), fixtureProcessCounts())
	if os.Getenv("SERIAL_SYNC_TEST_STEADY") != "1" {
		return
	}
	if err := os.Remove(filepath.Join(bin, "google-chrome")); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run("setup", "auth", "--auth-profile", "synthetic", "--force"); err == nil || !strings.Contains(output, "full image") {
		t.Fatalf("forced auth without browser: %v\n%s", err, output)
	}
	retained, err := os.ReadFile(sessionPath)
	if err != nil || string(retained) != string(saved) {
		t.Fatalf("CLI forced auth damaged saved session: %v", err)
	}
	t.Log("forced auth returned reauthentication handoff and preserved the saved bundle")
	expired.Store(true)
	if output, err := run("run"); err == nil || !strings.Contains(output, "full image") {
		t.Fatalf("expired CLI session: %v\n%s", err, output)
	}
	imported := filepath.Join(root, "import.json")
	bundle.SavedAt = time.Now()
	if err := saveSessionBundle(imported, bundle); err != nil {
		t.Fatal(err)
	}
	expired.Store(false)
	if output, err := run("setup", "auth", "--auth-profile", "synthetic", "--import-session", imported); err != nil {
		t.Fatalf("import: %v\n%s", err, output)
	}
	if output, err := run("run"); err != nil {
		t.Fatalf("resumed run: %v\n%s", err, output)
	}
	t.Log("expired session stopped; existing bundle import validated; subsequent CLI run succeeded")
}
