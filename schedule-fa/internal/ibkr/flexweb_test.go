package ibkr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFlexClientFetchPolls(t *testing.T) {
	sample, err := os.ReadFile("testdata/sample_flex.xml")
	if err != nil {
		t.Fatal(err)
	}

	var srvURL string
	var getCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SendRequest":
			if r.URL.Query().Get("t") != "TOKEN" || r.URL.Query().Get("q") != "Q1" {
				t.Errorf("SendRequest params = %v", r.URL.Query())
			}
			w.Write([]byte(`<FlexStatementResponse><Status>Success</Status>` +
				`<ReferenceCode>REF123</ReferenceCode><Url>` + srvURL + `/GetStatement</Url></FlexStatementResponse>`))
		case "/GetStatement":
			if r.URL.Query().Get("q") != "REF123" {
				t.Errorf("GetStatement ref = %q", r.URL.Query().Get("q"))
			}
			getCalls++
			if getCalls == 1 { // first poll: still generating
				w.Write([]byte(`<FlexStatementResponse><Status>Warn</Status><ErrorCode>1019</ErrorCode>` +
					`<ErrorMessage>Statement generation in progress. Please try again shortly.</ErrorMessage></FlexStatementResponse>`))
				return
			}
			w.Write(sample)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	c := NewFlexClient()
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL
	c.PollDelay = time.Millisecond
	c.MaxPolls = 5

	body, err := c.Fetch(context.Background(), "TOKEN", "Q1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if getCalls != 2 {
		t.Errorf("GetStatement calls = %d, want 2 (one in-progress, one success)", getCalls)
	}
	st, err := ParseFlexXML(strings.NewReader(string(body)), 2024)
	if err != nil {
		t.Fatalf("parse fetched statement: %v", err)
	}
	if st.Account.Number != "U1234567" {
		t.Errorf("account = %q, want U1234567", st.Account.Number)
	}
}

func TestFlexClientSendRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<FlexStatementResponse><Status>Fail</Status>` +
			`<ErrorCode>1012</ErrorCode><ErrorMessage>Token has expired.</ErrorMessage></FlexStatementResponse>`))
	}))
	defer srv.Close()

	c := NewFlexClient()
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	_, err := c.Fetch(context.Background(), "TOKEN", "Q1")
	if err == nil || !strings.Contains(err.Error(), "Token has expired") {
		t.Fatalf("want token-expired error, got %v", err)
	}
}

func TestFlexClientRequiresCredentials(t *testing.T) {
	if _, err := NewFlexClient().Fetch(context.Background(), "", "Q1"); err == nil {
		t.Error("expected error when token is empty")
	}
}
