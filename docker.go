package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func docker(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func dockerUp() bool {
	_, err := docker("info", "--format", "{{.ServerVersion}}")
	return err == nil
}

func ensureDocker() error {
	if dockerUp() {
		return nil
	}
	fmt.Println("Starting Docker...")
	switch runtime.GOOS {
	case "darwin":
		if err := exec.Command("open", "-a", "Docker").Run(); err != nil {
			return fmt.Errorf("start Docker: %w", err)
		}
	case "linux":
		if err := exec.Command("systemctl", "start", "docker").Run(); err != nil {
			if err := exec.Command("sudo", "-n", "systemctl", "start", "docker").Run(); err != nil {
				return fmt.Errorf("start Docker: %w", err)
			}
		}
	default:
		return fmt.Errorf("cannot auto-start Docker on %s; start it manually", runtime.GOOS)
	}
	for i := 0; i < 60; i++ {
		if dockerUp() {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("Docker did not start within 60s")
}

func ensureBuildkit() error {
	state, err := docker("inspect", "-f", "{{.State.Running}}", "buildkit")
	if err == nil {
		if state == "true" {
			return nil
		}
		_, err = docker("start", "buildkit")
		return err
	}
	fmt.Println("Starting BuildKit...")
	_, err = docker("run", "-d", "--privileged", "--name", "buildkit", "moby/buildkit:latest")
	return err
}

func ensureRailpack() error {
	if _, err := exec.LookPath("railpack"); err == nil {
		return nil
	}
	fmt.Println("Installing railpack...")
	cmd := exec.Command("sh", "-c", "curl -sSL https://railpack.com/install.sh | sh")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing railpack: %w", err)
	}
	if _, err := exec.LookPath("railpack"); err != nil {
		return fmt.Errorf("railpack installed but not on PATH")
	}
	return nil
}

func railpackBuild(dir, image string) error {
	cmd := exec.Command("railpack", "build", "--name", image, "--progress", "plain", dir)
	cmd.Env = append(os.Environ(), "BUILDKIT_HOST=docker-container://buildkit")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func waitForListenPort(container string) int {
	for i := 0; i < 30; i++ {
		out, err := docker("exec", container, "sh", "-c", "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null")
		if err == nil {
			if port := parseListenPorts(out); port != 0 {
				return port
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0
}

func parseListenPorts(raw string) int {
	var ports []int
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		i := strings.LastIndex(fields[1], ":")
		if i < 0 {
			continue
		}
		p, err := strconv.ParseInt(fields[1][i+1:], 16, 32)
		if err == nil && p >= 1024 {
			ports = append(ports, int(p))
		}
	}
	if len(ports) == 0 {
		return 0
	}
	sort.Ints(ports)
	return ports[0]
}

func containerState(name string) string {
	state, err := docker("inspect", "-f", "{{.State.Status}}", containerName(name))
	if err != nil {
		return "missing"
	}
	return state
}
