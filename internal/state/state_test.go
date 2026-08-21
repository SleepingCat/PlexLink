package state

import (
	"reflect"
	"testing"
)

func TestVerifiedResolutionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := VerifiedResolution{TMDBID: 8973, Kind: "tv", Title: "Show", Year: 1996, ActualAIModel: "provider/model", Files: map[string]VerifiedFileMapping{"Show.S01E01.mkv": {State: "RESOLVED", Season: 1, Episode: 1}}}
	if err := SaveVerified(dir, "ABCDEF", want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Verified(dir, "abcdef")
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestMissingVerifiedResolution(t *testing.T) {
	_, ok, err := Verified(t.TempDir(), "missing")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
