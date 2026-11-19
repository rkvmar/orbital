package main

import (
	"path/filepath"
	"testing"
)

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/user/my-app.git": "my-app",
		"git@github.com:user/My_App.git":     "my_app",
		"https://example.com/a/b/":           "b",
		"https://example.com/!!!":            "app",
	}
	for in, want := range cases {
		if got := deriveName(in); got != want {
			t.Errorf("deriveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePort(t *testing.T) {
	for _, raw := range []string{"0", "65536", "abc", "-1"} {
		if _, err := parsePort(raw); err == nil {
			t.Errorf("parsePort(%q) unexpectedly succeeded", raw)
		}
	}
	if got, err := parsePort("9090"); err != nil || got != 9090 {
		t.Errorf("parsePort(9090) = %d, %v", got, err)
	}
}

func TestParseListenPorts(t *testing.T) {
	raw := `  sl  local_address rem_address   st
   0: 00000000:D172 00000000:0000 01 00000000:00000000
   1: 00000000000000000000000000000000:1F91 00000000000000000000000000000000:0000 0A 00000000:00000000
   2: 0100007F:1F90 0100007F:D172 01 00000000:00000000`
	if got := parseListenPorts(raw); got != 8081 {
		t.Errorf("got %d, want 8081", got)
	}
	if got := parseListenPorts(""); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestParseEnv(t *testing.T) {
	got, err := parseEnv(map[string]string{"OLD": "1"}, []string{"NEW=x=y", "OLD=2"})
	if err != nil {
		t.Fatal(err)
	}
	if got["NEW"] != "x=y" || got["OLD"] != "2" {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"NOEQUALS", "=value", "1BAD=value"} {
		if _, err := parseEnv(nil, []string{bad}); err == nil {
			t.Errorf("parseEnv accepted %q", bad)
		}
	}
}

func TestNextPort(t *testing.T) {
	services := []service{{Port: 8080}, {Port: 8081}}
	if got := nextPort(services); got != 8082 {
		t.Errorf("nextPort = %d, want 8082", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	want := service{Name: "demo", URL: "https://example.com/demo.git", Image: "orbital-demo", Port: 8080}
	if err := saveService(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadService("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Port != want.Port || got.Image != want.Image {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if list, err := listServices(); err != nil || len(list) != 1 {
		t.Fatalf("listServices = %v, %v", list, err)
	}
	if filepath.Base(serviceDir("demo")) != "demo" {
		t.Fatal("unexpected service directory")
	}
}
