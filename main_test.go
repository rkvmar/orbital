package main

import (
	"os"
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

func TestParseEnvFile(t *testing.T) {
	got, err := parseEnvFile("# comment\nNEW=x=y\nOLD=\"2\"\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["NEW"] != "x=y" || got["OLD"] != "2" {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"NOEQUALS", "=value", "1BAD=value"} {
		if _, err := parseEnvFile(bad); err == nil {
			t.Errorf("parseEnvFile accepted %q", bad)
		}
	}
}

func TestNodeStackPreview(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"scripts":{"dev":"vite dev","build":"vite build","preview":"vite preview"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := detectStack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.buildCmd != "npm install && npm run build" || st.startCmd != "npm run preview -- --port $PORT --host" {
		t.Fatalf("unexpected stack %+v", st)
	}
}

func TestDefaultConfigFilled(t *testing.T) {
	s := service{Name: "demo", URL: "https://example.com/demo.git", Language: "go",
		BuildCmd: `go build -ldflags="-w -s" -o app .`, StartCmd: "./app", Port: 8081}
	cfg := parseConfig(defaultConfig(s))
	if cfg["url"] != s.URL || cfg["port"] != "8081" || cfg["build"] != s.BuildCmd ||
		cfg["start"] != s.StartCmd || cfg["language"] != s.Language {
		t.Fatalf("config round trip failed: %v", cfg)
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	repo := filepath.Join(serviceDir("demo"), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main(){listen(\":8081\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath("demo"), []byte("port = \"9000\"\nstart = \"./custom\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFilePath("demo"), []byte("API_KEY=secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(service{Name: "demo", URL: "x", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 || got.StartCmd != "./custom" || got.Env["API_KEY"] != "secret" {
		t.Fatalf("unexpected resolved service: %+v", got)
	}
	if got.BuildCmd != "go build -o app ." {
		t.Fatalf("build should fall back to detection, got %q", got.BuildCmd)
	}
}

func TestNextPort(t *testing.T) {
	services := []service{{Port: 8080}, {Port: 8081}}
	if got := nextPort(services); got != 8082 {
		t.Errorf("nextPort = %d, want 8082", got)
	}
}

func TestDetectStackAndPort(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func main() { listen(":8081") }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := detectStack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.name != "go" || st.startCmd != "./app" {
		t.Fatalf("unexpected stack %+v", st)
	}
	if got := detectPort(dir); got != 8081 {
		t.Errorf("detectPort = %d, want 8081", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	want := service{Name: "demo", URL: "https://example.com/demo.git", Language: "go", Port: 8080}
	if err := saveService(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadService("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Port != want.Port || got.Language != want.Language {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if list, err := listServices(); err != nil || len(list) != 1 {
		t.Fatalf("listServices = %v, %v", list, err)
	}
	if filepath.Base(serviceDir("demo")) != "demo" {
		t.Fatal("unexpected service directory")
	}
}
