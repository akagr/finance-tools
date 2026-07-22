// Package yahoo is a tiny client for the Yahoo Finance chart API (the public,
// key-less JSON endpoint at query1.finance.yahoo.com/v8/finance/chart). It
// returns daily bars of the close for a symbol over a date range. Weekends and
// holidays are simply absent. NSE symbols use a ".NS" suffix (e.g. NIFTYBEES.NS);
// indices are prefixed with "^" (e.g. ^NSEI for the Nifty 50).
package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBaseURL = "https://query1.finance.yahoo.com/v8/finance/chart"

// Bar is one trading day's close.
type Bar struct {
	Date  string // YYYY-MM-DD (UTC)
	Close float64
}

// Client calls the Yahoo Finance chart API.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

// NewClient returns a client with sensible defaults.
func NewClient() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		BaseURL: defaultBaseURL,
	}
}

// chartResponse mirrors the subset of the chart JSON we consume.
type chartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

// Chart returns daily bars for symbol over [start, end] (inclusive by calendar
// day, interpreted in UTC). Days with a null close are skipped. A symbol Yahoo
// doesn't recognise yields an empty slice, not an error.
func (c *Client) Chart(ctx context.Context, symbol string, start, end time.Time) ([]Bar, error) {
	if symbol == "" {
		return nil, fmt.Errorf("yahoo: empty symbol")
	}
	p1 := dayStartUTC(start).Unix()
	p2 := dayStartUTC(end).Unix() + 86400 // exclusive upper bound: include end day

	u := c.baseURL() + "/" + url.PathEscape(symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("period1", strconv.FormatInt(p1, 10))
	q.Set("period2", strconv.FormatInt(p2, 10))
	q.Set("interval", "1d")
	req.URL.RawQuery = q.Encode()
	// Yahoo rejects requests without a browser-like User-Agent.
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo: chart %s: http %d", symbol, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var cr chartResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("yahoo: chart %s: %w", symbol, err)
	}
	if len(cr.Chart.Result) == 0 {
		return nil, nil
	}
	res := cr.Chart.Result[0]
	if len(res.Indicators.Quote) == 0 {
		return nil, nil
	}
	closes := res.Indicators.Quote[0].Close
	bars := make([]Bar, 0, len(res.Timestamp))
	for i, ts := range res.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue
		}
		day := time.Unix(ts, 0).UTC().Format("2006-01-02")
		bars = append(bars, Bar{Date: day, Close: *closes[i]})
	}
	return bars, nil
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func dayStartUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
