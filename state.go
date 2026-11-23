package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type service struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Language       string            `json:"language"`
	BuildCmd       string            `json:"buildCmd"`
	StartCmd       string            `json:"startCmd"`
	Port           int               `json:"port"`
	PID            int               `json:"pid"`
	RestartOnCrash bool              `json:"restartOnCrash,omitempty"`
	MemoryLimitMB  int               `json:"memoryLimitMB,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	DBType         string            `json:"dbType,omitempty"`
	DataDir        string            `json:"dataDir,omitempty"`
	Password       string            `json:"password,omitempty"`
	StartedAt      time.Time         `json:"startedAt"`
}

func rootDir() string {
	if h := os.Getenv("ORBITAL_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".orbital")
}

func serviceDir(name string) string {
	return filepath.Join(rootDir(), "services", name)
}

func serviceWorkDir(s service) string {
	dir := serviceDir(s.Name)
	if s.Kind == "db" {
		return dir
	}
	return filepath.Join(dir, "repo")
}

func saveService(s service) error {
	dir := serviceDir(s.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600)
}

func loadService(name string) (service, error) {
	data, err := os.ReadFile(filepath.Join(serviceDir(name), "state.json"))
	if err != nil {
		return service{}, fmt.Errorf("service %q not found", name)
	}
	var s service
	if err := json.Unmarshal(data, &s); err != nil {
		return service{}, fmt.Errorf("bad state for %q: %w", name, err)
	}
	return s, nil
}

func listServices() ([]service, error) {
	entries, err := os.ReadDir(filepath.Join(rootDir(), "services"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []service
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := loadService(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
