package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDBPresetURLs(t *testing.T) {
	p := dbPresets()
	cases := []struct {
		typ, want string
	}{
		{"postgres", "postgres://postgres:pw@localhost:5432/auth"},
		{"redis", "redis://:pw@localhost:6379"},
		{"mongodb", "mongodb://localhost:27017/auth"},
	}
	for _, c := range cases {
		preset, ok := p[c.typ]
		if !ok {
			t.Fatalf("missing preset %q", c.typ)
		}
		s := service{Name: "auth", Port: preset.port, Password: "pw", DataDir: "/data dir", Kind: "db"}
		if got := preset.url(s); got != c.want {
			t.Errorf("%s url = %q, want %q", c.typ, got, c.want)
		}
		cmd := preset.start(s, map[string]string{"postgres": "P", "redis-server": "R", "mongod": "M"})
		if !strings.Contains(cmd, "$PORT") || !strings.Contains(cmd, shellQuote("/data dir")) {
			t.Errorf("%s start command = %q", c.typ, cmd)
		}
	}
}

func TestNormalizeDBType(t *testing.T) {
	for in, want := range map[string]string{"Mongo": "mongodb", "POSTGRESQL": "postgres", "mysql": "mysql"} {
		if got := normalizeDBType(in); got != want {
			t.Errorf("normalizeDBType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMongoDBRequiresPreinstall(t *testing.T) {
	p := dbPresets()["mongodb"]
	if !strings.Contains(p.install.hint, "mongodb/brew") {
		t.Fatalf("mongodb hint = %q", p.install.hint)
	}
	if p.install.brew != "" || p.install.apt != "" || p.install.dnf != "" {
		t.Fatal("mongodb must not be auto-installed")
	}
}

func TestCreatePostgresDBSkipsDefault(t *testing.T) {
	// initdb creates the "postgres" database, so the default instance name must not recreate it.
	if err := createPostgresDB(service{Name: "postgres", Port: 1, Password: "x"}, nil); err != nil {
		t.Fatalf("default postgres name should need no CREATE DATABASE: %v", err)
	}
}

func TestChoosePortSkipsOccupied(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if _, err := choosePort(port, true); err == nil {
		t.Error("forced port accepted an occupied port")
	}
	got, err := choosePort(port, false)
	if err != nil || got <= port || portFree(got) != nil {
		t.Fatalf("choosePort(%d) = %d, %v", port, got, err)
	}
}

func TestDBAddRegistersService(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "redis-server")
	script := "#!/bin/sh\nwhile :; do nc -l 127.0.0.1 \"$PORT\" >/dev/null 2>&1; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdDB([]string{"add", "redis", "--name", "cache"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadService("cache")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "db" || s.DBType != "redis" || s.Language != "redis" || s.Port == 0 ||
		s.Password == "" || s.PID <= 0 || !alive(s.PID) || !exists(s.DataDir) {
		t.Fatalf("unexpected db service: %+v", s)
	}
	if cfg, err := os.ReadFile(configPath("cache")); err != nil || !strings.Contains(string(cfg), "language = \"redis\"") {
		t.Fatalf("config not written: %q, %v", cfg, err)
	}
	list, err := listServices()
	if err != nil || len(list) != 1 || list[0].Name != "cache" {
		t.Fatalf("listServices = %v, %v", list, err)
	}
	if err := cmdStatus(nil); err != nil {
		t.Fatal(err)
	}
	if err := cmdStop([]string{"cache"}); err != nil {
		t.Fatal(err)
	}
	s, _ = loadService("cache")
	if alive(s.PID) {
		t.Fatalf("stop left pid %d running", s.PID)
	}
	if err := cmdStart([]string{"cache"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRemove([]string{"cache"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadService("cache"); err == nil {
		t.Error("service survived remove")
	}
}

func TestServiceStateRoundTripWithDBFields(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	want := service{Name: "auth", Kind: "db", DBType: "postgres", Language: "postgres",
		Port: 5433, Password: "secret", DataDir: "/data/auth", StartedAt: time.Now()}
	if err := saveService(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadService("auth")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || got.DBType != want.DBType || got.DataDir != want.DataDir ||
		got.Password != want.Password || got.Port != want.Port {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if err := os.WriteFile(configPath("auth"), []byte("port = 6000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFilePath("auth"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdUpdate([]string{"auth"}); err != nil {
		t.Fatalf("update database: %v", err)
	}
	got, err = loadService("auth")
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 6000 || got.Kind != "db" || got.Password != want.Password {
		t.Fatalf("update did not apply config: %+v", got)
	}
}
