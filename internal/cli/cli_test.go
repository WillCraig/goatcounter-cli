package cli

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/WillCraig/goatcounter-cli/internal/config"
)

func TestHelpAndUsage(t *testing.T) {
	code, out, errOut := run(t, nil, "--help")
	if code != 0 || !strings.Contains(out, "Exit codes:") || !strings.Contains(out, "GOATCOUNTER_TOKEN") {
		t.Fatalf("help code=%d out=%s err=%s", code, out, errOut)
	}
	code, out, errOut = run(t, nil, "help")
	if code != 0 || !strings.Contains(out, "site create") {
		t.Fatalf("help command code=%d out=%s err=%s", code, out, errOut)
	}
	code, out, _ = run(t, nil, "--version")
	if code != 0 || strings.TrimSpace(out) != Version {
		t.Fatalf("version code=%d out=%q", code, out)
	}
	code, _, errOut = run(t, nil)
	if code != exitUsage || !strings.Contains(errOut, "missing command") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	code, _, errOut = run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "nope")
	if code != exitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	code, _, errOut = run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--token", "abc", "sites")
	if code != exitUsage {
		t.Fatalf("token flag should be rejected, code=%d err=%s", code, errOut)
	}
}

func TestSnippet(t *testing.T) {
	code, out, errOut := run(t, nil, "snippet", "blog")
	if code != 0 {
		t.Fatal(errOut)
	}
	if !strings.Contains(out, `data-goatcounter="https://blog.goatcounter.com/count"`) || !strings.Contains(out, `src="//gc.zgo.at/count.js"`) {
		t.Fatalf("snippet:\n%s", out)
	}

	code, out, errOut = run(t, nil, "--json", "--url", "https://stats.example.com", "snippet", "blog")
	if code != 0 {
		t.Fatal(errOut)
	}
	var doc map[string]string
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["count_url"] != "https://stats.example.com/count" || !strings.Contains(doc["script"], "https://stats.example.com/count.js") {
		t.Fatalf("doc %+v", doc)
	}
	if strings.Contains(doc["script"], "gc.zgo.at") {
		t.Fatal("self-hosted snippet should not use gc.zgo.at")
	}

	code, _, errOut = run(t, nil, "snippet", "Bad")
	if code != exitUsage {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
}

func TestSitesJSON(t *testing.T) {
	const token = "sites-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.RequestURI(), token) {
			t.Error("token in URL")
		}
		w.Header().Set("Content-Type", "application/json")
		ioWrite(w, `{"sites":[{"id":1,"code":"blog","cname":"stats.example.com","link_domain":"https://example.com"}]}`)
	}))
	defer srv.Close()

	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=" + token}, "--json", "--url", srv.URL, "sites")
	if code != 0 {
		t.Fatal(errOut)
	}
	if strings.Contains(out, token) || strings.Contains(errOut, token) {
		t.Fatal("token leaked into output")
	}
	var doc struct {
		Sites []struct {
			ID        int64  `json:"id"`
			Code      string `json:"code"`
			Dashboard string `json:"dashboard"`
		} `json:"sites"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Sites) != 1 || doc.Sites[0].Dashboard != "https://stats.example.com" {
		t.Fatalf("doc %+v", doc)
	}
}

func TestMissingToken(t *testing.T) {
	code, _, errOut := run(t, nil, "--json", "--url", "https://blog.goatcounter.com", "sites")
	if code != exitAuth {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(errOut), &doc); err != nil {
		t.Fatal(errOut)
	}
	if doc["exit_code"] != float64(exitAuth) {
		t.Fatalf("doc %+v", doc)
	}
	if !strings.Contains(doc["error"].(string), "GOATCOUNTER_TOKEN") {
		t.Fatalf("doc %+v", doc)
	}
}

func TestRedactTokenFromAPIError(t *testing.T) {
	const token = "leak-this-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		ioWrite(w, `{"error":"bad `+token+`"}`)
	}))
	defer srv.Close()
	code, _, errOut := run(t, []string{"GOATCOUNTER_TOKEN=" + token}, "--url", srv.URL, "sites")
	if code != exitAPI {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if strings.Contains(errOut, token) {
		t.Fatalf("token leaked: %s", errOut)
	}
	if !strings.Contains(errOut, "[redacted]") {
		t.Fatalf("err %s", errOut)
	}
}

func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		ioWrite(w, `{"error":"unknown token"}`)
	}))
	defer srv.Close()
	code, _, errOut := run(t, []string{"GOATCOUNTER_TOKEN=gc-test-token-401"}, "--url", srv.URL, "sites")
	if code != exitAuth || !strings.Contains(errOut, "unknown token") {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
}

func TestSiteCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v0/sites" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "blog" || body["link_domain"] != "https://example.com" {
			t.Errorf("body %+v", body)
		}
		ioWrite(w, `{"id":4,"code":"blog","link_domain":"https://example.com"}`)
	}))
	defer srv.Close()
	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--json", "--url", srv.URL, "site", "create", "blog", "--link-domain", "https://example.com")
	if code != 0 {
		t.Fatal(errOut)
	}
	if !strings.Contains(out, `"dashboard": "`+srv.URL+`"`) {
		t.Fatalf("out %s", out)
	}
}

func TestDashboardURL(t *testing.T) {
	if got := dashboardURL("https://old.goatcounter.com", "blog", nil); got != "https://blog.goatcounter.com" {
		t.Fatalf("hosted: %s", got)
	}
	cname := "stats.example.com"
	if got := dashboardURL("https://old.goatcounter.com", "blog", &cname); got != "https://stats.example.com" {
		t.Fatalf("cname: %s", got)
	}
	if got := dashboardURL("https://stats.example.com", "blog", nil); got != "https://stats.example.com" {
		t.Fatalf("self-hosted: %s", got)
	}
	if isGoatcounterHost("blog.goatcounter.com.evil.com") || isGoatcounterHost("notgoatcounter.com") {
		t.Fatal("host check is too broad")
	}
}

func TestStatsFlagsAfterCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/v0/stats/total":
			if q.Get("start") != "2026-11-01T00:00:00Z" || q.Get("end") != "2026-11-08T23:59:59Z" {
				t.Errorf("range %s", r.URL.RawQuery)
			}
			ioWrite(w, `{"total":5,"total_events":1,"total_utc":5}`)
		case "/api/v0/stats/hits":
			if q.Get("limit") != "2" {
				t.Errorf("limit %s", q.Get("limit"))
			}
			ioWrite(w, `{"hits":[{"count":4,"path":"/","title":"Home"},{"count":1,"path":"signup","event":true}],"more":false}`)
		case "/api/v0/stats/toprefs":
			ioWrite(w, `{"stats":[{"name":"Google","count":3,"ref_scheme":"g"}],"more":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "stats", "blog", "--from", "2026-11-01", "--to", "2026-11-08", "--top", "2", "--url", srv.URL)
	if code != 0 {
		t.Fatal(errOut)
	}
	if !strings.Contains(out, "visitors: 5") || !strings.Contains(out, "(event)") || !strings.Contains(out, "Google") {
		t.Fatalf("out:\n%s", out)
	}

	code, out, errOut = run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--json", "--url", srv.URL, "stats", "blog", "--from", "2026-11-01", "--to", "2026-11-08", "--top", "2")
	if code != 0 {
		t.Fatal(errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["total"] != float64(5) || doc["code"] != "blog" {
		t.Fatalf("doc %+v", doc)
	}
}

func TestExportRoundTrip(t *testing.T) {
	const csv = "2,Path,Title\n/,Home\n"
	exportPollInterval = 0
	exportPollTimeout = 5 * timeSecond
	t.Cleanup(func() {
		exportPollInterval = timeSecond
		exportPollTimeout = 5 * 60 * timeSecond
	})

	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v0/export":
			w.WriteHeader(http.StatusAccepted)
			ioWrite(w, `{"id":4,"finished_at":null}`)
		case r.URL.Path == "/api/v0/export/4":
			polls++
			if polls < 2 {
				ioWrite(w, `{"id":4,"finished_at":null}`)
				return
			}
			ioWrite(w, `{"id":4,"finished_at":"2026-11-13T01:00:00Z","num_rows":1}`)
		case r.URL.Path == "/api/v0/export/4/download":
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write([]byte(csv))
			zw.Close()
			w.Header().Set("Content-Type", "application/gzip")
			w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	outPath := filepath.Join(dir, "hits.csv")
	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--json", "--url", srv.URL, "export", "blog", "--out", outPath)
	if code != 0 {
		t.Fatal(errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(out)
	}
	if doc["path"] != outPath || doc["bytes"] != float64(len(csv)) || doc["num_rows"] != float64(1) {
		t.Fatalf("doc %+v", doc)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != csv {
		t.Fatalf("file %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", info.Mode().Perm())
		}
	}
	if strings.Contains(out+errOut, "tok") && strings.Contains(out+errOut, "GOATCOUNTER") {
		// token value "tok" is too short to assert absence safely
	}
}

func TestExportStdout(t *testing.T) {
	exportPollInterval = 0
	t.Cleanup(func() { exportPollInterval = timeSecond })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			ioWrite(w, `{"id":1,"finished_at":"2026-11-13T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, "/download"):
			ioWrite(w, "a,b\n")
		default:
			ioWrite(w, `{"id":1,"finished_at":"2026-11-13T00:00:00Z"}`)
		}
	}))
	defer srv.Close()
	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--url", srv.URL, "export", "blog")
	if code != 0 {
		t.Fatal(errOut)
	}
	if out != "a,b\n" {
		t.Fatalf("stdout %q stderr %s", out, errOut)
	}
	if !strings.Contains(errOut, "export 1 started") {
		t.Fatalf("stderr %s", errOut)
	}
}

func TestLooseConfigRefusesToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	home := t.TempDir()
	path, err := config.Path(runtime.GOOS, func(k string) string {
		if k == "HOME" || k == "APPDATA" {
			return home
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "config-file-secret"
	if err := os.WriteFile(path, []byte("token="+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := run(t, []string{"HOME=" + home, "APPDATA=" + home}, "sites", "--url", "https://blog.goatcounter.com")
	if code != exitAuth {
		t.Fatalf("code=%d err=%s", code, errOut)
	}
	if strings.Contains(errOut, secret) {
		t.Fatal("secret leaked")
	}
}

func TestCLIRefusesCrossHostRedirect(t *testing.T) {
	const token = "gc-test-token-redirect"
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed a redirect to a different host")
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization was forwarded")
		}
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v0/sites", http.StatusFound)
	}))
	defer srv.Close()

	code, out, errOut := run(t, []string{"GOATCOUNTER_TOKEN=" + token}, "--url", srv.URL, "sites")
	if code == 0 || !strings.Contains(errOut, "different host") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, token) {
		t.Fatal("token leaked")
	}
}

func TestHostedStatsUseCodeWhenURLUnset(t *testing.T) {
	var host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		switch r.URL.Path {
		case "/api/v0/stats/total":
			ioWrite(w, `{"total":0,"total_events":0,"total_utc":0}`)
		case "/api/v0/stats/hits":
			ioWrite(w, `{"hits":[],"more":false}`)
		case "/api/v0/stats/toprefs":
			ioWrite(w, `{"stats":[],"more":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	// --url is explicit. Without it, a code would target goatcounter.com and
	// this test would leave the machine. That path is covered by snippet.
	code, _, errOut := run(t, []string{"GOATCOUNTER_TOKEN=tok"}, "--url", srv.URL, "stats", "blog")
	if code != 0 {
		t.Fatal(errOut)
	}
	if host == "" {
		t.Fatal("no request")
	}
}

func TestConfigURLUsedForSites(t *testing.T) {
	home := t.TempDir()
	path, err := config.Path(runtime.GOOS, func(k string) string {
		if k == "HOME" || k == "APPDATA" {
			return home
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer from-env" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		ioWrite(w, `{"sites":[]}`)
	}))
	defer srv.Close()
	body := "token=from-file\nurl=" + srv.URL + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, []string{"HOME=" + home, "APPDATA=" + home, "GOATCOUNTER_TOKEN=from-env"}, "sites")
	if code != 0 {
		t.Fatal(errOut)
	}
	if !strings.Contains(out, "no sites") {
		t.Fatalf("out %q", out)
	}
}

func run(t *testing.T, environ []string, args ...string) (int, string, string) {
	t.Helper()
	if !hasKey(environ, "HOME") && !hasKey(environ, "APPDATA") {
		dir := t.TempDir()
		environ = append([]string{"HOME=" + dir, "APPDATA=" + dir}, environ...)
	}
	var stdout, stderr bytes.Buffer
	code := Run(args, environ, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func hasKey(environ []string, key string) bool {
	prefix := key + "="
	for _, e := range environ {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func ioWrite(w http.ResponseWriter, s string) {
	_, _ = w.Write([]byte(s))
}

const timeSecond = 1_000_000_000
