// Package config loads the GoatCounter API token and an optional API base URL.
// The token is never taken from command-line arguments.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the on-disk configuration plus the process environment.
// Token is empty when neither the environment nor the file set one.
type Config struct {
	Token string
	URL   string
	Path  string
}

// Load reads GOATCOUNTER_TOKEN and the config file.
// The environment token wins over the file. A missing file is not an error.
// On Unix the file must not be readable by group or other (mode 0600 or stricter).
func Load(getenv func(string) string) (Config, error) {
	return load(runtime.GOOS, getenv)
}

func load(goos string, getenv func(string) string) (Config, error) {
	path, err := Path(goos, getenv)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Path: path}
	fileToken, fileURL, found, err := readFile(goos, path)
	if err != nil {
		return Config{}, err
	}
	if found {
		cfg.Token = fileToken
		cfg.URL = fileURL
	}
	if env := strings.TrimSpace(getenv("GOATCOUNTER_TOKEN")); env != "" {
		cfg.Token = env
	}
	return cfg, nil
}

// Path is ~/.config/goatcounter/config, or %APPDATA%\goatcounter\config on Windows.
func Path(goos string, getenv func(string) string) (string, error) {
	if goos == "windows" {
		appdata := getenv("APPDATA")
		if appdata == "" {
			return "", fmt.Errorf("APPDATA is not set; the config file is %%APPDATA%%\\goatcounter\\config")
		}
		return filepath.Join(appdata, "goatcounter", "config"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("HOME is not set; the config file is ~/.config/goatcounter/config")
	}
	return filepath.Join(home, ".config", "goatcounter", "config"), nil
}

func readFile(goos, path string) (token, apiURL string, found bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("config file %s: %w", path, err)
	}
	if info.IsDir() {
		return "", "", false, fmt.Errorf("config file %s is a directory", path)
	}
	if goos != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", "", false, fmt.Errorf("config file %s is readable by group or other; run: chmod 0600 %s", path, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", false, fmt.Errorf("config file %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return "", "", false, fmt.Errorf("config file %s:%d: expected key=value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = stripQuotes(value)
		switch key {
		case "token":
			if value == "" {
				return "", "", false, fmt.Errorf("config file %s:%d: token is empty", path, lineNo)
			}
			token = value
		case "url":
			apiURL = value
		default:
			return "", "", false, fmt.Errorf("config file %s:%d: unknown key %q", path, lineNo, key)
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", false, fmt.Errorf("config file %s: %w", path, err)
	}
	return token, apiURL, true, nil
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
