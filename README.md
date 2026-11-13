# gcli

`gcli` is a small, dependency-free Go client for the [GoatCounter API](https://www.goatcounter.com/help/api). It lists and creates sites, prints the tracking snippet, reads visitor totals, and downloads a CSV export.

The binary is static (`CGO_ENABLED=0`). It uses only the Go standard library.

## Install

### GitHub release

Download the archive for your OS and architecture from [GitHub Releases](https://github.com/WillCraig/goatcounter-cli/releases) (`darwin`, `linux`, or `windows`; `amd64` or `arm64`). macOS and Linux archives are `.tar.gz`. Windows archives are `.zip`. Put `gcli` (or `gcli.exe`) on your `PATH`.

A release is published when a `v*` tag is pushed. This repository does not ship binaries until that tag exists.

### Homebrew

After the first release publishes the formula to `WillCraig/homebrew-tap`:

```sh
brew tap WillCraig/homebrew-tap
brew install gcli
```

### Scoop

The Scoop manifest is published to the `bucket/` directory of the same tap:

```powershell
scoop bucket add WillCraig https://github.com/WillCraig/homebrew-tap
scoop install gcli
```

### Shell script (macOS and Linux)

The script does not use sudo. It installs to `~/.local/bin` unless `GCLI_INSTALL_DIR` is set, and it checks the release checksum.

```sh
curl -fsSL https://raw.githubusercontent.com/WillCraig/goatcounter-cli/main/install.sh | sh
```

Pin a version with `GCLI_VERSION=v0.1.0`.

### From source

```sh
go install github.com/WillCraig/goatcounter-cli/cmd/gcli@latest
```

## Configure

Create an API key in GoatCounter (`username menu → API`). The token is read from the environment or from a config file. It is not accepted as a flag.

```sh
export GOATCOUNTER_TOKEN=your-token
```

Or store it in the config file, mode `0600`:

```text
# ~/.config/goatcounter/config
# Windows: %APPDATA%\goatcounter\config
token=your-token

# Optional. Set this for a self-hosted GoatCounter.
# On goatcounter.com, leave it unset if you have more than one site.
# url=https://stats.example.com
```

```sh
chmod 0600 ~/.config/goatcounter/config
```

`GOATCOUNTER_TOKEN` overrides `token=` in the file. `gcli` refuses a Unix config file that is readable by group or other.

`--url` overrides `url=` in the file. A trailing `/api/v0` is accepted and removed. Do not put a username, password, or token in the URL.

Host selection:

1. `--url`
2. `url=` in the config file
3. `https://<code>.goatcounter.com` for commands that take a site code

`sites` and `site create` have no code of their own. Point them at an existing site (`--url https://yoursite.goatcounter.com`) or at the self-hosted origin. The API is served by a site, not by `https://goatcounter.com`.

## Usage

```text
gcli [--json] [--url URL] <command>
```

```text
gcli --url https://yoursite.goatcounter.com sites
gcli --url https://yoursite.goatcounter.com site create myblog --link-domain https://example.com
gcli snippet myblog
gcli stats myblog --from 2026-11-01 --to 2026-11-07 --top 10
gcli export myblog --out hits.csv
```

`--json` prints one JSON document on stdout. Errors are a JSON object on stderr (`error`, `exit_code`).

`snippet` does not call the network and does not need a token. On goatcounter.com the script loads `count.js` from `gc.zgo.at`. With a self-hosted `--url`, it loads `count.js` from that origin.

`stats` prints GoatCounter's visitor total (GoatCounter counts visitors, not every raw page load), the top pages from `/api/v0/stats/hits`, and the top referrers from `/api/v0/stats/toprefs`. Dates are inclusive UTC days (`YYYY-MM-DD`). Omitted dates use the API default of about the last 7 days. `--top` is 1 to 100 and defaults to 10.

`export` starts a CSV export, waits until `finished_at` is set, and downloads it. GoatCounter gzips the file; `gcli` decompresses it. Without `--out` the CSV is written to stdout and status lines go to stderr. With `--out` the file is mode `0600`. A new export can be started about once an hour. Individual pageviews are included only when that site setting is enabled.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | The API request failed, or the network failed |
| 2 | The command line is wrong |
| 3 | No token, the config file is readable by others, or the API returned 401 or 403 |

## Privacy

GoatCounter is built to avoid personal data. The public policy is [goatcounter.com/help/privacy](https://www.goatcounter.com/help/privacy). In short, the analytics store aggregate counts (browser, system, coarse location, language, screen width, path, referrer). They do not store IP addresses, the full User-Agent, or a tracker cookie. A random session id is kept in memory for up to eight hours. Collection of individual pageviews is off unless you turn it on, and a CSV export of those rows is what `gcli export` downloads.

`gcli` sends the API token only in the `Authorization` header to the host you configured. It does not put the token in arguments, URLs, or log lines. If an API error body contains the token, the token is replaced with `[redacted]` before printing. Redirects to a different host are refused, so the token is not forwarded.

The tracking snippet is a pageview beacon to your GoatCounter site. Put it only on pages you mean to measure, and mention GoatCounter on the site's privacy page.

## Development

```sh
go build -o gcli ./cmd/gcli
go vet ./...
go test ./...
```

Tests use `net/http/httptest`. They do not call goatcounter.com.

CI runs `go vet` and `go test` on `ubuntu-latest` and `windows-latest`. The release workflow runs on `ubuntu-latest` for tags matching `v*` and publishes archives with [GoReleaser](.goreleaser.yaml). Publishing the Homebrew formula and Scoop manifest needs a `WillCraig/homebrew-tap` repository and a token that can push to it. The default `GITHUB_TOKEN` can publish the GitHub release for this repository only.
