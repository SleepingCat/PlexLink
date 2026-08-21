package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Cache struct{ Directory string }

const CacheSchemaVersion = "1"

type cacheRecord struct {
	CacheSchemaVersion string  `json:"ai_cache_schema_version"`
	PromptVersion      string  `json:"prompt_version"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	ActualModel        string  `json:"actual_model,omitempty"`
	Request            Request `json:"request"`
	Result             Result  `json:"result"`
}

func Fingerprint(req Request, provider, model string) (string, error) {
	payload := struct {
		Prompt, Provider, Model string
		Request                 Request
	}{PromptVersion, provider, model, req}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal AI fingerprint: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (c Cache) Load(key string) (Result, bool, error) {
	b, err := os.ReadFile(filepath.Join(c.Directory, "ai-cache", key+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("read AI cache: %w", err)
	}
	var record cacheRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return Result{}, false, fmt.Errorf("decode AI cache: %w", err)
	}
	if record.CacheSchemaVersion != CacheSchemaVersion || record.PromptVersion != PromptVersion || !CacheableResult(record.Request, record.Result) {
		return Result{}, false, nil
	}
	record.Result.ActualModel = record.ActualModel
	return record.Result, true, nil
}

func (c Cache) Save(key, provider, model string, req Request, result Result) error {
	if !CacheableResult(req, result) {
		return nil
	}
	dir := filepath.Join(c.Directory, "ai-cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create AI cache: %w", err)
	}
	b, err := json.MarshalIndent(cacheRecord{CacheSchemaVersion, PromptVersion, provider, model, result.ActualModel, req, result}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode AI cache: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, key+".json"), b, 0o600)
}

func CacheableResult(req Request, result Result) bool {
	if result.Status != Resolved || result.Confidence <= 0 {
		return false
	}
	switch req.Task {
	case IdentifyMedia:
		return strings.TrimSpace(result.CanonicalTitle) != "" || len(result.LocalizedTitles) > 0 || len(result.SearchQueries) > 0
	case SelectCandidate:
		return result.SelectedTMDBID != nil
	case MapEpisodes:
		return len(result.EpisodeMappings) > 0
	default:
		return false
	}
}
