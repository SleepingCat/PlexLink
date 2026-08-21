package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type VerifiedResolution struct {
	TMDBID        int                            `json:"tmdb_id"`
	Kind          string                         `json:"kind"`
	Title         string                         `json:"title"`
	Year          int                            `json:"year"`
	ActualAIModel string                         `json:"actual_ai_model,omitempty"`
	Files         map[string]VerifiedFileMapping `json:"files,omitempty"`
}

type VerifiedFileMapping struct {
	State   string `json:"state"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

func Verified(directory, hash string) (VerifiedResolution, bool, error) {
	path := filepath.Join(directory, "verified-resolutions", strings.ToLower(hash)+".json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return VerifiedResolution{}, false, nil
	}
	if err != nil {
		return VerifiedResolution{}, false, fmt.Errorf("read verified resolution: %w", err)
	}
	var result VerifiedResolution
	if err := json.Unmarshal(b, &result); err != nil {
		return VerifiedResolution{}, false, fmt.Errorf("parse verified resolution: %w", err)
	}
	if result.TMDBID <= 0 {
		return VerifiedResolution{}, false, fmt.Errorf("parse verified resolution: invalid TMDB ID")
	}
	return result, true, nil
}

func SaveVerified(directory, hash string, resolution VerifiedResolution) error {
	if resolution.TMDBID <= 0 {
		return errors.New("verified resolution requires a TMDB ID")
	}
	dir := filepath.Join(directory, "verified-resolutions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(resolution, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, strings.ToLower(hash)+".json"), b, 0o600)
}

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
