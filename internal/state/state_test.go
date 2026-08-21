package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestVerifiedResolutionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := VerifiedResolution{ResolutionSchemaVersion: ResolutionSchemaVersion, ScoringVersion: ScoringVersion, EpisodeMappingVersion: EpisodeMappingVersion, TMDBID: 8973, Kind: "tv", Title: "Show", Year: 1996, ActualAIModel: "provider/model", Files: map[string]VerifiedFileMapping{"Show.S01E01.mkv": {State: "RESOLVED", Season: 1, Episode: 1}}}
	if err := SaveVerified(dir, "ABCDEF", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Verified(dir, "abcdef")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestIncompatibleVerifiedResolutionIsCacheMiss(t *testing.T) {
	for _, mutate := range []func(*VerifiedResolution){
		func(value *VerifiedResolution) { value.ResolutionSchemaVersion = "old" },
		func(value *VerifiedResolution) { value.ScoringVersion = "old" },
		func(value *VerifiedResolution) { value.EpisodeMappingVersion = "old" },
	} {
		dir := t.TempDir()
		value := VerifiedResolution{TMDBID: 1}
		if err := SaveVerified(dir, "hash", value); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "verified-resolutions", "hash.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		mutate(&value)
		data, _ = json.Marshal(value)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, hit, err := Verified(dir, "hash"); err != nil || hit {
			t.Fatalf("hit=%v err=%v value=%+v", hit, err, value)
		}
	}
}

func TestMissingVerifiedResolution(t *testing.T) {
	_, ok, err := Verified(t.TempDir(), "missing")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
