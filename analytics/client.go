// Package analytics provides raw-REST clients for the three Google APIs that
// Adlytica needs: Analytics Data (GA4), Search Console and Google Ads.
//
// Every client returns rows keyed by the *requested field names* (the same
// names the app already asked Windsor.ai for), so callers never have to know
// about the wire format of a specific API.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"analytics-mcp/auth"

	"golang.org/x/oauth2"
)

// TokenSourceFromContext builds an auto-refreshing token source from the
// request context: the shared service-account source in S2S mode, or the
// caller's Google token in OAuth mode.
func TokenSourceFromContext(ctx context.Context) (oauth2.TokenSource, error) {
	if saTS := auth.GetSATokenSource(ctx); saTS != nil {
		return saTS, nil
	}
	tokenInfo := auth.GetTokenInfo(ctx)
	if tokenInfo == nil || tokenInfo.GoogleToken == nil {
		return nil, fmt.Errorf("not authenticated - link your Google account first")
	}
	return auth.NewAutoRefreshTokenSource(
		auth.GetTokenStore(ctx),
		tokenInfo.AccessToken,
		auth.GetGoogleProvider(ctx).Config(),
		tokenInfo.GoogleToken,
	), nil
}

// Client is a shared HTTP client for the Google APIs, bound to one request.
type Client struct {
	HTTP *http.Client
}

// NewClient builds an API client for the current request.
func NewClient(ctx context.Context) (*Client, error) {
	ts, err := TokenSourceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return &Client{HTTP: oauth2.NewClient(ctx, ts)}, nil
}

// postJSON sends a JSON body and decodes the JSON response into out.
func (c *Client) postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("google api %s returned HTTP %d: %s", endpointName(url), resp.StatusCode, truncate(string(raw), 900))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", endpointName(url), err)
	}
	return nil
}

func endpointName(url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	}
	if i := strings.IndexAny(url, "?"); i >= 0 {
		url = url[:i]
	}
	return url
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// num converts an API value (usually a string) into a number for JSON output.
// Values the API leaves empty stay nil so the app can fall back.
func num(v any) any {
	switch value := v.(type) {
	case nil:
		return nil
	case float64:
		return value
	case int:
		return float64(value)
	case string:
		s := strings.TrimSpace(value)
		if s == "" {
			return nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		// Google returns 64-bit counters as strings precisely because they can
		// exceed float precision; parse as integer where possible.
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return float64(i)
		}
		return nil
	default:
		return v
	}
}

// micros converts a *micros field to whole currency units, matching the shape
// the app already expects from Windsor (`spend` in euro, not micros).
func micros(v any) any {
	n := num(v)
	f, ok := n.(float64)
	if !ok {
		return nil
	}
	return round2(f / 1e6)
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// dateRange holds an inclusive start/end pair in YYYY-MM-DD.
type dateRange struct {
	from string
	to   string
}

func newDateRange(from, to string) (dateRange, error) {
	dr := dateRange{from: strings.TrimSpace(from), to: strings.TrimSpace(to)}
	if dr.from == "" || dr.to == "" {
		return dr, fmt.Errorf("date_from and date_to are required (YYYY-MM-DD)")
	}
	for _, d := range []string{dr.from, dr.to} {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return dr, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", d)
		}
	}
	return dr, nil
}

// toStringSlice normalises a JSON array/list of field names.
func toStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
