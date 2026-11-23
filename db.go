package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type dbInstall struct {
	brew, apt, dnf, yum, pacman, apk string
	hint                             string
}

type dbPreset struct {
	port       int
	password   bool
	bins       []string
	formula    string
	install    dbInstall
	init       func(s service, bins map[string]string) error
	afterStart func(s service, bins map[string]string) error
	start      func(s service, bins map[string]string) string
	url        func(s service) string
}

func dbPresets() map[string]dbPreset {
	return map[string]dbPreset{
		"postgres": {
			port: 5432, password: true,
			bins: []string{"initdb", "postgres", "psql"}, formula: "postgresql@16",
			install: dbInstall{
				brew: "postgresql@16", apt: "postgresql", dnf: "postgresql-server",
				yum: "postgresql-server", pacman: "postgresql", apk: "postgresql",
			},
			init:       initPostgres,
			afterStart: createPostgresDB,
			start: func(s service, bins map[string]string) string {
				dir := shellQuote(s.DataDir)
				return bins["postgres"] + " -D " + dir + " -h 127.0.0.1 -p $PORT -k " + dir
			},
			url: func(s service) string {
				return fmt.Sprintf("postgres://postgres:%s@localhost:%d/%s", s.Password, s.Port, s.Name)
			},
		},
		"redis": {
			port: 6379, password: true,
			bins: []string{"redis-server"}, formula: "redis",
			install: dbInstall{
				brew: "redis", apt: "redis-server", dnf: "redis",
				yum: "redis", pacman: "redis", apk: "redis",
			},
			start: func(s service, bins map[string]string) string {
				return bins["redis-server"] + " --port $PORT --bind 127.0.0.1 --requirepass " +
					shellQuote(s.Password) + " --dir " + shellQuote(s.DataDir) + " --appendonly yes"
			},
			url: func(s service) string {
				return fmt.Sprintf("redis://:%s@localhost:%d", s.Password, s.Port)
			},
		},
		"mongodb": {
			port: 27017,
			bins: []string{"mongod"}, formula: "mongodb-community",
			install: dbInstall{hint: "MongoDB is not in the default package sources. Install it first: " +
				"`brew tap mongodb/brew && brew install mongodb-community` on macOS, or add MongoDB's apt/yum repo on Linux."},
			start: func(s service, bins map[string]string) string {
				return bins["mongod"] + " --dbpath " + shellQuote(s.DataDir) + " --port $PORT --bind_ip 127.0.0.1"
			},
			url: func(s service) string {
				return fmt.Sprintf("mongodb://localhost:%d/%s", s.Port, s.Name)
			},
		},
	}
}

func normalizeDBType(t string) string {
	switch strings.ToLower(t) {
	case "mongo":
		return "mongodb"
	case "postgresql":
		return "postgres"
	default:
		return strings.ToLower(t)
	}
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func cmdDB(args []string) error {
	if len(args) < 1 || args[0] != "add" {
		return fmt.Errorf("usage: orbital db add <postgres|redis|mongodb|name> [flags]")
	}
	return cmdDBAdd(args[1:])
}

func cmdDBAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: orbital db add <postgres|redis|mongodb|name> [flags]")
	}
	rawType := args[0]
	typ := normalizeDBType(rawType)
	fs := flag.NewFlagSet("db add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", typ, "instance name")
	port := fs.Int("port", 0, "port to listen on (defaults to the database's standard port)")
	startCmd := fs.String("start", "", "foreground start command (required for custom databases)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !validName.MatchString(*name) {
		return fmt.Errorf("invalid name %q (use letters, numbers, - and _)", *name)
	}
	if _, err := loadService(*name); err == nil {
		return fmt.Errorf("service %q already exists", *name)
	}

	p, known := dbPresets()[typ]
	if !known && *startCmd == "" {
		return fmt.Errorf("unknown database %q; use postgres, redis, mongodb or pass --start", rawType)
	}
	preferredPort := *port
	if preferredPort == 0 {
		if !known {
			return fmt.Errorf("--port is required for custom databases")
		}
		preferredPort = p.port
	}
	if preferredPort < 1 || preferredPort > 65535 {
		return fmt.Errorf("invalid port %d (use 1-65535)", preferredPort)
	}
	forcedPort := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			forcedPort = true
		}
	})
	hostPort, err := choosePort(preferredPort, forcedPort)
	if err != nil {
		return err
	}

	pw := ""
	var bins map[string]string
	if known {
		if p.password {
			pw, err = randomPassword()
			if err != nil {
				return err
			}
		}
		bins, err = ensureDBBinaries(p)
		if err != nil {
			return err
		}
	}

	dir := serviceDir(*name)
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	s := service{
		Name: *name, Language: typ, Port: hostPort, Kind: "db", DBType: typ,
		DataDir: dataDir, Password: pw, StartedAt: time.Now(),
	}
	if known {
		if p.init != nil {
			if err := p.init(s, bins); err != nil {
				_ = os.RemoveAll(dir)
				return err
			}
		}
		s.StartCmd = p.start(s, bins)
		s.URL = p.url(s)
	} else {
		s.StartCmd = *startCmd
		s.URL = fmt.Sprintf("localhost:%d", hostPort)
	}

	// config and .env must exist for resolve() and the config/env commands to work.
	if err := os.WriteFile(configPath(*name), []byte(defaultConfig(s)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(envFilePath(*name), nil, 0o600); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "log.txt")
	pid, err := startProcess(s, dir, logPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	s.PID = pid
	if err := saveService(s); err != nil {
		_ = stopProcess(pid)
		return err
	}
	if err := waitForDB(&s, 20*time.Second); err != nil {
		_ = stopProcess(pid)
		_ = os.RemoveAll(dir)
		return err
	}
	if known && p.afterStart != nil {
		if err := p.afterStart(s, bins); err != nil {
			_ = stopProcess(pid)
			_ = os.RemoveAll(dir)
			return err
		}
	}
	fmt.Printf("Added %s at %s (pid %d)\n", s.Name, s.URL, s.PID)
	return nil
}

func findDBBinary(name, formula string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	var candidates []string
	if formula != "" {
		if _, err := exec.LookPath("brew"); err == nil {
			if out, e := exec.Command("brew", "--prefix", formula).Output(); e == nil {
				candidates = append(candidates, filepath.Join(strings.TrimSpace(string(out)), "bin", name))
			}
		}
	}
	matches, _ := filepath.Glob("/usr/lib/postgresql/*/bin/" + name)
	candidates = append(candidates, matches...)
	for _, c := range candidates {
		if exists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

func ensureDBBinaries(p dbPreset) (map[string]string, error) {
	bins := map[string]string{}
	missing := false
	for _, b := range p.bins {
		path, err := findDBBinary(b, p.formula)
		if err != nil {
			missing = true
			continue
		}
		bins[b] = path
	}
	if !missing {
		return bins, nil
	}
	if p.install.hint != "" {
		return nil, fmt.Errorf("%s is not installed. %s", p.bins[0], p.install.hint)
	}
	fmt.Printf("Installing %s...\n", p.bins[0])
	if err := installDBPackages(p.install); err != nil {
		return nil, err
	}
	bins = map[string]string{}
	for _, b := range p.bins {
		path, err := findDBBinary(b, p.formula)
		if err != nil {
			return nil, fmt.Errorf("%s still not found after install: %w", b, err)
		}
		bins[b] = path
	}
	return bins, nil
}

func installDBPackages(inst dbInstall) error {
	switch runtime.GOOS {
	case "darwin":
		if inst.brew == "" {
			return fmt.Errorf("automatic install is unavailable; install the database manually")
		}
		if _, err := exec.LookPath("brew"); err != nil {
			return fmt.Errorf("Homebrew is required to install databases automatically: %w", err)
		}
		return runInteractive("brew", "install", inst.brew)
	case "linux":
		managers := []struct{ bin, pkg string }{
			{"apt-get", inst.apt}, {"dnf", inst.dnf}, {"yum", inst.yum},
			{"pacman", inst.pacman}, {"apk", inst.apk},
		}
		for _, m := range managers {
			if m.pkg == "" {
				continue
			}
			bin, err := exec.LookPath(m.bin)
			if err != nil {
				continue
			}
			prefix := []string{}
			if os.Geteuid() != 0 {
				if _, err := exec.LookPath("sudo"); err != nil {
					return fmt.Errorf("%s needs root; install sudo or run as root", m.bin)
				}
				bin, prefix = "sudo", []string{m.bin}
			}
			if m.bin == "apt-get" {
				if err := runInteractive(bin, append(prefix, "update")...); err != nil {
					return err
				}
			}
			return runInteractive(bin, append(prefix, packageInstallArgs(m.bin, m.pkg)...)...)
		}
		return fmt.Errorf("no supported package manager found (apt-get, dnf, yum, pacman, apk)")
	default:
		return fmt.Errorf("automatic database install is unsupported on %s", runtime.GOOS)
	}
}

func packageInstallArgs(manager, pkg string) []string {
	switch manager {
	case "apt-get", "dnf", "yum":
		return []string{"install", "-y", pkg}
	case "pacman":
		return []string{"-S", "--noconfirm", pkg}
	case "apk":
		return []string{"add", "--no-cache", pkg}
	}
	return nil
}

func runInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func initPostgres(s service, bins map[string]string) error {
	pwPath := filepath.Join(serviceDir(s.Name), ".pgpass_init")
	if err := os.WriteFile(pwPath, []byte(s.Password+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(pwPath)
	cmd := exec.Command(bins["initdb"], "-D", s.DataDir, "-U", "postgres",
		"--auth-local=trust", "--auth-host=scram-sha-256", "--pwfile="+pwPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initdb failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func createPostgresDB(s service, bins map[string]string) error {
	// initdb already creates the postgres database.
	if s.Name == "postgres" {
		return nil
	}
	cmd := exec.Command(bins["psql"], "-h", "127.0.0.1", "-p", strconv.Itoa(s.Port),
		"-U", "postgres", "-d", "postgres", "-c", `CREATE DATABASE "`+s.Name+`"`)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+s.Password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create database failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func portFree(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	return ln.Close()
}

func choosePort(preferred int, forced bool) (int, error) {
	if forced {
		if err := portFree(preferred); err != nil {
			return 0, fmt.Errorf("port %d is already in use; choose another with --port", preferred)
		}
		return preferred, nil
	}
	for p := preferred; p < preferred+100 && p <= 65535; p++ {
		if portFree(p) == nil {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port found near %d; choose one with --port", preferred)
}

// waitForDB waits for our own process to serve the port, not just any listener.
func waitForDB(s *service, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(s.PID) {
			return fmt.Errorf("%s exited during startup; see %s", s.Name, filepath.Join(serviceDir(s.Name), "log.txt"))
		}
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("nothing listening on %s", addr)
}
