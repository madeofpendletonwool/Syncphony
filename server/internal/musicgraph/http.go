// SPDX-License-Identifier: AGPL-3.0-only

package musicgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// requestTimeout bounds one request, including the wait for our turn.
	requestTimeout = 20 * time.Second
	maxResponse    = 4 << 20
	// backOffFor is how long a source that said we're too fast is left
	// alone, when it doesn't say.
	backOffFor = 5 * time.Second
)

// client calls one source's web API, keeping to its rate limit across
// every caller.
type client struct {
	name      string
	userAgent string
	http      *http.Client
	interval  time.Duration
	// header is sent with every request (a token, say).
	header http.Header

	mu   sync.Mutex
	next time.Time // when the next request may go
}

func newClient(name, userAgent string, hc *http.Client, interval time.Duration) *client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &client{name: name, userAgent: userAgent, http: hc, interval: max(interval, 0), header: http.Header{}}
}

// wait blocks until it's our turn to send a request.
func (c *client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	at := now
	if c.next.After(now) {
		at = c.next
	}
	c.next = at.Add(c.interval)
	c.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// backOff pushes the next request back.
func (c *client) backOff(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := time.Now().Add(d); until.After(c.next) {
		c.next = until
	}
}

// get fetches u and hands the response to read, which sees every status
// but a throttle (429, 503), so a source whose errors come in the body can
// read them. The body is limited to maxResponse.
func (c *client) get(ctx context.Context, u string, params url.Values, read func(status int, body io.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := c.wait(ctx); err != nil {
		return err
	}
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	for k, vs := range c.header {
		req.Header[k] = vs
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", c.name, provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		d := backOffFor
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			d = time.Duration(s) * time.Second
		}
		c.backOff(d)
		return fmt.Errorf("%s: %w", c.name, &provider.RateLimitError{RetryAfter: d})
	}
	return read(resp.StatusCode, io.LimitReader(resp.Body, maxResponse))
}

// getJSON fetches u and decodes a 200's JSON into v. A 404 is
// provider.ErrNotFound; anything else, provider.ErrUnavailable.
func (c *client) getJSON(ctx context.Context, u string, params url.Values, v any) error {
	return c.get(ctx, u, params, func(status int, body io.Reader) error {
		switch status {
		case http.StatusOK:
		case http.StatusNotFound:
			return fmt.Errorf("%s: %w", c.name, provider.ErrNotFound)
		default:
			return fmt.Errorf("%s: HTTP %d: %w", c.name, status, provider.ErrUnavailable)
		}
		if err := json.NewDecoder(body).Decode(v); err != nil {
			return fmt.Errorf("%s: decoding: %w: %w", c.name, provider.ErrUnavailable, err)
		}
		return nil
	})
}

// number is a JSON number that some APIs (Last.fm) send as a string.
type number float64

func (n *number) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*n = 0
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*n = number(f)
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = number(f)
	return nil
}
