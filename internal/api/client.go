// Package api is a small GoatCounter API v0 client.
package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultUserAgent = "gcli"

// StatusError is an HTTP response that is not a successful API call.
type StatusError struct {
	Status int
	Method string
	Path   string
	Msg    string
}

func (e *StatusError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("%s %s: %s", e.Method, e.Path, e.Msg)
}

// Site is the subset of goatcounter.Site this CLI uses.
type Site struct {
	ID         int64   `json:"id"`
	Parent     *int64  `json:"parent"`
	Code       string  `json:"code"`
	Cname      *string `json:"cname"`
	LinkDomain string  `json:"link_domain"`
}

// Hit is one row from /api/v0/stats/hits.
type Hit struct {
	Count  int    `json:"count"`
	PathID int64  `json:"path_id"`
	Path   string `json:"path"`
	Event  bool   `json:"event"`
	Title  string `json:"title"`
}

// Ref is one row from /api/v0/stats/toprefs.
type Ref struct {
	Name      string `json:"name"`
	Count     int    `json:"count"`
	RefScheme string `json:"ref_scheme"`
}

// Total is the /api/v0/stats/total payload the CLI keeps.
type Total struct {
	Total       int `json:"total"`
	TotalEvents int `json:"total_events"`
	TotalUTC    int `json:"total_utc"`
}

// HitsPage is the /api/v0/stats/hits payload the CLI keeps.
type HitsPage struct {
	Hits  []Hit `json:"hits"`
	Total int   `json:"total"`
	More  bool  `json:"more"`
}

// RefsPage is the /api/v0/stats/toprefs payload the CLI keeps.
type RefsPage struct {
	Stats []Ref `json:"stats"`
	More  bool  `json:"more"`
}

// Export is the export object returned by the API.
type Export struct {
	ID         int64   `json:"id"`
	Format     string  `json:"format"`
	FinishedAt *string `json:"finished_at"`
	Error      *string `json:"error"`
	NumRows    *int    `json:"num_rows"`
	LastHitID  *int64  `json:"last_hit_id"`
}

// Client talks to one GoatCounter API base URL per call.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return defaultUserAgent
}

// ListSites calls GET /api/v0/sites.
func (c *Client) ListSites(ctx context.Context, base, token string) ([]Site, error) {
	var out struct {
		Sites []Site `json:"sites"`
	}
	if err := c.getJSON(ctx, base, token, "/api/v0/sites", nil, &out); err != nil {
		return nil, err
	}
	if out.Sites == nil {
		out.Sites = []Site{}
	}
	return out.Sites, nil
}

// CreateSite calls PUT /api/v0/sites.
func (c *Client) CreateSite(ctx context.Context, base, token, code, linkDomain string) (Site, error) {
	body := map[string]string{"code": code}
	if linkDomain != "" {
		body["link_domain"] = linkDomain
	}
	var site Site
	if err := c.sendJSON(ctx, http.MethodPut, base, token, "/api/v0/sites", nil, body, &site, http.StatusOK); err != nil {
		return Site{}, err
	}
	return site, nil
}

// Total calls GET /api/v0/stats/total.
func (c *Client) Total(ctx context.Context, base, token, start, end string) (Total, error) {
	q := url.Values{}
	setRange(q, start, end)
	var out Total
	if err := c.getJSON(ctx, base, token, "/api/v0/stats/total", q, &out); err != nil {
		return Total{}, err
	}
	return out, nil
}

// Hits calls GET /api/v0/stats/hits.
func (c *Client) Hits(ctx context.Context, base, token, start, end string, limit int) (HitsPage, error) {
	q := url.Values{}
	setRange(q, start, end)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out HitsPage
	if err := c.getJSON(ctx, base, token, "/api/v0/stats/hits", q, &out); err != nil {
		return HitsPage{}, err
	}
	if out.Hits == nil {
		out.Hits = []Hit{}
	}
	return out, nil
}

// TopRefs calls GET /api/v0/stats/toprefs.
func (c *Client) TopRefs(ctx context.Context, base, token, start, end string, limit int) (RefsPage, error) {
	q := url.Values{}
	setRange(q, start, end)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out RefsPage
	if err := c.getJSON(ctx, base, token, "/api/v0/stats/toprefs", q, &out); err != nil {
		return RefsPage{}, err
	}
	if out.Stats == nil {
		out.Stats = []Ref{}
	}
	return out, nil
}

// StartExport calls POST /api/v0/export with format csv.
func (c *Client) StartExport(ctx context.Context, base, token string) (Export, error) {
	var out Export
	err := c.sendJSON(ctx, http.MethodPost, base, token, "/api/v0/export", nil, map[string]string{"format": "csv"}, &out, http.StatusAccepted, http.StatusOK)
	if err != nil {
		return Export{}, err
	}
	return out, nil
}

// GetExport calls GET /api/v0/export/{id}.
func (c *Client) GetExport(ctx context.Context, base, token string, id int64) (Export, error) {
	var out Export
	path := "/api/v0/export/" + strconv.FormatInt(id, 10)
	if err := c.getJSON(ctx, base, token, path, nil, &out); err != nil {
		return Export{}, err
	}
	return out, nil
}

// DownloadExport calls GET /api/v0/export/{id}/download.
// A 202 means the file is still being generated. On 200 the body is CSV,
// gunzipped when the payload is gzip (GoatCounter serves the file that way).
func (c *Client) DownloadExport(ctx context.Context, base, token string, id int64) (csv []byte, pending bool, err error) {
	path := "/api/v0/export/" + strconv.FormatInt(id, 10) + "/download"
	status, body, ctype, err := c.do(ctx, http.MethodGet, base, token, path, nil)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusAccepted {
		return nil, true, nil
	}
	if status != http.StatusOK {
		return nil, false, &StatusError{Status: status, Method: http.MethodGet, Path: path, Msg: formatAPIError(status, body)}
	}
	plain, err := maybeGunzip(body, ctype)
	if err != nil {
		return nil, false, fmt.Errorf("export %d: %w", id, err)
	}
	return plain, false, nil
}

func setRange(q url.Values, start, end string) {
	if start != "" {
		q.Set("start", start)
	}
	if end != "" {
		q.Set("end", end)
	}
}

func (c *Client) getJSON(ctx context.Context, base, token, path string, q url.Values, dest any) error {
	return c.sendJSON(ctx, http.MethodGet, base, token, path, q, nil, dest, http.StatusOK)
}

func (c *Client) sendJSON(ctx context.Context, method, base, token, path string, q url.Values, body any, dest any, ok ...int) error {
	status, raw, _, err := c.do(ctx, method, base, token, pathWithQuery(path, q), body)
	if err != nil {
		return err
	}
	for _, want := range ok {
		if status == want {
			if dest == nil || len(raw) == 0 {
				return nil
			}
			if err := json.Unmarshal(raw, dest); err != nil {
				return fmt.Errorf("%s %s: invalid JSON: %w", method, path, err)
			}
			return nil
		}
	}
	return &StatusError{Status: status, Method: method, Path: path, Msg: formatAPIError(status, raw)}
}

func pathWithQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

func (c *Client) do(ctx context.Context, method, base, token, pathQuery string, body any) (int, []byte, string, error) {
	endpoint, err := endpointURL(base, pathQuery)
	if err != nil {
		return 0, nil, "", err
	}
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, "", err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	const maxBody = 32 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, nil, "", err
	}
	if len(raw) > maxBody {
		return 0, nil, "", fmt.Errorf("%s %s: response is larger than %d bytes", method, pathQuery, maxBody)
	}
	return resp.StatusCode, raw, resp.Header.Get("Content-Type"), nil
}

func endpointURL(base, pathQuery string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("api url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("api url %q must be http or https", base)
	}
	if u.Host == "" {
		return "", fmt.Errorf("api url %q has no host", base)
	}
	if u.User != nil {
		return "", errors.New("api url must not contain credentials")
	}
	ref, err := url.Parse(pathQuery)
	if err != nil {
		return "", err
	}
	return u.ResolveReference(ref).String(), nil
}

func formatAPIError(status int, body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	var ae struct {
		Error  string              `json:"error"`
		Errors map[string][]string `json:"errors"`
	}
	if json.Unmarshal(body, &ae) == nil && (ae.Error != "" || len(ae.Errors) > 0) {
		if ae.Error != "" {
			return fmt.Sprintf("HTTP %d: %s", status, ae.Error)
		}
		parts := make([]string, 0, len(ae.Errors))
		keys := make([]string, 0, len(ae.Errors))
		for k := range ae.Errors {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			parts = append(parts, k+": "+strings.Join(ae.Errors[k], "; "))
		}
		return fmt.Sprintf("HTTP %d: %s", status, strings.Join(parts, "; "))
	}
	msg := string(body)
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return fmt.Sprintf("HTTP %d: %s", status, msg)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func maybeGunzip(body []byte, contentType string) ([]byte, error) {
	gzipType := strings.Contains(strings.ToLower(contentType), "gzip")
	gzipMagic := len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b
	if !gzipType && !gzipMagic {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer zr.Close()
	const maxBody = 32 << 20
	out, err := io.ReadAll(io.LimitReader(zr, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if len(out) > maxBody {
		return nil, fmt.Errorf("gzip: decompressed export is larger than %d bytes", maxBody)
	}
	return out, nil
}

// Finished reports whether the export has a non-empty finished_at.
func (e Export) Finished() bool {
	return e.FinishedAt != nil && strings.TrimSpace(*e.FinishedAt) != "" && !strings.EqualFold(*e.FinishedAt, "null")
}

// Failed reports an export that finished with an error string.
func (e Export) Failed() string {
	if e.Error == nil {
		return ""
	}
	return strings.TrimSpace(*e.Error)
}

// NormalizeBase cleans a user-supplied API base URL.
// A trailing /api/v0 is removed so callers can pass either form.
func NormalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty api url")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("api url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("api url %q must be http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("api url %q has no host", raw)
	}
	if u.User != nil {
		return "", errors.New("api url must not contain credentials")
	}
	u.Fragment = ""
	u.RawQuery = ""
	path := strings.TrimRight(u.Path, "/")
	if path == "/api/v0" || path == "/api" {
		path = ""
	}
	if path != "" {
		return "", fmt.Errorf("api url must be the site origin, not %q", u.Path)
	}
	u.Path = ""
	return u.String(), nil
}

// PollInterval is the default pause between export status checks.
const PollInterval = time.Second

// PollTimeout is how long an export may stay unfinished before giving up.
const PollTimeout = 5 * time.Minute
