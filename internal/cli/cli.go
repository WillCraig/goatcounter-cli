// Package cli implements the gcli command line.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WillCraig/goatcounter-cli/internal/api"
	"github.com/WillCraig/goatcounter-cli/internal/config"
)

// Version is the release version, overridden by GoReleaser with -X.
var Version = "dev"

// Export polling defaults. Tests shorten them.
var (
	exportPollInterval = api.PollInterval
	exportPollTimeout  = api.PollTimeout
)

const (
	exitOK    = 0
	exitAPI   = 1
	exitUsage = 2
	exitAuth  = 3
)

// Run executes one gcli invocation. environ is the process environment
// ("KEY=value" entries). The token is read only from that environment or the
// config file, never from args.
func Run(args []string, environ []string, stdout, stderr io.Writer) int {
	getenv := func(k string) string {
		prefix := k + "="
		for i := len(environ) - 1; i >= 0; i-- {
			if strings.HasPrefix(environ[i], prefix) {
				return environ[i][len(prefix):]
			}
		}
		return ""
	}
	opts, err := parseArgs(args)
	if err != nil {
		return fail(opts.JSON, "", err, stderr)
	}
	if opts.Help {
		fmt.Fprint(stdout, usageText)
		return exitOK
	}
	if opts.Version {
		fmt.Fprintln(stdout, Version)
		return exitOK
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		return fail(opts.JSON, "", err, stderr)
	}
	opts.ConfigURL = cfg.URL

	switch opts.Command {
	case "snippet":
		err = cmdSnippet(opts, stdout)
	case "sites", "site", "stats", "export":
		if cfg.Token == "" {
			err = authErrf("no API token; set GOATCOUNTER_TOKEN or token= in %s (mode 0600)", cfg.Path)
			break
		}
		client := &api.Client{
			HTTP:      defaultHTTP(),
			UserAgent: "gcli/" + Version,
		}
		switch opts.Command {
		case "sites":
			err = cmdSites(client, opts, cfg.Token, stdout)
		case "site":
			err = cmdSiteCreate(client, opts, cfg.Token, stdout)
		case "stats":
			err = cmdStats(client, opts, cfg.Token, stdout)
		case "export":
			err = cmdExport(client, opts, cfg.Token, stdout, stderr)
		}
	default:
		err = usageErrf("unknown command %q", opts.Command)
	}
	if err != nil {
		return fail(opts.JSON, cfg.Token, err, stderr)
	}
	return exitOK
}

type options struct {
	JSON       bool
	Help       bool
	Version    bool
	URL        string
	ConfigURL  string
	LinkDomain string
	From       string
	To         string
	Top        int
	TopSet     bool
	Out        string
	Command    string
	Args       []string
}

func parseArgs(args []string) (options, error) {
	opts := options{Top: 10}
	flags, pos, err := splitFlags(args)
	if err != nil {
		return opts, err
	}
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		name, value, hasValue := strings.Cut(strings.TrimLeft(f, "-"), "=")
		// TrimLeft strips every leading dash, so --json and -json both work.
		// Require a single or double dash so a bare word is not a flag.
		if !strings.HasPrefix(f, "-") {
			return opts, usageErrf("internal parse error")
		}
		take := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(flags) {
				return "", usageErrf("flag --%s needs a value", name)
			}
			i++
			return flags[i], nil
		}
		switch name {
		case "json":
			opts.JSON, err = parseBoolFlag(name, value, hasValue)
		case "help", "h":
			opts.Help, err = parseBoolFlag(name, value, hasValue)
		case "version":
			opts.Version, err = parseBoolFlag(name, value, hasValue)
		case "url":
			opts.URL, err = take()
		case "link-domain":
			opts.LinkDomain, err = take()
		case "from":
			opts.From, err = take()
		case "to":
			opts.To, err = take()
		case "top":
			var raw string
			raw, err = take()
			if err != nil {
				return opts, err
			}
			opts.Top, err = strconv.Atoi(raw)
			opts.TopSet = true
		case "out":
			opts.Out, err = take()
		default:
			err = usageErrf("unknown flag --%s", name)
		}
		if err != nil {
			return opts, err
		}
	}
	if opts.Help || opts.Version {
		if len(pos) > 0 && !opts.Help {
			return opts, usageErrf("--version does not take arguments")
		}
		return opts, nil
	}
	if len(pos) == 0 {
		return opts, usageErrf("missing command")
	}
	if pos[0] == "help" {
		opts.Help = true
		return opts, nil
	}
	opts.Command = pos[0]
	opts.Args = pos[1:]
	if err := validateCommand(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

func parseBoolFlag(name, value string, hasValue bool) (bool, error) {
	if !hasValue {
		return true, nil
	}
	switch strings.ToLower(value) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, usageErrf("--%s must be true or false", name)
	}
}

func validateCommand(opts options) error {
	switch opts.Command {
	case "sites":
		if len(opts.Args) != 0 {
			return usageErrf("sites takes no arguments")
		}
		if opts.LinkDomain != "" || opts.From != "" || opts.To != "" || opts.TopSet || opts.Out != "" {
			return usageErrf("sites accepts only --json and --url")
		}
	case "site":
		if len(opts.Args) < 1 || opts.Args[0] != "create" {
			return usageErrf("usage: gcli site create <code> [--link-domain URL]")
		}
		if len(opts.Args) != 2 {
			return usageErrf("usage: gcli site create <code> [--link-domain URL]")
		}
		if err := checkCode(opts.Args[1]); err != nil {
			return err
		}
		if opts.LinkDomain != "" {
			if err := checkLink(opts.LinkDomain); err != nil {
				return err
			}
		}
		if opts.From != "" || opts.To != "" || opts.TopSet || opts.Out != "" {
			return usageErrf("site create accepts --json, --url, and --link-domain")
		}
	case "snippet":
		if len(opts.Args) != 1 {
			return usageErrf("usage: gcli snippet <code>")
		}
		if err := checkCode(opts.Args[0]); err != nil {
			return err
		}
		if opts.LinkDomain != "" || opts.From != "" || opts.To != "" || opts.TopSet || opts.Out != "" {
			return usageErrf("snippet accepts only --json and --url")
		}
	case "stats":
		if len(opts.Args) != 1 {
			return usageErrf("usage: gcli stats <code> [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--top N]")
		}
		if err := checkCode(opts.Args[0]); err != nil {
			return err
		}
		if opts.Top < 1 || opts.Top > 100 {
			return usageErrf("--top must be from 1 to 100")
		}
		if _, err := day(opts.From, false); err != nil {
			return err
		}
		if _, err := day(opts.To, true); err != nil {
			return err
		}
		if opts.LinkDomain != "" || opts.Out != "" {
			return usageErrf("stats accepts --json, --url, --from, --to, and --top")
		}
	case "export":
		if len(opts.Args) != 1 {
			return usageErrf("usage: gcli export <code> [--out FILE]")
		}
		if err := checkCode(opts.Args[0]); err != nil {
			return err
		}
		if opts.LinkDomain != "" || opts.From != "" || opts.To != "" || opts.TopSet {
			return usageErrf("export accepts --json, --url, and --out")
		}
	default:
		return usageErrf("unknown command %q", opts.Command)
	}
	return nil
}

func checkCode(code string) error {
	if len(code) < 2 || len(code) > 50 {
		return usageErrf("site code must be 2 to 50 characters")
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(code)-1:
		default:
			return usageErrf("site code %q must be a lowercase hostname label", code)
		}
	}
	return nil
}

func checkLink(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return usageErrf("--link-domain must be an http or https URL")
	}
	return nil
}

// day converts YYYY-MM-DD into the RFC3339 value the API expects.
// endOfDay selects 23:59:59Z. An empty input returns "" and no error.
func day(s string, endOfDay bool) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", usageErrf("date %q must be YYYY-MM-DD", s)
	}
	if endOfDay {
		t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}
	return t.UTC().Format(time.RFC3339), nil
}

func cmdSites(client *api.Client, opts options, token string, stdout io.Writer) error {
	base, err := resolveBase("", opts)
	if err != nil {
		return err
	}
	sites, err := client.ListSites(context.Background(), base, token)
	if err != nil {
		return err
	}
	rows := make([]siteRow, 0, len(sites))
	for _, s := range sites {
		rows = append(rows, siteRowFrom(base, s))
	}
	if opts.JSON {
		return writeJSON(stdout, map[string]any{"sites": rows})
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "no sites")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\n", row.ID, row.Code, row.Dashboard, row.LinkDomain)
	}
	return nil
}

func cmdSiteCreate(client *api.Client, opts options, token string, stdout io.Writer) error {
	code := opts.Args[1]
	base, err := resolveBase("", opts)
	if err != nil {
		return err
	}
	site, err := client.CreateSite(context.Background(), base, token, code, opts.LinkDomain)
	if err != nil {
		return err
	}
	if site.Code == "" {
		site.Code = code
	}
	row := siteRowFrom(base, site)
	if opts.JSON {
		return writeJSON(stdout, map[string]any{"site": row})
	}
	fmt.Fprintf(stdout, "created %s\ndashboard: %s\n", row.Code, row.Dashboard)
	return nil
}

func cmdSnippet(opts options, stdout io.Writer) error {
	code := opts.Args[0]
	base, err := resolveBase(code, opts)
	if err != nil {
		return err
	}
	script, countURL, err := snippetFor(base)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeJSON(stdout, map[string]string{
			"code":      code,
			"count_url": countURL,
			"script":    script,
		})
	}
	fmt.Fprintln(stdout, script)
	return nil
}

func cmdStats(client *api.Client, opts options, token string, stdout io.Writer) error {
	code := opts.Args[0]
	base, err := resolveBase(code, opts)
	if err != nil {
		return err
	}
	start, err := day(opts.From, false)
	if err != nil {
		return err
	}
	end, err := day(opts.To, true)
	if err != nil {
		return err
	}
	ctx := context.Background()
	total, err := client.Total(ctx, base, token, start, end)
	if err != nil {
		return err
	}
	hits, err := client.Hits(ctx, base, token, start, end, opts.Top)
	if err != nil {
		return err
	}
	refs, err := client.TopRefs(ctx, base, token, start, end, opts.Top)
	if err != nil {
		return err
	}
	pages := hits.Hits
	if pages == nil {
		pages = []api.Hit{}
	}
	referrers := refs.Stats
	if referrers == nil {
		referrers = []api.Ref{}
	}
	out := statsOut{
		Code:          code,
		URL:           base,
		From:          opts.From,
		To:            opts.To,
		Total:         total.Total,
		TotalEvents:   total.TotalEvents,
		TotalUTC:      total.TotalUTC,
		Pages:         pages,
		MorePages:     hits.More,
		Referrers:     referrers,
		MoreReferrers: refs.More,
	}
	if opts.JSON {
		return writeJSON(stdout, out)
	}
	fmt.Fprintf(stdout, "%s  %s\n", code, base)
	if opts.From != "" || opts.To != "" {
		fmt.Fprintf(stdout, "%s .. %s\n", blank(opts.From), blank(opts.To))
	}
	fmt.Fprintf(stdout, "visitors: %d\nevents: %d\n\npages\n", out.Total, out.TotalEvents)
	if len(pages) == 0 {
		fmt.Fprintln(stdout, "  (none)")
	}
	for _, p := range pages {
		label := p.Path
		if p.Title != "" {
			label += "  " + p.Title
		}
		if p.Event {
			label += "  (event)"
		}
		fmt.Fprintf(stdout, "  %7d  %s\n", p.Count, label)
	}
	if hits.More {
		fmt.Fprintln(stdout, "  more pages exist; raise --top")
	}
	fmt.Fprintln(stdout, "\nreferrers")
	if len(referrers) == 0 {
		fmt.Fprintln(stdout, "  (none)")
	}
	for _, r := range referrers {
		fmt.Fprintf(stdout, "  %7d  %s\n", r.Count, r.Name)
	}
	if refs.More {
		fmt.Fprintln(stdout, "  more referrers exist; raise --top")
	}
	return nil
}

func cmdExport(client *api.Client, opts options, token string, stdout, stderr io.Writer) error {
	code := opts.Args[0]
	base, err := resolveBase(code, opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportPollTimeout+2*time.Minute)
	defer cancel()
	exp, err := client.StartExport(ctx, base, token)
	if err != nil {
		return err
	}
	if exp.ID == 0 {
		return fmt.Errorf("export response did not include an id")
	}
	if !opts.JSON {
		fmt.Fprintf(stderr, "export %d started\n", exp.ID)
	}
	deadline := time.Now().Add(exportPollTimeout)
	var pendingNoted bool
	for !exp.Finished() {
		if msg := exp.Failed(); msg != "" {
			return fmt.Errorf("export %d: %s", exp.ID, msg)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("export %d still running after %s", exp.ID, exportPollTimeout)
		}
		if !opts.JSON && !pendingNoted {
			fmt.Fprintf(stderr, "waiting for export %d\n", exp.ID)
			pendingNoted = true
		}
		if err := sleep(ctx, exportPollInterval); err != nil {
			return err
		}
		exp, err = client.GetExport(ctx, base, token, exp.ID)
		if err != nil {
			return err
		}
	}
	if msg := exp.Failed(); msg != "" {
		return fmt.Errorf("export %d: %s", exp.ID, msg)
	}
	var csv []byte
	for {
		var pending bool
		csv, pending, err = client.DownloadExport(ctx, base, token, exp.ID)
		if err != nil {
			return err
		}
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("export %d still running after %s", exp.ID, exportPollTimeout)
		}
		if err := sleep(ctx, exportPollInterval); err != nil {
			return err
		}
	}
	result := map[string]any{
		"id":     exp.ID,
		"code":   code,
		"format": "csv",
		"bytes":  len(csv),
	}
	if exp.NumRows != nil {
		result["num_rows"] = *exp.NumRows
	}
	if opts.Out != "" {
		if err := os.WriteFile(opts.Out, csv, 0o600); err != nil {
			return err
		}
		result["path"] = opts.Out
		if opts.JSON {
			return writeJSON(stdout, result)
		}
		fmt.Fprintf(stdout, "%s\n", opts.Out)
		fmt.Fprintf(stderr, "wrote %d bytes to %s\n", len(csv), opts.Out)
		return nil
	}
	if opts.JSON {
		result["csv"] = string(csv)
		return writeJSON(stdout, result)
	}
	if _, err := stdout.Write(csv); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %d bytes\n", len(csv))
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type siteRow struct {
	ID         int64   `json:"id"`
	Code       string  `json:"code"`
	Cname      *string `json:"cname"`
	LinkDomain string  `json:"link_domain"`
	Dashboard  string  `json:"dashboard"`
}

func siteRowFrom(base string, s api.Site) siteRow {
	return siteRow{
		ID:         s.ID,
		Code:       s.Code,
		Cname:      s.Cname,
		LinkDomain: s.LinkDomain,
		Dashboard:  dashboardURL(base, s.Code, s.Cname),
	}
}

type statsOut struct {
	Code          string    `json:"code"`
	URL           string    `json:"url"`
	From          string    `json:"from,omitempty"`
	To            string    `json:"to,omitempty"`
	Total         int       `json:"total"`
	TotalEvents   int       `json:"total_events"`
	TotalUTC      int       `json:"total_utc"`
	Pages         []api.Hit `json:"pages"`
	MorePages     bool      `json:"more_pages"`
	Referrers     []api.Ref `json:"referrers"`
	MoreReferrers bool      `json:"more_referrers"`
}

func dashboardURL(apiBase, code string, cname *string) string {
	if cname != nil && *cname != "" {
		if strings.Contains(*cname, "://") {
			return *cname
		}
		return "https://" + *cname
	}
	if code != "" && isGoatcounterHost(hostOf(apiBase)) {
		return "https://" + code + ".goatcounter.com"
	}
	return strings.TrimRight(apiBase, "/")
}

func snippetFor(base string) (script, countURL string, err error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", "", err
	}
	countURL = strings.TrimRight(base, "/") + "/count"
	src := strings.TrimRight(base, "/") + "/count.js"
	if isGoatcounterHost(u.Host) {
		src = "//gc.zgo.at/count.js"
	}
	script = "<script data-goatcounter=\"" + countURL + "\"\n        async src=\"" + src + "\"></script>"
	return script, countURL, nil
}

// resolveBase picks the API origin.
// --url wins, then the config file url, then https://<code>.goatcounter.com.
// sites and site create have no code, so they need --url or a config url.
func resolveBase(code string, opts options) (string, error) {
	raw := opts.URL
	if raw == "" {
		raw = opts.ConfigURL
	}
	if raw == "" {
		if code == "" {
			return "", usageErrf("set --url or url= in the config file (an existing site, or the self-hosted origin)")
		}
		return "https://" + code + ".goatcounter.com", nil
	}
	base, err := api.NormalizeBase(raw)
	if err != nil {
		return "", usageErrf("%s", err.Error())
	}
	return base, nil
}

func isGoatcounterHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == "goatcounter.com" || strings.HasSuffix(host, ".goatcounter.com")
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func blank(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeJSON(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usageErrf(format string, args ...any) error {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

type authError struct{ msg string }

func (e authError) Error() string { return e.msg }

func authErrf(format string, args ...any) error {
	return authError{msg: fmt.Sprintf(format, args...)}
}

func fail(asJSON bool, token string, err error, stderr io.Writer) int {
	msg := redact(err.Error(), token)
	code := exitAPI
	var u usageError
	var a authError
	var se *api.StatusError
	switch {
	case errors.As(err, &u):
		code = exitUsage
		msg = redact(u.msg, token)
	case errors.As(err, &a):
		code = exitAuth
		msg = redact(a.msg, token)
	case errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden):
		code = exitAuth
	}
	if strings.Contains(err.Error(), "readable by group or other") || strings.Contains(err.Error(), "no API token") {
		code = exitAuth
	}
	if asJSON {
		_ = writeJSON(stderr, map[string]any{"error": msg, "exit_code": code})
		return code
	}
	fmt.Fprintln(stderr, "gcli: "+msg)
	if code == exitUsage {
		fmt.Fprintln(stderr, "Run gcli --help for usage.")
	}
	return code
}

func redact(msg, token string) string {
	// GoatCounter tokens are longer than common words. A short value would
	// erase itself out of words such as "token" in an error message.
	if len(token) < 8 || msg == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "[redacted]")
}

// valueFlags are flags that consume the next argument.
var valueFlags = map[string]bool{
	"url": true, "link-domain": true, "from": true, "to": true, "top": true, "out": true,
}

// splitFlags pulls flags out from anywhere on the command line so
// `gcli stats code --from 2026-01-01` and `gcli --json stats code` both work.
func splitFlags(args []string) (flags, positionals []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if hasValue || !valueFlags[name] {
			continue
		}
		if i+1 >= len(args) {
			return nil, nil, usageErrf("flag --%s needs a value", name)
		}
		i++
		flags = append(flags, args[i])
	}
	return flags, positionals, nil
}

func defaultHTTP() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return errors.New("refusing to follow a redirect to a different host")
			}
			return nil
		},
	}
}

const usageText = `gcli is a GoatCounter API client.

Usage:
  gcli [--json] [--url URL] <command> [arguments]

Commands:
  sites
      List sites on the account. Requires --url or url= in the config file,
      because the API is served by an existing site (or a self-hosted origin).

  site create <code> [--link-domain URL]
      Create a site on goatcounter.com. <code> becomes <code>.goatcounter.com.
      --link-domain is the site you are tracking, for example https://example.com.

  snippet <code>
      Print the count.js script tag. No token and no network call.
      On goatcounter.com the script is loaded from gc.zgo.at.
      With a self-hosted --url, the script is loaded from that origin.

  stats <code> [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--top N]
      Total visitors, top pages, and top referrers.
      Dates are inclusive UTC days. Omitted dates use the API default
      (about the last 7 days). --top is 1 to 100 and defaults to 10.

  export <code> [--out FILE]
      Start a CSV export, wait until it is ready, and download it.
      Without --out the CSV is written to stdout. With --out the file mode is 0600.
      GoatCounter allows a new export about once an hour, and only stores
      individual pageviews when that setting is enabled.

Global flags:
  --json    Print one JSON document on stdout. Errors are JSON on stderr.
  --url     API origin. Use this for a self-hosted GoatCounter, or pass an
            existing https://<code>.goatcounter.com when listing or creating sites.
            A trailing /api/v0 is accepted and removed.
  --help    Show this text.
  --version Print the version.

The API token is GOATCOUNTER_TOKEN, or token= in the config file:
  ~/.config/goatcounter/config
  %APPDATA%\goatcounter\config
The file must be mode 0600 on Unix. Optional url= in that file is used when
--url is not set. Do not put the token in arguments, URLs, or the command line.

Host selection:
  --url, then url= in the config file, then https://<code>.goatcounter.com.
  On goatcounter.com with more than one site, leave url unset and pass --url
  only for "sites" and "site create". A config url is for a self-hosted origin
  or a single site you always call.

Exit codes:
  0  success
  1  the API request failed, or the network failed
  2  the command line is wrong
  3  no token, the config file is readable by others, or the API returned 401 or 403
`
