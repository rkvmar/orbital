package main

import (
	"fmt"
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
	if os.Args[1] != "help" && os.Args[1] != "-h" && os.Args[1] != "--help" {
		if err := ensureDocker(); err != nil {
			fmt.Fprintln(os.Stderr, "orbital:", err)
			os.Exit(1)
		}
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
  orbital launch <git-url> [name]   clone, build with Railpack and run in Docker
  orbital update <name>             rebuild and redeploy from its git URL
  orbital config port <name> <port> change a service's host port
  orbital config container-port <name> <port> change the port the app listens on
  orbital env <name> [KEY=VALUE...] show or set environment variables
  orbital status                    list all services
  orbital log <name>                show a service's logs
  orbital stop <name>               stop and remove a service container
  orbital start <name>              start a stopped service
  orbital remove <name>             delete a service and its image
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
	services, err := listServices()
	if err != nil {
		return err
	}
	return deploy(url, name, nextPort(services))
}

func cmdUpdate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital update <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	return deploy(s.URL, s.Name, s.Port)
}

func cmdConfig(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: orbital config port <name> <port> | orbital config container-port <name> <port>")
	}
	s, err := loadService(args[1])
	if err != nil {
		return err
	}
	port, err := parsePort(args[2])
	if err != nil {
		return err
	}
	label := "port"
	switch args[0] {
	case "port":
		services, err := listServices()
		if err != nil {
			return err
		}
		for _, other := range services {
			if other.Name != s.Name && other.Port == port {
				return fmt.Errorf("port %d is already used by %s", port, other.Name)
			}
		}
		s.Port = port
	case "container-port":
		s.ContainerPort = port
		label = "container port"
	default:
		return fmt.Errorf("unknown config key %q (use port or container-port)", args[0])
	}
	if err := replaceContainer(s); err != nil {
		return err
	}
	fmt.Printf("%s now uses %s %d\n", s.Name, label, port)
	return nil
}

func replaceContainer(s service) error {
	existed := containerState(s.Name) != "missing"
	if existed {
		if _, err := docker("rm", "-f", containerName(s.Name)); err != nil {
			return err
		}
	}
	if err := saveService(s); err != nil {
		return err
	}
	if existed {
		return startContainer(s)
	}
	return nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q (use 1-65535)", raw)
	}
	return port, nil
}

func deploy(url, name string, port int) error {
	if err := ensureRailpack(); err != nil {
		return err
	}
	var env map[string]string
	var configuredPort int
	if old, err := loadService(name); err == nil {
		env = old.Env
		configuredPort = old.ContainerPort
	}

	dir := serviceDir(name)
	if containerState(name) != "missing" {
		fmt.Printf("Removing existing %s...\n", name)
		if _, err := docker("rm", "-f", containerName(name)); err != nil {
			return err
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

	if err := ensureBuildkit(); err != nil {
		return fmt.Errorf("BuildKit: %w", err)
	}
	image := containerName(name)
	fmt.Printf("Building %s with Railpack...\n", name)
	if err := railpackBuild(repo, image); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	s := service{Name: name, URL: url, Image: image, Port: port,
		ContainerPort: configuredPort, CreatedAt: time.Now(), Env: env}
	if err := startContainer(s); err != nil {
		return err
	}
	if detected := waitForListenPort(containerName(name)); detected != 0 && detected != s.internalPort() {
		fmt.Printf("Detected app listening on %d, remapping...\n", detected)
		s.ContainerPort = detected
		if _, err := docker("rm", "-f", containerName(name)); err != nil {
			return err
		}
		if err := startContainer(s); err != nil {
			return err
		}
	}
	if err := saveService(s); err != nil {
		_, _ = docker("rm", "-f", containerName(name))
		return err
	}
	fmt.Printf("Deployed %s at http://localhost:%d\n", name, port)
	return nil
}

func startContainer(s service) error {
	fmt.Printf("Starting %s on port %d...\n", s.Name, s.Port)
	args := []string{"run", "-d", "--name", containerName(s.Name),
		"-p", fmt.Sprintf("%d:%d", s.Port, s.internalPort()), "-e", fmt.Sprintf("PORT=%d", s.internalPort())}
	for _, k := range envKeys(s.Env) {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	_, err := docker(append(args, s.Image)...)
	return err
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
	fmt.Fprintln(w, "NAME\tSTATUS\tPORT\tURL")
	for _, s := range services {
		status := "stopped"
		if containerState(s.Name) == "running" {
			status = "running"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, status, s.Port, s.URL)
	}
	return w.Flush()
}

func cmdLog(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital log <name>")
	}
	if _, err := loadService(args[0]); err != nil {
		return err
	}
	out, err := docker("logs", containerName(args[0]))
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func cmdStop(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: orbital stop <name>")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if containerState(s.Name) == "missing" {
		fmt.Printf("%s is not running.\n", s.Name)
		return nil
	}
	if _, err := docker("stop", containerName(s.Name)); err != nil {
		return err
	}
	if _, err := docker("rm", containerName(s.Name)); err != nil {
		return err
	}
	fmt.Printf("Stopped %s.\n", s.Name)
	return nil
}

func cmdEnv(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: orbital env <name> [KEY=VALUE ...]")
	}
	s, err := loadService(args[0])
	if err != nil {
		return err
	}
	if len(args) == 1 {
		keys := envKeys(s.Env)
		if len(keys) == 0 {
			fmt.Printf("No environment variables set for %s.\n", s.Name)
			return nil
		}
		for _, k := range keys {
			fmt.Printf("%s=%s\n", k, s.Env[k])
		}
		return nil
	}
	env, err := parseEnv(s.Env, args[1:])
	if err != nil {
		return err
	}
	s.Env = env
	if err := replaceContainer(s); err != nil {
		return err
	}
	fmt.Printf("Updated environment for %s.\n", s.Name)
	return nil
}

func parseEnv(base map[string]string, assignments []string) (map[string]string, error) {
	env := make(map[string]string, len(base)+len(assignments))
	for k, v := range base {
		env[k] = v
	}
	for _, item := range assignments {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !validEnvKey.MatchString(key) {
			return nil, fmt.Errorf("invalid env assignment %q (use KEY=VALUE)", item)
		}
		env[key] = value
	}
	return env, nil
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
	state := containerState(s.Name)
	if state == "running" {
		fmt.Printf("%s is already running.\n", s.Name)
		return nil
	}
	if state != "missing" {
		if _, err := docker("start", containerName(s.Name)); err != nil {
			return err
		}
	} else if err := startContainer(s); err != nil {
		return err
	}
	fmt.Printf("Started %s on port %d.\n", s.Name, s.Port)
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
	if containerState(s.Name) != "missing" {
		if _, err := docker("rm", "-f", containerName(s.Name)); err != nil {
			return err
		}
	}
	_, _ = docker("rmi", "-f", s.Image)
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
