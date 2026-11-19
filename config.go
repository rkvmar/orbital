package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func configPath(name string) string {
	return filepath.Join(serviceDir(name), "config.toml")
}

func envFilePath(name string) string {
	return filepath.Join(serviceDir(name), ".env")
}

func defaultConfig(s service) string {
	return fmt.Sprintf(`url = %s
port = %d
build = %s
start = %s
language = %s
`, tomlString(s.URL), s.Port, tomlString(s.BuildCmd), tomlString(s.StartCmd), tomlString(s.Language))
}

func tomlString(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

func parseConfig(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = trimQuotes(strings.TrimSpace(v))
	}
	return out
}

func serializeEnv(env map[string]string) string {
	var b strings.Builder
	for _, k := range envKeys(env) {
		fmt.Fprintf(&b, "%s=%s\n", k, env[k])
	}
	return b.String()
}

func parseEnvFile(raw string) (map[string]string, error) {
	env := map[string]string{}
	for n, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !validEnvKey.MatchString(k) {
			return nil, fmt.Errorf("invalid env line %d: %q", n+1, line)
		}
		env[k] = trimQuotes(strings.TrimSpace(v))
	}
	return env, nil
}

func trimQuotes(v string) string {
	if len(v) < 2 {
		return v
	}
	if v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		v = strings.ReplaceAll(v, `\"`, `"`)
		return strings.ReplaceAll(v, `\\`, `\`)
	}
	if v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

func resolve(s service) (service, error) {
	repo := filepath.Join(serviceDir(s.Name), "repo")
	if st, err := detectStack(repo); err == nil {
		s.Language, s.BuildCmd, s.StartCmd = st.name, st.buildCmd, st.startCmd
	}
	if p := detectPort(repo); p != 0 {
		s.Port = p
	}
	cfgRaw, err := os.ReadFile(configPath(s.Name))
	if err != nil {
		return s, err
	}
	cfg := parseConfig(string(cfgRaw))
	if v := cfg["url"]; v != "" {
		s.URL = v
	}
	if v := cfg["language"]; v != "" {
		s.Language = v
	}
	if v := cfg["build"]; v != "" {
		s.BuildCmd = v
	}
	if v := cfg["start"]; v != "" {
		s.StartCmd = v
	}
	if v := cfg["port"]; v != "" {
		p, err := parsePort(v)
		if err != nil {
			return s, err
		}
		s.Port = p
	}
	envRaw, err := os.ReadFile(envFilePath(s.Name))
	if err != nil {
		return s, err
	}
	env, err := parseEnvFile(string(envRaw))
	if err != nil {
		return s, err
	}
	s.Env = env
	return s, nil
}

func openEditor(path string) error {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func editAndApply(s service, path, content, label string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if err := openEditor(path); err != nil {
		return fmt.Errorf("open editor: %w", err)
	}
	s, err := resolve(s)
	if err != nil {
		return err
	}
	if err := saveService(s); err != nil {
		return err
	}
	if err := restartService(s); err != nil {
		return err
	}
	fmt.Printf("Updated %s for %s.\n", label, s.Name)
	return nil
}
