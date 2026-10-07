// Package apiclient is a Go client for berth's API (internal/api). Its methods (client_gen.go) are
// generated from the API's route table: go run ./tools/genapi.
package apiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ar4mirez/berth/internal/ops"
)

// Client talks to a `berth serve`.
type Client struct {
	// Base is the URL before /v1: "http://berth" on the socket (the host is ignored).
	Base string
	// Token is the bearer token, for TCP.
	Token string
	HTTP  *http.Client
}

// Unix is a client on a server's Unix socket.
func Unix(socket string) *Client {
	return &Client{Base: "http://berth", HTTP: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, accept string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	u := c.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("berth's API isn't answering (is `berth serve` running?): %w", err)
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, apiError(resp)
	}
	return resp, nil
}

// apiError is a failed response as the error the operation would have returned here.
func apiError(resp *http.Response) error {
	var doc ops.ErrorDoc
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &doc) != nil || doc.Message == "" {
		return fmt.Errorf("berth's API answered %s", resp.Status)
	}
	return &ops.Error{Kind: doc.Kind, Code: doc.Code, Msg: doc.Message, Hint: doc.Hint}
}

// do sends a request and decodes its document into out.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	resp, err := c.request(ctx, method, path, query, body, "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(out)
}

// Stream sends a request for an event stream, and calls on for each event (start, step, output,
// done, failed: berth.event/v1; then result, with the document). A "failed" event is the error
// Stream returns.
func (c *Client) Stream(ctx context.Context, method, path string, body any, on func(event string, data json.RawMessage)) error {
	resp, err := c.request(ctx, method, path, nil, body, "text/event-stream")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var event string
	var failed error
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := json.RawMessage(strings.TrimPrefix(line, "data: "))
			if event == ops.EventFailed {
				var e struct{ Error *ops.ErrorDoc }
				var doc ops.ErrorDoc
				if json.Unmarshal(data, &e) == nil && e.Error != nil {
					doc = *e.Error
				} else {
					_ = json.Unmarshal(data, &doc)
				}
				failed = &ops.Error{Kind: doc.Kind, Code: doc.Code, Msg: doc.Message, Hint: doc.Hint}
			}
			if on != nil {
				on(event, data)
			}
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return failed
}

// Info is `GET /v1`: what is serving, and what this client may do.
func (c *Client) Info(ctx context.Context) (out struct {
	Schema, Version  string
	ReadOnly         bool `json:"read_only"`
	Writes, Restarts bool
}, err error) {
	return out, c.do(ctx, "GET", "/v1", nil, nil, &out)
}
