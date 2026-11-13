package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	unix, err := Path("linux", func(k string) string {
		if k == "HOME" {
			return "/home/ada"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if unix != filepath.Join("/home/ada", ".config", "goatcounter", "config") {
		t.Fatalf("unix path: %s", unix)
	}
	if _, err := Path("linux", func(string) string { return "" }); err == nil {
		t.Fatal("expected error when HOME is unset")
	}

	win, err := Path("windows", func(k string) string {
		if k == "APPDATA" {
			return `C:\Users\ada\AppData\Roaming`
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`C:\Users\ada\AppData\Roaming`, "goatcounter", "config")
	if win != want {
		t.Fatalf("windows path: got %s want %s", win, want)
	}
	if _, err := Path("windows", func(string) string { return "" }); err == nil {
		t.Fatal("expected error when APPDATA is unset")
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	home := t.TempDir()
	path, err := Path(runtime.GOOS, testGetenv(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# comment\ntoken = \"file-token\"\nurl = 'https://stats.example.com'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(runtime.GOOS, testGetenv(home))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "file-token" || cfg.URL != "https://stats.example.com" {
		t.Fatalf("cfg: %+v", cfg)
	}

	env := func(k string) string {
		if k == "GOATCOUNTER_TOKEN" {
			return "env-token"
		}
		return testGetenv(home)(k)
	}
	cfg, err = load(runtime.GOOS, env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("env token should win, got %q", cfg.Token)
	}
	if cfg.URL != "https://stats.example.com" {
		t.Fatalf("url: %q", cfg.URL)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := load(runtime.GOOS, testGetenv(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" || cfg.URL != "" {
		t.Fatalf("cfg: %+v", cfg)
	}
}

func TestRejectLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	home := t.TempDir()
	path, err := Path(runtime.GOOS, testGetenv(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-token-value"
	if err := os.WriteFile(path, []byte("token="+secret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = load(runtime.GOOS, testGetenv(home))
	if err == nil {
		t.Fatal("expected permission error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("permission error includes the token")
	}
	if !strings.Contains(err.Error(), "chmod 0600") {
		t.Fatalf("error: %v", err)
	}
}

func TestRejectUnknownKey(t *testing.T) {
	home := t.TempDir()
	path, err := Path(runtime.GOOS, testGetenv(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("password=nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(runtime.GOOS, testGetenv(home)); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func testGetenv(home string) func(string) string {
	return func(k string) string {
		switch k {
		case "HOME":
			return home
		case "APPDATA":
			return home
		default:
			return ""
		}
	}
}
