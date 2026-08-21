package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

func Do(ctx context.Context, client *http.Client, makeRequest func() (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := makeRequest()
		if err != nil {
			return nil, err
		}
		req = req.WithContext(ctx)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if !transient(resp.StatusCode) {
			return resp, nil
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		d := time.Duration(1<<attempt) * 200 * time.Millisecond
		if v := resp.Header.Get("Retry-After"); v != "" {
			if n, e := strconv.Atoi(v); e == nil {
				d = time.Duration(n) * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	return nil, fmt.Errorf("request failed after transient retries")
}
func transient(code int) bool { return code == 429 || code == 502 || code == 503 || code == 504 }
