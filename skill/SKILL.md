---
name: goatcounter
description: >
  Set up GoatCounter analytics on a website repository and read stats with the
  gcli CLI. Use when adding analytics, creating a GoatCounter site, inserting
  the count.js snippet, updating a privacy page, or pulling weekly stats.
  Always call gcli with --json. The API token stays in GOATCOUNTER_TOKEN or
  the config file, never in arguments, commits, logs, or chat.
---

# GoatCounter

Use the `gcli` binary from `github.com/WillCraig/goatcounter-cli`. Pass `--json` on every command that returns data. Do not call the GoatCounter API yourself, and do not invent counts.

The token is `GOATCOUNTER_TOKEN`, or `token=` in `~/.config/goatcounter/config` (`%APPDATA%\goatcounter\config` on Windows, mode `0600`). Never pass a token as a flag, put one in a URL, or write one into the repo. If a command exits 3, the token is missing, the config file is readable by others, or the API rejected it. Say so and stop. Do not ask the user to paste the token into the chat.

`--url` is the API origin. On goatcounter.com, `stats`, `export`, and `snippet` use `https://<code>.goatcounter.com` when `--url` and the config `url=` are both unset. `sites` and `site create` need an existing site origin: `--url https://<existing-code>.goatcounter.com`. For a self-hosted instance, pass that origin as `--url` (or set `url=` in the config file) on every command.

Exit codes: 0 success, 1 API or network error, 2 bad command line, 3 credentials. JSON errors look like `{"error":"...","exit_code":3}` on stderr.

## Set up analytics on a site repo

1. Run `gcli --version`. If it is missing, install from the project README (Homebrew, Scoop, `install.sh`, a GitHub release, or `go install github.com/WillCraig/goatcounter-cli/cmd/gcli@latest`). Do not download a binary from anywhere else.
2. Create the site. `<code>` is a lowercase hostname label (the goatcounter.com subdomain). `--link-domain` is the public site URL.

   `gcli --json --url https://<existing-code>.goatcounter.com site create <code> --link-domain https://<public-site>`

   Read `site.code` and `site.dashboard` from the JSON. On goatcounter.com the dashboard is `https://<code>.goatcounter.com`.
3. Print the snippet and insert it once, in the shared HTML layout, before `</body>`:

   `gcli --json snippet <code>`

   Use the `script` field exactly. Skip the insert if a `data-goatcounter` script is already present. Do not add it to admin, preview, draft, or email templates unless asked.
4. Check the site's privacy page (look for a privacy route, `privacy.md`, or a footer link). If a page exists and does not mention GoatCounter, add a short factual note: pageviews are counted by GoatCounter, which does not use cookies or store IP addresses or the full User-Agent. Link `https://www.goatcounter.com/help/privacy`. If no privacy page exists, say so and ask before creating one. Do not draft a full legal policy.
5. Tell the user the dashboard URL from the JSON. Do not claim that data is flowing until `stats` shows a non-zero total or the user confirms a page load.

## Weekly stats

```sh
gcli --json stats <code> --from YYYY-MM-DD --to YYYY-MM-DD --top 10
```

Use the last 7 days unless the user names a range. Summarize only fields in the JSON: `total` (visitors, which is what GoatCounter stores), `total_events`, `pages` (`path`, `title`, `count`, `event`), and `referrers` (`name`, `count`). If `more_pages` or `more_referrers` is true, say the list is truncated. Do not fill gaps with estimates.

## Export

```sh
gcli --json export <code> --out hits.csv
```

This writes a CSV of individual pageviews. GoatCounter only includes those rows when the site has individual pageview collection turned on, and a new export can be started about once an hour. Do not commit the CSV. It can contain session ids and referrer URLs.
