//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// testManagedBrowser uses a fresh Linux HOME and NSS database, never the user's
// trust store. Both launches use ordinary hostname resolution and TLS checking.
func testManagedBrowser(t *testing.T, origin, root string) {
	t.Helper()
	binary := os.Getenv("BACKLOT_BROWSER_TEST_BINARY")
	certutil := os.Getenv("BACKLOT_CERTUTIL_TEST_BINARY")
	if binary == "" || certutil == "" {
		t.Fatal("browser acceptance requires explicit browser and certutil executables")
	}
	home := t.TempDir()
	db := filepath.Join(home, ".pki", "nssdb")
	if err := os.MkdirAll(db, 0700); err != nil {
		t.Fatal(err)
	}
	runCertutil := func(args ...string) {
		t.Helper()
		command := exec.Command(certutil, args...)
		command.Env = append(os.Environ(), "HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("private NSS setup: %v %s", err, output)
		}
	}
	runCertutil("-N", "--empty-password", "-d", "sql:"+db)
	visit := func() (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
		options = append(options, chromedp.ExecPath(binary), chromedp.UserDataDir(t.TempDir()), chromedp.Env("HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share")))
		allocator, cancelAllocator := chromedp.NewExecAllocator(ctx, options...)
		defer cancelAllocator()
		browser, cancelBrowser := chromedp.NewContext(allocator)
		defer cancelBrowser()
		var body string
		err := chromedp.Run(browser, chromedp.Navigate(origin+"/ssr"), chromedp.Text("body", &body, chromedp.ByQuery))
		return body, err
	}
	if _, err := visit(); err == nil || !strings.Contains(err.Error(), "ERR_CERT_AUTHORITY_INVALID") {
		t.Fatalf("untrusted fresh browser did not reject local root: %v", err)
	}
	// Import only the public certificate into this disposable browser environment.
	runCertutil("-A", "-d", "sql:"+db, "-n", "Backlot owned fixture", "-t", "C,,", "-i", root)
	body, err := visit()
	if err != nil || body != "ssr:api:/value" {
		t.Fatalf("trusted normal-DNS browser HTTPS/SSR: body=%q err=%v", body, err)
	}
	t.Log("disposable Linux browser rejected missing trust then accepted explicit public-root trust, canonical hostname and SSR")
}

// managedBrowserOptIn validates the pair before fixture allocation. Absence is
// optional coverage, never evidence that browser trust passed.
func managedBrowserOptIn(browser, certutil string) (bool, error) {
	if (browser == "") != (certutil == "") {
		return false, errors.New("browser acceptance requires both BACKLOT_BROWSER_TEST_BINARY and BACKLOT_CERTUTIL_TEST_BINARY")
	}
	return browser != "", nil
}

func TestManagedBrowserOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, browser, certutil string
		enabled, invalid        bool
	}{
		{name: "absent"},
		{name: "browser only", browser: "/invalid/browser", invalid: true},
		{name: "certutil only", certutil: "/invalid/certutil", invalid: true},
		{name: "both", browser: "/explicit/browser", certutil: "/explicit/certutil", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, err := managedBrowserOptIn(tc.browser, tc.certutil)
			if enabled != tc.enabled || (err != nil) != tc.invalid {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}
