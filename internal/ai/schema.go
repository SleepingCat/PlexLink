package ai

func ResultSchema() map[string]any {
	stringArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 8}
	}
	mapping := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"source_file", "season", "episode", "confidence"}, "properties": map[string]any{
		"source_file": map[string]any{"type": "string"}, "season": map[string]any{"type": "integer", "minimum": 0}, "episode": map[string]any{"type": "integer", "minimum": 1}, "confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	}}
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"status", "media_type", "canonical_title", "localized_titles", "year", "season", "search_queries", "selected_tmdb_id", "episode_mappings", "confidence", "evidence_summary"},
		"properties": map[string]any{
			"status":          map[string]any{"type": "string", "enum": []string{"resolved", "ambiguous", "unknown"}},
			"media_type":      map[string]any{"type": "string", "enum": []string{"movie", "tv", "anime"}},
			"canonical_title": map[string]any{"type": "string"}, "localized_titles": stringArray(),
			"year": map[string]any{"type": "integer", "minimum": 0}, "season": map[string]any{"type": "integer", "minimum": 0},
			"search_queries": stringArray(), "selected_tmdb_id": map[string]any{"type": []string{"integer", "null"}},
			"episode_mappings": map[string]any{"type": "array", "items": mapping, "maxItems": 200},
			"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "evidence_summary": stringArray(),
		},
	}
}
