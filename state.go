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
	Name          string            `json:"name"`
	URL           string            `json:"url"`
	Image         string            `json:"image"`
	Port          int               `json:"port"`
	CreatedAt     time.Time         `json:"createdAt"`
	Env           map[string]string `json:"env,omitempty"`
	ContainerPort int               `json:"containerPort,omitempty"`
}

func (s service) internalPort() int {
	if s.ContainerPort != 0 {
		return s.ContainerPort
	}
	return 3000
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

func containerName(name string) string {
	return "orbital-" + name
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
	return os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644)
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

func nextPort(services []service) int {
	port := 8080
	for {
		used := false
		for _, s := range services {
			if s.Port == port {
				used = true
				break
			}
		}
		if !used {
			return port
		}
		port++
	}
}
