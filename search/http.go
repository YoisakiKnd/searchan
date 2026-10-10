package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"searchan/internal/httpx"
)

const maxBody = 8 << 20

func (b *base) do(ctx context.Context, method, endpoint string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: invalid HTTP request", b.name)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "searchan/0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := b.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// net/url errors embed the query URL; keep queries and credentials out of logs.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s: transport error: %w", b.name, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &HTTPError{Provider: b.name, StatusCode: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: reading response: %w", b.name, err)
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("%s: response exceeds 8 MiB", b.name)
	}
	return data, nil
}

func (b *base) json(ctx context.Context, method, endpoint string, payload any, headers map[string]string, out any) error {
	err := httpx.JSON(ctx, b.client, method, endpoint, headers, payload, out, maxBody)
	var status *httpx.StatusError
	if errors.As(err, &status) {
		return &HTTPError{Provider: b.name, StatusCode: status.Code, RetryAfter: status.RetryAfter}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", b.name, err)
	}
	return nil
}

func withQuery(endpoint string, values url.Values) string {
	u, _ := url.Parse(endpoint) // validated by constructor
	q := u.Query()
	for k, vs := range values {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String()
}
