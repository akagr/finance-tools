package yahoo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// sampleChart builds a chart JSON body with the given timestamp/close pairs.
// A nil close is emitted as JSON null.
func sampleChart(ts []int64, closes []*float64) string {
	var body chartResponse
	body.Chart.Result = make([]struct {
		Timestamp  []int64 `json:"timestamp"`
		Indicators struct {
			Quote []struct {
				Close []*float64 `json:"close"`
			} `json:"quote"`
		} `json:"indicators"`
	}, 1)
	body.Chart.Result[0].Timestamp = ts
	body.Chart.Result[0].Indicators.Quote = make([]struct {
		Close []*float64 `json:"close"`
	}, 1)
	body.Chart.Result[0].Indicators.Quote[0].Close = closes
	b, _ := json.Marshal(body)
	return string(b)
}

func f(v float64) *float64 { return &v }

func TestChartParsesBarsAndSkipsNulls(t *testing.T) {
	// 2024-06-03, 2024-06-04, 2024-06-05 (UTC); middle close is null.
	ts := []int64{1717372800, 1717459200, 1717545600}
	closes := []*float64{f(100.5), nil, f(102.25)}

	var gotPath, gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotQuery = r.URL.RawQuery
		w.Write([]byte(sampleChart(ts, closes)))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	start := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)
	bars, err := c.Chart(context.Background(), "VWRA.L", start, end)
	if err != nil {
		t.Fatalf("Chart: %v", err)
	}

	if gotPath != "/VWRA.L" {
		t.Errorf("path = %q, want /VWRA.L", gotPath)
	}
	if gotUA == "" {
		t.Error("expected a User-Agent header")
	}
	// period1 = start-of-1-Jun...; period2 = end-of-30-Jun + 1 day.
	wantP1 := strconv.FormatInt(start.Unix(), 10)
	wantP2 := strconv.FormatInt(end.Unix()+86400, 10)
	if q := "period1=" + wantP1; !containsQuery(gotQuery, q) {
		t.Errorf("query %q missing %q", gotQuery, q)
	}
	if q := "period2=" + wantP2; !containsQuery(gotQuery, q) {
		t.Errorf("query %q missing %q", gotQuery, q)
	}

	want := []Bar{
		{Date: "2024-06-03", Close: 100.5},
		{Date: "2024-06-05", Close: 102.25},
	}
	if len(bars) != len(want) {
		t.Fatalf("got %d bars, want %d: %+v", len(bars), len(want), bars)
	}
	for i := range want {
		if bars[i] != want[i] {
			t.Errorf("bar %d = %+v, want %+v", i, bars[i], want[i])
		}
	}
}

func TestChartEmptyResultIsNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"chart":{"result":null,"error":{"code":"Not Found"}}}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	bars, err := c.Chart(context.Background(), "NOPE", time.Now().AddDate(0, 0, -5), time.Now())
	if err != nil {
		t.Fatalf("Chart on unknown symbol should not error: %v", err)
	}
	if len(bars) != 0 {
		t.Errorf("got %d bars, want 0", len(bars))
	}
}

func TestChartHTTPErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	if _, err := c.Chart(context.Background(), "X", time.Now().AddDate(0, 0, -5), time.Now()); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
}

func TestChartEmptySymbol(t *testing.T) {
	c := NewClient()
	if _, err := c.Chart(context.Background(), "", time.Now(), time.Now()); err == nil {
		t.Fatal("expected an error for empty symbol")
	}
}

func containsQuery(raw, sub string) bool {
	for _, part := range splitAmp(raw) {
		if part == sub {
			return true
		}
	}
	return false
}

func splitAmp(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '&' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
