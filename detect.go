package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type stack struct {
	name     string
	buildCmd string
	startCmd string
}

var (
	cargoNameRE = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)
	portRE      = regexp.MustCompile(`:(80[0-9]{2}|[1-9][0-9]{3,4})\b`)
	sourceExts  = map[string]bool{
		".go": true, ".js": true, ".ts": true, ".py": true, ".rb": true,
		".rs": true, ".java": true, ".php": true, ".md": true, ".json": true,
		".toml": true, ".yaml": true, ".yml": true, ".sh": true, ".env": true,
	}
)

func detectStack(dir string) (stack, error) {
	switch {
	case exists(filepath.Join(dir, "go.mod")):
		return stack{"go", "go build -o app .", "./app"}, nil
	case exists(filepath.Join(dir, "Cargo.toml")):
		name := cargoPackageName(filepath.Join(dir, "Cargo.toml"))
		if name == "" {
			name = "app"
		}
		return stack{"rust", "cargo build --release", "./target/release/" + name}, nil
	case exists(filepath.Join(dir, "package.json")):
		return nodeStack(dir)
	case exists(filepath.Join(dir, "requirements.txt")) || exists(filepath.Join(dir, "pyproject.toml")):
		return pythonStack(dir)
	case exists(filepath.Join(dir, "Gemfile")):
		return rubyStack(dir)
	case exists(filepath.Join(dir, "pom.xml")):
		return stack{"java", "mvn -q package -DskipTests", "java -jar target/*.jar"}, nil
	case exists(filepath.Join(dir, "build.gradle")) || exists(filepath.Join(dir, "build.gradle.kts")):
		return stack{"java", "gradle -q build", "java -jar build/libs/*.jar"}, nil
	}
	return stack{}, fmt.Errorf("no supported project found (need go.mod, package.json, Cargo.toml, requirements.txt, pyproject.toml, Gemfile, pom.xml or build.gradle)")
}

func nodeStack(dir string) (stack, error) {
	pm := "npm"
	switch {
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		pm = "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		pm = "yarn"
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		_ = json.Unmarshal(b, &pkg)
	}
	if _, ok := pkg.Scripts["start"]; ok {
		return stack{"node", pm + " install", pm + " run start"}, nil
	}
	if cmd, ok := pkg.Scripts["preview"]; ok {
		build := pm + " install"
		if _, ok := pkg.Scripts["build"]; ok {
			build += " && " + pm + " run build"
		}
		return stack{"node", build, pm + " run preview" + vitePort(cmd)}, nil
	}
	if cmd, ok := pkg.Scripts["dev"]; ok {
		return stack{"node", pm + " install", pm + " run dev" + vitePort(cmd)}, nil
	}
	for _, f := range []string{"index.js", "server.js", "app.js", "main.js"} {
		if exists(filepath.Join(dir, f)) {
			return stack{"node", pm + " install", "node " + f}, nil
		}
	}
	return stack{}, fmt.Errorf("package.json has no start/preview/dev script and no index.js/server.js/app.js/main.js entry point")
}

func vitePort(script string) string {
	if strings.Contains(script, "vite") {
		return " -- --port $PORT --host"
	}
	return ""
}

func pythonStack(dir string) (stack, error) {
	build := "python3 -m venv .venv && .venv/bin/pip install ."
	if exists(filepath.Join(dir, "requirements.txt")) {
		build = "python3 -m venv .venv && .venv/bin/pip install -r requirements.txt"
	}
	for _, f := range []string{"app.py", "main.py", "server.py"} {
		if exists(filepath.Join(dir, f)) {
			return stack{"python", build, ".venv/bin/python " + f}, nil
		}
	}
	return stack{}, fmt.Errorf("no app.py, main.py or server.py found for the Python entry point")
}

func rubyStack(dir string) (stack, error) {
	if exists(filepath.Join(dir, "config.ru")) {
		return stack{"ruby", "bundle install", "bundle exec rackup -p $PORT"}, nil
	}
	for _, f := range []string{"app.rb", "main.rb"} {
		if exists(filepath.Join(dir, f)) {
			return stack{"ruby", "bundle install", "bundle exec ruby " + f}, nil
		}
	}
	return stack{}, fmt.Errorf("no config.ru, app.rb or main.rb found for the Ruby entry point")
}

func cargoPackageName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inPackage := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inPackage = t == "[package]"
			continue
		}
		if inPackage {
			if m := cargoNameRE.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

func detectPort(dir string) int {
	counts := map[int]int{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "go.sum" {
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range portRE.FindAllStringSubmatch(string(b), -1) {
			p, _ := strconv.Atoi(m[1])
			counts[p]++
		}
		return nil
	})
	best, bestCount := 0, 0
	for p, c := range counts {
		if c > bestCount || (c == bestCount && p < best) {
			best, bestCount = p, c
		}
	}
	return best
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
