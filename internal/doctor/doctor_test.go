package doctor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SleepingCat/PlexLink/internal/kinopoisk"
)

func TestPoiskKinoStatusIsReportedWithoutReturningDoctorError(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{"available", http.StatusOK, `{"requestsLimit":100,"requestsUsed":10,"requestsRemaining":90,"ttl":60,"resetAt":"soon"}`, "ok requests=10/100 remaining=90"},
		{"optional outage", http.StatusServiceUnavailable, `provider body`, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			got := PoiskKinoStatus(context.Background(), kinopoisk.NewClient(server.URL, "secret", server.Client()))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("status=%q, want substring %q", got, tc.want)
			}
			if strings.Contains(got, "provider body") || strings.Contains(got, "secret") {
				t.Fatalf("unsafe doctor status: %q", got)
			}
		})
	}
}
