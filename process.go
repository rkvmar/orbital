package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	if p.Signal(syscall.Signal(0)) != nil {
		return false
	}
	// A zombie still answers signal 0 but is already gone.
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err != nil || !strings.Contains(string(out), "Z")
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

func startScript(s service) string {
	body := s.StartCmd + "\ncode=$?\n"
	if s.MemoryLimitMB > 0 {
		limitKB := s.MemoryLimitMB * 1024
		body = fmt.Sprintf(`%s &
pid=$!
while kill -0 "$pid" 2>/dev/null; do
  [ "$(ps -o stat= -p "$pid" 2>/dev/null | tr -d ' ')" = "Z" ] && break
  rss=$(ps -Axo pgid=,rss= | awk -v g="$$" '$1==g {s+=$2} END {print s+0}')
  if [ "$rss" -gt %d ]; then
    echo "orbital: memory limit exceeded (${rss}KB > %dKB), killing process" >&2
    for p in $(ps -Axo pid=,pgid= | awk -v g="$$" '$2==g && $1!=g {print $1}'); do
      kill -9 "$p" 2>/dev/null
    done
    break
  fi
  sleep 0.2
done
wait "$pid"
code=$?
`, s.StartCmd, limitKB, limitKB)
	}
	if !s.RestartOnCrash {
		return body
	}
	return "trap 'exit 0' TERM INT\nwhile :; do\n" + body +
		"[ \"$code\" -eq 0 ] && break\n" +
		"echo \"orbital: process exited with code $code, restarting...\" >&2\n" +
		"sleep 1\ndone\n"
}

func startProcess(s service, dir, logPath string) (int, error) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	cmd := exec.Command("/bin/sh", "-c", startScript(s))
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if s.Port != 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", s.Port))
	}
	if s.Kind == "db" {
		cmd.Env = append(cmd.Env, "DATA_DIR="+s.DataDir, "DB_NAME="+s.Name, "DB_PASSWORD="+s.Password)
	}
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
