package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListSitesAuthAndURL(t *testing.T) {
	const token = "tok-list-sites"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != "/api/v0/sites" {
			t.Errorf("url %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.String(), token) {
			t.Error("token leaked into the URL")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"sites":[{"id":3,"code":"blog","link_domain":"https://blog.example"}]}`)
	}))
	defer srv.Close()

	sites, err := (&Client{HTTP: srv.Client()}).ListSites(context.Background(), srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].Code != "blog" || sites[0].ID != 3 {
		t.Fatalf("sites: %+v", sites)
	}
}

func TestCreateSite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "blog" || body["link_domain"] != "https://example.com" {
			t.Fatalf("body: %+v", body)
		}
		if _, ok := body["token"]; ok {
			t.Fatal("token in body")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":9,"code":"blog","link_domain":"https://example.com"}`)
	}))
	defer srv.Close()

	site, err := (&Client{HTTP: srv.Client()}).CreateSite(context.Background(), srv.URL, "tok", "blog", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if site.ID != 9 {
		t.Fatalf("site: %+v", site)
	}
}

func TestStatsQuery(t *testing.T) {
	const token = "tok-stats"
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		seen[r.URL.Path] = true
		q := r.URL.Query()
		if q.Get("start") != "2026-11-01T00:00:00Z" || q.Get("end") != "2026-11-07T23:59:59Z" {
			t.Errorf("%s query %s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v0/stats/total":
			io.WriteString(w, `{"total":12,"total_events":2,"total_utc":11}`)
		case "/api/v0/stats/hits":
			if q.Get("limit") != "3" {
				t.Errorf("limit %s", q.Get("limit"))
			}
			io.WriteString(w, `{"hits":[{"count":8,"path_id":1,"path":"/","title":"Home","event":false}],"total":8,"more":true}`)
		case "/api/v0/stats/toprefs":
			io.WriteString(w, `{"stats":[{"name":"Google","count":4,"ref_scheme":"g"}],"more":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client()}
	ctx := context.Background()
	total, err := c.Total(ctx, srv.URL, token, "2026-11-01T00:00:00Z", "2026-11-07T23:59:59Z")
	if err != nil || total.Total != 12 || total.TotalEvents != 2 {
		t.Fatalf("total %+v err %v", total, err)
	}
	hits, err := c.Hits(ctx, srv.URL, token, "2026-11-01T00:00:00Z", "2026-11-07T23:59:59Z", 3)
	if err != nil || len(hits.Hits) != 1 || !hits.More || hits.Hits[0].Path != "/" {
		t.Fatalf("hits %+v err %v", hits, err)
	}
	refs, err := c.TopRefs(ctx, srv.URL, token, "2026-11-01T00:00:00Z", "2026-11-07T23:59:59Z", 3)
	if err != nil || len(refs.Stats) != 1 || refs.Stats[0].Name != "Google" {
		t.Fatalf("refs %+v err %v", refs, err)
	}
	for _, p := range []string{"/api/v0/stats/total", "/api/v0/stats/hits", "/api/v0/stats/toprefs"} {
		if !seen[p] {
			t.Errorf("missing %s", p)
		}
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"errors":{"code":["too short","reserved"]}}`)
	}))
	defer srv.Close()
	_, err := (&Client{HTTP: srv.Client()}).ListSites(context.Background(), srv.URL, "tok")
	if err == nil {
		t.Fatal("expected error")
	}
	var se *StatusError
	if !asStatus(err, &se) || se.Status != 400 {
		t.Fatalf("err %#v", err)
	}
	if !strings.Contains(err.Error(), "code: too short; reserved") {
		t.Fatalf("err %v", err)
	}
}

func TestExportGzip(t *testing.T) {
	const csv = "Path,Title\n/,Home\n"
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v0/export":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["format"] != "csv" {
				t.Errorf("format %q", body["format"])
			}
			w.WriteHeader(http.StatusAccepted)
			io.WriteString(w, `{"id":7,"format":"csv","finished_at":null}`)
		case r.URL.Path == "/api/v0/export/7":
			polls++
			if polls == 1 {
				io.WriteString(w, `{"id":7,"finished_at":null}`)
				return
			}
			io.WriteString(w, `{"id":7,"finished_at":"2026-11-13T00:00:00Z","num_rows":1}`)
		case r.URL.Path == "/api/v0/export/7/download":
			var buf strings.Builder
			zw := gzip.NewWriter(&buf)
			zw.Write([]byte(csv))
			zw.Close()
			w.Header().Set("Content-Type", "application/gzip")
			w.Write([]byte(buf.String()))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client()}
	exp, err := c.StartExport(context.Background(), srv.URL, "tok")
	if err != nil || exp.ID != 7 || exp.Finished() {
		t.Fatalf("start %+v %v", exp, err)
	}
	exp, err = c.GetExport(context.Background(), srv.URL, "tok", 7)
	if err != nil || exp.Finished() {
		t.Fatalf("first poll %+v %v", exp, err)
	}
	exp, err = c.GetExport(context.Background(), srv.URL, "tok", 7)
	if err != nil || !exp.Finished() || exp.NumRows == nil || *exp.NumRows != 1 {
		t.Fatalf("second poll %+v %v", exp, err)
	}
	got, pending, err := c.DownloadExport(context.Background(), srv.URL, "tok", 7)
	if err != nil || pending {
		t.Fatalf("download pending=%v err=%v", pending, err)
	}
	if string(got) != csv {
		t.Fatalf("csv %q", got)
	}
}

func TestDownloadPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		io.WriteString(w, `{"error":"still being generated"}`)
	}))
	defer srv.Close()
	_, pending, err := (&Client{HTTP: srv.Client()}).DownloadExport(context.Background(), srv.URL, "tok", 1)
	if err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}

func TestNormalizeBase(t *testing.T) {
	got, err := NormalizeBase("https://stats.example.com/api/v0/")
	if err != nil || got != "https://stats.example.com" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := NormalizeBase("https://user:secret@stats.example.com"); err == nil {
		t.Fatal("credentials should be rejected")
	}
	if _, err := NormalizeBase("https://stats.example.com/extra"); err == nil {
		t.Fatal("path should be rejected")
	}
	got, err = NormalizeBase("stats.example.com")
	if err != nil || got != "https://stats.example.com" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestRefuseCrossHostRedirect(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed redirect to a different host")
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization forwarded")
		}
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v0/sites", http.StatusFound)
	}))
	defer srv.Close()

	client := srv.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Host != via[0].URL.Host {
			return errString("refusing to follow a redirect to a different host")
		}
		return nil
	}
	_, err := (&Client{HTTP: client}).ListSites(context.Background(), srv.URL, "tok-redirect")
	if err == nil || !strings.Contains(err.Error(), "different host") {
		t.Fatalf("err %v", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func asStatus(err error, dest **StatusError) bool {
	se, ok := err.(*StatusError)
	if ok {
		*dest = se
	}
	return ok
}
