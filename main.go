package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

var (
	validName   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	validEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "launch":
		err = cmdLaunch(os.Args[2:])
	case "update":
		err = cmdUpdate(os.Args[2:])
	case "config":
		err = cmdConfig(os.Args[2:])
	case "env":
		err = cmdEnv(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "log":
		err = cmdLog(os.Args[2:])
	case "stop":
		err = cmdStop(os.Args[2:])
	case "start":
		err = cmdStart(os.Args[2:])
	case "remove":
		err = cmdRemove(os.Args[2:])
	case "db":
		err = cmdDB(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "orbital: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "orbital:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`Usage:
  orbital launch <git-url> [name]   clone, build and start a service
  orbital update <name>             rebuild/restart a service, or apply config to a database
  orbital config <name>             edit config.toml (blank = detected default)
  orbital env <name>                edit the service's .env file
  orbital status                    list all services
  orbital log <name>                show a service's logs
  orbital stop <name>               stop a service
  orbital start <name>              start a stopped service
  orbital remove <name>             delete a service
  orbital db add <type> [flags]     add a database as a service (installs binaries if needed)
`)
}

func cmdLaunch(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: orbital launch <git-url> [name]")
	}
	url := args[0]
	name := deriveName(url)
	if len(args) == 2 {
		name = args[1]
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid name %q (use letters, numbers, - and _)", name)
	}
	return deploy(url, name)
}

func cmdUpdate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital update <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if s.Kind == "db" {
		resolved, err := resolve(s)
		if err != nil {
			return err
		}
		if err := saveService(resolved); err != nil {
			return err
		}
		if err := restartService(resolved); err != nil {
			return err
		}
		fmt.Printf("Updated %s.\n", s.Name)
		return nil
	}
	return deploy(s.URL, s.Name)
}

func cmdConfig(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital config <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	return editAndApply(s, configPath(s.Name), defaultConfig(s), "config")
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q (use 0 for none or 1-65535)", raw)
	}
	return port, nil
}

func deploy(url, name string) error {
	dir := serviceDir(name)
	cfgPath, epPath := configPath(name), envFilePath(name)
	configRaw, _ := os.ReadFile(cfgPath)
	envRaw, _ := os.ReadFile(epPath)
	var env map[string]string
	if old, err := loadService(name); err == nil {
		env = old.Env
		if len(envRaw) == 0 {
			envRaw = []byte(serializeEnv(env))
		}
		if alive(old.PID) {
			fmt.Printf("Stopping existing %s...\n", name)
			_ = stopProcess(old.PID)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	repo := filepath.Join(dir, "repo")
	fmt.Printf("Cloning %s...\n", url)
	out, err := exec.Command("git", "clone", "--depth", "1", url, repo).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone failed: %w\n%s", err, out)
	}
	st, err := detectStack(repo)
	if err != nil {
		return err
	}
	s := service{Name: name, URL: url, Language: st.name, BuildCmd: st.buildCmd,
		StartCmd: st.startCmd, Port: detectPort(repo), Env: env}
	if len(configRaw) == 0 {
		configRaw = []byte(defaultConfig(s))
	}
	if len(envRaw) == 0 {
		envRaw = []byte(serializeEnv(s.Env))
	}
	if err := os.WriteFile(cfgPath, configRaw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(epPath, envRaw, 0o644); err != nil {
		return err
	}
	s, err = resolve(s)
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "log.txt")
	if s.BuildCmd != "" {
		fmt.Printf("Building %s (%s)...\n", name, s.Language)
		if err := runLogged(repo, logPath, s.Env, "/bin/sh", "-c", s.BuildCmd); err != nil {
			return fmt.Errorf("build failed (see %s): %w", logPath, err)
		}
	}
	if s.Port == 0 {
		fmt.Printf("Starting %s...\n", name)
	} else {
		fmt.Printf("Starting %s on port %d...\n", name, s.Port)
	}
	pid, err := startProcess(s, repo, logPath)
	if err != nil {
		return fmt.Errorf("start failed: %w", err)
	}
	s.PID, s.StartedAt = pid, time.Now()
	if err := saveService(s); err != nil {
		_ = stopProcess(pid)
		return err
	}
	if s.Port == 0 {
		fmt.Printf("Deployed %s (%s), pid %d\n", name, s.Language, pid)
	} else {
		fmt.Printf("Deployed %s (%s) at http://localhost:%d, pid %d\n", name, s.Language, s.Port, pid)
	}
	return nil
}

func restartService(s service) error {
	if !alive(s.PID) {
		return nil
	}
	_ = stopProcess(s.PID)
	dir := serviceDir(s.Name)
	pid, err := startProcess(s, serviceWorkDir(s), filepath.Join(dir, "log.txt"))
	if err != nil {
		return err
	}
	s.PID, s.StartedAt = pid, time.Now()
	return saveService(s)
}

func cmdStatus(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: orbital status")
	}
	services, err := listServices()
	if err != nil {
		return err
	}
	if len(services) == 0 {
		fmt.Println("No services deployed.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tLANGUAGE\tSTATUS\tPORT\tPID\tUPTIME")
	for _, s := range services {
		status, pid, uptime, port := "stopped", "-", "-", "-"
		if alive(s.PID) {
			status = "running"
			pid = fmt.Sprint(s.PID)
			uptime = time.Since(s.StartedAt).Round(time.Second).String()
		}
		if s.Port != 0 {
			port = fmt.Sprint(s.Port)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Language, status, port, pid, uptime)
	}
	return w.Flush()
}

func cmdLog(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital log <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(serviceDir(s.Name), "log.txt"))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func cmdStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital stop <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if !alive(s.PID) {
		fmt.Printf("%s is not running.\n", s.Name)
		return nil
	}
	if err := stopProcess(s.PID); err != nil {
		return err
	}
	fmt.Printf("Stopped %s.\n", s.Name)
	return nil
}

func cmdEnv(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital env <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	return editAndApply(s, envFilePath(s.Name), serializeEnv(s.Env), "env")
}

func envKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cmdStart(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital start <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if alive(s.PID) {
		fmt.Printf("%s is already running.\n", s.Name)
		return nil
	}
	dir := serviceDir(s.Name)
	pid, err := startProcess(s, serviceWorkDir(s), filepath.Join(dir, "log.txt"))
	if err != nil {
		return err
	}
	s.PID, s.StartedAt = pid, time.Now()
	if err := saveService(s); err != nil {
		return err
	}
	if s.Port == 0 {
		fmt.Printf("Started %s, pid %d.\n", s.Name, pid)
	} else {
		fmt.Printf("Started %s on port %d, pid %d.\n", s.Name, s.Port, pid)
	}
	return nil
}

func cmdRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital remove <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if err := stopProcess(s.PID); err != nil {
		return err
	}
	if err := os.RemoveAll(serviceDir(s.Name)); err != nil {
		return err
	}
	fmt.Printf("Removed %s.\n", s.Name)
	return nil
}

func deriveName(rawURL string) string {
	s := strings.TrimSuffix(rawURL, "/")
	s = s[strings.LastIndexAny(s, "/:")+1:]
	s = strings.TrimSuffix(strings.ToLower(s), ".git")
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), "-_")
	if name == "" {
		return "app"
	}
	return name
}
