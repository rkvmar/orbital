package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func runLogged(dir, logPath string, env map[string]string, name string, args ...string) error {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for _, k := range envKeys(env) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	out := io.MultiWriter(os.Stdout, f)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

func startProcess(s service, dir, logPath string) (int, error) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	cmd := exec.Command("sh", "-c", s.StartCmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", s.Port))
	for _, k := range envKeys(s.Env) {
		cmd.Env = append(cmd.Env, k+"="+s.Env[k])
	}
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

func stopProcess(pid int) error {
	if !alive(pid) {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		p, _ := os.FindProcess(pid)
		_ = p.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
