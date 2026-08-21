package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type resolutions struct {
	Hashes map[string]int `yaml:"hashes"`
}

func Resolution(directory, hash string) (int, error) {
	b, err := os.ReadFile(filepath.Join(directory, "resolutions.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read resolutions: %w", err)
	}
	var r resolutions
	if err := yaml.Unmarshal(b, &r); err != nil {
		return 0, fmt.Errorf("parse resolutions: %w", err)
	}
	return r.Hashes[strings.ToLower(hash)], nil
}
func SaveResolution(directory, hash string, id int) error {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	path := filepath.Join(directory, "resolutions.yaml")
	r := resolutions{Hashes: map[string]int{}}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &r); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if r.Hashes == nil {
		r.Hashes = map[string]int{}
	}
	r.Hashes[strings.ToLower(hash)] = id
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
