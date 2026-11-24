package main

import (
	"os"
	"path/filepath"
	"strings"
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
	for _, raw := range []string{"65536", "abc", "-1"} {
		if _, err := parsePort(raw); err == nil {
			t.Errorf("parsePort(%q) unexpectedly succeeded", raw)
		}
	}
	for raw, want := range map[string]int{"0": 0, "9090": 9090} {
		if got, err := parsePort(raw); err != nil || got != want {
			t.Errorf("parsePort(%q) = %d, %v, want %d", raw, got, err, want)
		}
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

func TestUvStack(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"uv.lock", "pyproject.toml"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := detectStack(dir); err == nil || !strings.Contains(err.Error(), "uv project") {
		t.Fatalf("expected uv entry point error, got %v", err)
	}

	pyproject := `[project]
name = "demo"

[project.scripts]
demo = "demo:main"
another = "demo:other"

[tool.uv]
`
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := detectStack(dir)
	if err != nil || st.name != "uv" || st.buildCmd != "uv sync --frozen" || st.startCmd != ".venv/bin/demo" {
		t.Fatalf("unexpected uv stack with script: %+v, %v", st, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "bot.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = detectStack(dir)
	if err != nil || st.startCmd != ".venv/bin/demo" {
		t.Fatalf("[project.scripts] should win over a root bot.py: %+v, %v", st, err)
	}
	if err := os.Remove(filepath.Join(dir, "bot.py")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = detectStack(dir)
	if err != nil || st.startCmd != ".venv/bin/python main.py" {
		t.Fatalf("unexpected uv stack with main.py: %+v, %v", st, err)
	}
}

func TestDefaultConfigFilled(t *testing.T) {
	s := service{Name: "demo", URL: "https://example.com/demo.git", Language: "go",
		BuildCmd: `go build -ldflags="-w -s" -o app .`, StartCmd: "./app", Port: 8081,
		RestartOnCrash: true, MemoryLimitMB: 256}
	cfg := parseConfig(defaultConfig(s))
	if cfg["url"] != s.URL || cfg["port"] != "8081" || cfg["build"] != s.BuildCmd ||
		cfg["start"] != s.StartCmd || cfg["language"] != s.Language ||
		cfg["restart_on_crash"] != "true" || cfg["memory_limit"] != "256" {
		t.Fatalf("config round trip failed: %v", cfg)
	}
}

func TestPortlessService(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(serviceDir("demo"), "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath("demo"), []byte("port = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFilePath("demo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(service{Name: "demo"})
	if err != nil || got.Port != 0 {
		t.Fatalf("portless resolve = %+v, %v", got, err)
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
	if err := os.WriteFile(configPath("demo"), []byte("port = \"9000\"\nstart = \"./custom\"\nrestart_on_crash = true\nmemory_limit = 512\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFilePath("demo"), []byte("API_KEY=secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolve(service{Name: "demo", URL: "x", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 9000 || got.StartCmd != "./custom" || got.Env["API_KEY"] != "secret" || !got.RestartOnCrash || got.MemoryLimitMB != 512 {
		t.Fatalf("unexpected resolved service: %+v", got)
	}
	if got.BuildCmd != "go build -o app ." {
		t.Fatalf("build should fall back to detection, got %q", got.BuildCmd)
	}
}

func TestExpandEnvRefs(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	for _, s := range []service{
		{Name: "postgres", Kind: "db", URL: "postgres://u:p@localhost:5433/postgres"},
		{Name: "my-app", URL: "https://example.com/app.git"},
	} {
		if err := saveService(s); err != nil {
			t.Fatal(err)
		}
	}
	got := expandEnvRefs(map[string]string{
		"DATABASE_URL": "$POSTGRES_URL$",
		"NESTED":       "prefix=$MY-APP_URL$ suffix",
		"LOWER":        "$postgres_url$",
		"UNKNOWN":      "$NOPE_URL$",
		"PLAIN":        "unchanged",
	})
	want := map[string]string{
		"DATABASE_URL": "postgres://u:p@localhost:5433/postgres",
		"NESTED":       "prefix=https://example.com/app.git suffix",
		"LOWER":        "postgres://u:p@localhost:5433/postgres",
		"UNKNOWN":      "$NOPE_URL$",
		"PLAIN":        "unchanged",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("expandEnvRefs[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestStartScript(t *testing.T) {
	s := service{StartCmd: "./app"}
	if got := startScript(s); !strings.Contains(got, "./app") || strings.Contains(got, "ps -Axo") {
		t.Fatalf("startScript with no options = %q", got)
	}
	s.MemoryLimitMB = 256
	script := startScript(s)
	for _, want := range []string{"./app &", "ps -Axo pgid=,rss=", "262144", "kill -9", "wait \"$pid\""} {
		if !strings.Contains(script, want) {
			t.Errorf("startScript missing %q: %q", want, script)
		}
	}
	s.RestartOnCrash = true
	script = startScript(s)
	for _, want := range []string{"trap 'exit 0' TERM INT", "while :; do", "code=$?", "sleep 1", "done", "ps -Axo pgid=,rss="} {
		if !strings.Contains(script, want) {
			t.Errorf("startScript missing %q: %q", want, script)
		}
	}
}

func TestInvalidConfigValues(t *testing.T) {
	t.Setenv("ORBITAL_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(serviceDir("demo"), "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envFilePath("demo"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"restart_on_crash = maybe\n", "memory_limit = -1\n", "memory_limit = lots\n"} {
		if err := os.WriteFile(configPath("demo"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := resolve(service{Name: "demo"}); err == nil {
			t.Errorf("expected resolve to reject %q", body)
		}
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
