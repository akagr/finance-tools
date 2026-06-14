package ibkr

// Flex Web Service client (M6): pull an Activity Flex statement online instead of
// downloading the XML by hand. The protocol is two GET requests:
//
//  1. SendRequest?t=<token>&q=<queryId>&v=3  -> a FlexStatementResponse with a
//     Status, a ReferenceCode, and the GetStatement Url (or an error).
//  2. <Url>?t=<token>&q=<referenceCode>&v=3  -> the FlexQueryResponse statement,
//     or a FlexStatementResponse with error code 1019 ("generation in progress")
//     while IBKR builds it — in which case we poll.
//
// The token is created in Client Portal (Settings → Flex Web Service) and the
// query is an Activity Flex Query id.

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/akagr/tax-tools/schedule-fa/internal/model"
)

const defaultFlexBaseURL = "https://ndcdyn.interactivebrokers.com/AccountManagement/FlexWebService"

// statementInProgressCode is IBKR's "try again shortly" error code.
const statementInProgressCode = "1019"

// FlexClient calls the IBKR Flex Web Service.
type FlexClient struct {
	HTTP      *http.Client
	BaseURL   string
	Version   int
	MaxPolls  int           // attempts to retrieve the generated statement
	PollDelay time.Duration // wait between GetStatement polls
}

// NewFlexClient returns a client with sensible defaults.
func NewFlexClient() *FlexClient {
	return &FlexClient{
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		BaseURL:   defaultFlexBaseURL,
		Version:   3,
		MaxPolls:  12,
		PollDelay: 5 * time.Second,
	}
}

// flexControl is the SendRequest/GetStatement control envelope (not the
// statement itself, whose root is FlexQueryResponse).
type flexControl struct {
	XMLName       xml.Name `xml:"FlexStatementResponse"`
	Status        string   `xml:"Status"`
	ReferenceCode string   `xml:"ReferenceCode"`
	URL           string   `xml:"Url"`
	ErrorCode     string   `xml:"ErrorCode"`
	ErrorMessage  string   `xml:"ErrorMessage"`
}

// Fetch runs the full flow and returns the raw statement XML, polling while IBKR
// generates it. The returned bytes can be passed to ParseFlexXML.
func (c *FlexClient) Fetch(ctx context.Context, token, queryID string) ([]byte, error) {
	if token == "" || queryID == "" {
		return nil, fmt.Errorf("ibkr: flex token and query id are both required")
	}
	ref, statementURL, err := c.sendRequest(ctx, token, queryID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < c.MaxPolls; attempt++ {
		stmt, inProgress, err := c.getStatement(ctx, statementURL, token, ref)
		if err != nil {
			return nil, err
		}
		if !inProgress {
			return stmt, nil
		}
		if attempt < c.MaxPolls-1 {
			if err := sleepCtx(ctx, c.PollDelay); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("ibkr: statement still generating after %d polls", c.MaxPolls)
}

func (c *FlexClient) sendRequest(ctx context.Context, token, queryID string) (refCode, statementURL string, err error) {
	body, err := c.get(ctx, c.BaseURL+"/SendRequest", token, queryID)
	if err != nil {
		return "", "", err
	}
	var ctl flexControl
	if err := xml.Unmarshal(body, &ctl); err != nil {
		return "", "", fmt.Errorf("ibkr: parse SendRequest response: %w", err)
	}
	if !strings.EqualFold(ctl.Status, "Success") {
		return "", "", flexError("SendRequest", ctl)
	}
	if ctl.ReferenceCode == "" {
		return "", "", fmt.Errorf("ibkr: SendRequest succeeded but returned no reference code")
	}
	statementURL = ctl.URL
	if statementURL == "" {
		statementURL = c.BaseURL + "/GetStatement"
	}
	return ctl.ReferenceCode, statementURL, nil
}

func (c *FlexClient) getStatement(ctx context.Context, statementURL, token, refCode string) (statement []byte, inProgress bool, err error) {
	body, err := c.get(ctx, statementURL, token, refCode)
	if err != nil {
		return nil, false, err
	}
	// A control envelope means error or still-generating; anything else is the
	// statement (root FlexQueryResponse).
	if rootElement(body) == "FlexStatementResponse" {
		var ctl flexControl
		if err := xml.Unmarshal(body, &ctl); err != nil {
			return nil, false, fmt.Errorf("ibkr: parse GetStatement response: %w", err)
		}
		if ctl.ErrorCode == statementInProgressCode ||
			strings.Contains(strings.ToLower(ctl.ErrorMessage), "generation in progress") {
			return nil, true, nil
		}
		if !strings.EqualFold(ctl.Status, "Success") {
			return nil, false, flexError("GetStatement", ctl)
		}
	}
	return body, false, nil
}

func (c *FlexClient) get(ctx context.Context, endpoint, t, q string) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	qy := u.Query()
	qy.Set("t", t)
	qy.Set("q", q)
	qy.Set("v", strconv.Itoa(c.Version))
	u.RawQuery = qy.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// IBKR rejects requests without a User-Agent.
	req.Header.Set("User-Agent", "schedulefa/0.1")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ibkr: flex http %d from %s", resp.StatusCode, endpoint)
	}
	return io.ReadAll(resp.Body)
}

// FetchAndParse fetches the statement online and parses it for the given year.
func FetchAndParse(ctx context.Context, token, queryID string, year int) (*model.Statement, error) {
	c := NewFlexClient()
	body, err := c.Fetch(ctx, token, queryID)
	if err != nil {
		return nil, err
	}
	return ParseFlexXML(bytes.NewReader(body), year)
}

func flexError(op string, ctl flexControl) error {
	msg := ctl.ErrorMessage
	if msg == "" {
		msg = "unknown error"
	}
	if ctl.ErrorCode != "" {
		return fmt.Errorf("ibkr: flex %s failed: %s (code %s)", op, msg, ctl.ErrorCode)
	}
	return fmt.Errorf("ibkr: flex %s failed: %s", op, msg)
}

func rootElement(b []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
