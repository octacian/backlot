// The release fixture is application code, not a Backlot lifecycle wrapper.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/coder/websocket"
	"github.com/go-sql-driver/mysql"
)

type value struct {
	Value string `json:"value"`
}

func database() (*sql.DB, error) {
	config := mysql.NewConfig()
	config.User, config.Passwd, config.DBName = "app", os.Getenv("DB_PASSWORD"), "app"
	config.Net, config.Addr = "tcp", net.JoinHostPort(os.Getenv("DB_HOST"), os.Getenv("DB_PORT"))
	config.Timeout, config.ReadTimeout, config.WriteTimeout = 3*time.Second, 3*time.Second, 3*time.Second
	return sql.Open("mysql", config.FormatDSN())
}

func prepare(ctx context.Context) error {
	db, err := database()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	// Image TCP availability precedes completion of its application-user setup.
	for {
		if err = db.PingContext(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("database application credentials did not become ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS fixture (id INT PRIMARY KEY, value VARCHAR(255) NOT NULL)"); err != nil {
		return errors.New("create fixture table failed")
	}
	if _, err = db.ExecContext(ctx, "INSERT IGNORE INTO fixture VALUES (1, 'fresh')"); err != nil {
		return errors.New("initialize fixture row failed")
	}
	fmt.Println("database application preparation complete")
	return nil
}

func api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/value", func(w http.ResponseWriter, r *http.Request) {
		db, err := database()
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = db.Close() }()
		if r.Method == http.MethodPost {
			var input value
			if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil || len(input.Value) > 255 {
				http.Error(w, "invalid value", 400)
				return
			}
			if _, err := db.ExecContext(r.Context(), "UPDATE fixture SET value=? WHERE id=1", input.Value); err != nil {
				http.Error(w, "write failed", http.StatusServiceUnavailable)
				return
			}
		} else if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		var result value
		if err := db.QueryRowContext(r.Context(), "SELECT value FROM fixture WHERE id=1").Scan(&result.Value); err != nil {
			http.Error(w, "read failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ready") })
	return mux
}

const page = `<!doctype html><html><head><title>Backlot release fixture</title></head><body>
<h1>Backlot release fixture</h1><p id="build">{{.Message}}</p><p id="ssr">SSR: {{.Value}}</p>
<label>Database value <input id="value" value="browser-mutated"></label><button id="save">Save value</button><p id="result">Loading browser checks</p>
<script>
async function check(){const r=await fetch('/api/value');if(!r.ok)throw Error('API '+r.status);const v=await r.json();document.querySelector('#result').textContent='API: '+v.value;return v.value;}
document.querySelector('#save').onclick=async()=>{await fetch('/api/value',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({value:document.querySelector('#value').value})});await check();};
(async()=>{await check();const ws=new WebSocket(location.origin.replace('https:','wss:')+'/ws');const payload='browser-'+crypto.randomUUID();ws.onopen=()=>ws.send(payload);ws.onmessage=e=>{if(e.data!==payload)throw Error('WebSocket mismatch');document.body.dataset.acceptance='passed';document.querySelector('#result').textContent+='; WebSocket verified';ws.close();};})().catch(e=>{document.querySelector('#result').textContent=String(e);});
</script></body></html>`

func web() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		response, err := (&http.Client{Timeout: 5 * time.Second}).Get(os.Getenv("ORIGIN") + "/api/value")
		if err != nil {
			http.Error(w, "SSR origin request failed", http.StatusBadGateway)
			return
		}
		defer func() { _ = response.Body.Close() }()
		var result value
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&result) != nil {
			http.Error(w, "SSR API response failed", http.StatusBadGateway)
			return
		}
		message, err := os.ReadFile(os.Getenv("MESSAGE"))
		if err != nil {
			http.Error(w, "build output unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = template.Must(template.New("page").Parse(page)).Execute(w, struct{ Message, Value string }{strings.TrimSpace(string(message)), result.Value})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "listener ready") })
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "fixture", Value: "yes", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, os.Getenv("ORIGIN")+"/", http.StatusFound)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		c.SetReadLimit(1024)
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		for {
			kind, payload, err := c.Read(ctx)
			if err != nil {
				return
			}
			if c.Write(ctx, kind, payload) != nil {
				return
			}
		}
	})
	return mux
}

func backendTest(ctx context.Context) error {
	db, err := database()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var before string
	if err := db.QueryRowContext(ctx, "SELECT value FROM fixture WHERE id=1").Scan(&before); err != nil {
		return errors.New("read test database failed")
	}
	if before != "fresh" {
		return fmt.Errorf("test database was not fresh: %q", before)
	}
	if _, err := db.ExecContext(ctx, "UPDATE fixture SET value='test-mutated' WHERE id=1"); err != nil {
		return errors.New("test database mutation failed")
	}
	var after string
	if err := db.QueryRowContext(ctx, "SELECT value FROM fixture WHERE id=1").Scan(&after); err != nil || after != "test-mutated" {
		return errors.New("test mutation not observed")
	}
	fmt.Println("TestDatabase: fresh data and mutation verified")
	return nil
}

func browserTest(ctx context.Context) error {
	// The optional browser image owns Chromium and all of its descendants.
	// Wait for application state through CDP instead of racing --dump-dom.
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(os.Getenv("BROWSER")), chromedp.NoSandbox, chromedp.Flag("disable-dev-shm-usage", true))
	allocator, cancelAllocator := chromedp.NewExecAllocator(ctx, options...)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()
	var dom, ssr string
	var screenshot []byte
	err := chromedp.Run(browser, chromedp.Navigate(os.Getenv("ORIGIN")+"/"), chromedp.WaitReady(`body[data-acceptance="passed"]`, chromedp.ByQuery), chromedp.Text("#ssr", &ssr, chromedp.ByQuery))
	if err != nil {
		return fmt.Errorf("browser initial verification failed: %w", err)
	}
	if ssr != "SSR: fresh" {
		return fmt.Errorf("browser SSR data was not isolated: %q", ssr)
	}
	err = chromedp.Run(browser, chromedp.Click("#save", chromedp.ByQuery), chromedp.Poll(`document.querySelector('#result').textContent.includes('API: browser-mutated')`, nil), chromedp.Reload(), chromedp.WaitReady(`body[data-acceptance="passed"]`, chromedp.ByQuery), chromedp.Text("#ssr", &ssr, chromedp.ByQuery), chromedp.OuterHTML("html", &dom, chromedp.ByQuery), chromedp.FullScreenshot(&screenshot, 100))
	if dom != "" {
		if writeErr := os.WriteFile("reports/browser.html", []byte(dom), 0600); writeErr != nil {
			return writeErr
		}
	}
	if len(screenshot) > 0 {
		if writeErr := os.WriteFile("reports/browser.png", screenshot, 0600); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return fmt.Errorf("browser verification failed: %w", err)
	}
	if ssr != "SSR: browser-mutated" {
		return fmt.Errorf("browser mutation did not survive reload: %q", ssr)
	}
	fmt.Println("TestBrowser: trusted HTTPS, rendered SSR, UI mutation/reload, browser API and variable WebSocket echo verified")
	return nil
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("expected prepare, api, web, build, backend-test, ui-test, or client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	switch os.Args[1] {
	case "fail":
		return errors.New("requested preparation failure")
	case "prepare":
		return prepare(ctx)
	case "api", "web":
		handler := api()
		if os.Args[1] == "web" {
			handler = web()
		}
		server := &http.Server{Addr: net.JoinHostPort("0.0.0.0", os.Getenv("PORT")), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		fmt.Println(os.Args[1] + " listener starting")
		return server.ListenAndServe()
	case "build":
		message, err := os.ReadFile("message.txt")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(os.Getenv("DIST"), 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(os.Getenv("DIST"), "message.txt"), message, 0600)
	case "backend-test", "ui-test":
		if err := os.MkdirAll("reports", 0700); err != nil {
			return err
		}
		// Replace previous application output even when argument validation or
		// cancellation prevents a completed report. Backlot serializes writers.
		if err := os.WriteFile("reports/result.json", []byte(`{"status":"not_completed","passed":false}`), 0600); err != nil {
			return err
		}
		flags := flag.NewFlagSet("fixture tests", flag.ContinueOnError)
		selector := flags.String("run", ".*", "Regular expression selecting TestDatabase or TestBrowser")
		hold := flags.Duration("wait", 0, "Hold the selected test to exercise cancellation and shared outputs")
		fail := flags.Bool("fail", false, "Return a deliberate application failure after collecting evidence")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		pattern, err := regexp.Compile(*selector)
		if err != nil {
			return errors.New("invalid test selector")
		}
		name := "TestDatabase"
		if os.Args[1] == "ui-test" {
			name = "TestBrowser"
		}
		if !pattern.MatchString(name) {
			return errors.New("selector matched no fixture tests")
		}
		started := time.Now().UTC()
		fmt.Println("selected test started")
		if os.Args[1] == "backend-test" {
			err = backendTest(ctx)
		} else {
			err = browserTest(ctx)
		}
		if *hold < 0 || *hold > 24*time.Hour {
			return errors.New("invalid test wait")
		}
		if *hold > 0 {
			select {
			case <-time.After(*hold):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if *fail {
			err = errors.New("requested fixture failure")
		}
		report := struct {
			Test        string    `json:"test"`
			Arguments   []string  `json:"arguments"`
			Passed      bool      `json:"passed"`
			StartedAt   time.Time `json:"started_at"`
			CompletedAt time.Time `json:"completed_at"`
		}{os.Args[1], os.Args[2:], err == nil, started, time.Now().UTC()}
		data, _ := json.MarshalIndent(report, "", "  ")
		if writeErr := os.WriteFile("reports/result.json", data, 0600); writeErr != nil {
			return writeErr
		}
		return err
	case "client":
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Get(os.Getenv("ORIGIN") + "/redirect")
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		cookies := response.Cookies()
		if response.StatusCode != 302 || response.Header.Get("Location") != os.Getenv("ORIGIN")+"/" || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
			return errors.New("canonical redirect/cookie failed")
		}
		response, err = client.Get(os.Getenv("ORIGIN") + "/")
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 200 || !bytes.Contains(body, []byte("SSR: ")) {
			return errors.New("container SSR failed")
		}
		connection, _, err := websocket.Dial(ctx, os.Getenv("ORIGIN")+"/ws", nil)
		if err != nil {
			return err
		}
		defer func() { _ = connection.CloseNow() }()
		payload := []byte(fmt.Sprintf("container-%d", time.Now().UnixNano()))
		if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
			return err
		}
		kind, echo, err := connection.Read(ctx)
		if err != nil || kind != websocket.MessageText || !bytes.Equal(payload, echo) {
			return errors.New("container WebSocket echo failed")
		}
		fmt.Println("container trusted HTTPS SSR/API/WebSocket/redirect/cookie verified")
		return nil
	default:
		return errors.New("unknown fixture command")
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
