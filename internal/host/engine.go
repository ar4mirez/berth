package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Engine is a minimal, read-only Docker engine API client over a host's Docker dialer. It covers
// what phase 0 needs (ping, container ports); richer reads come with the commands that use them.
type Engine struct {
	c *http.Client
}

// NewEngine returns an Engine that reaches the API through d.
func NewEngine(d Docker) *Engine {
	return &Engine{c: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.DialEngine(ctx) },
		// One engine per host and short-lived requests: don't keep idle connections (an ssh
		// channel each) open behind the caller's back.
		DisableKeepAlives: true,
	}}}
}

// Ping checks that the engine answers.
func (e *Engine) Ping(ctx context.Context) error {
	body, err := e.get(ctx, "/_ping")
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(string(body)); s != "OK" {
		return fmt.Errorf("docker engine: unexpected ping reply %q", s)
	}
	return nil
}

// GetJSON GETs an engine API path (e.g. "/containers/json?all=1") and decodes the reply into out.
func (e *Engine) GetJSON(ctx context.Context, path string, out any) error {
	body, err := e.get(ctx, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("docker engine %s: %w", path, err)
	}
	return nil
}

func (e *Engine) get(ctx context.Context, path string) ([]byte, error) {
	// The host part is ignored: DialContext always reaches the engine socket.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := e.c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker engine: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("docker engine %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker engine %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// publishedPorts lists host ports published by the engine's running containers.
func (e *Engine) publishedPorts(ctx context.Context) ([]int, error) {
	var cs []struct {
		Ports []struct {
			PublicPort int
			Type       string
		}
	}
	if err := e.GetJSON(ctx, "/containers/json", &cs); err != nil {
		return nil, err
	}
	var ports []int
	for _, c := range cs {
		for _, p := range c.Ports {
			if p.PublicPort > 0 && p.Type == "tcp" {
				ports = append(ports, p.PublicPort)
			}
		}
	}
	return ports, nil
}

// ErrEngineNotFound is the engine's 404: no such container, network or image.
var ErrEngineNotFound = errors.New("not found")

// Do sends method to an engine API path, with in as its JSON body (nil for none), and decodes a
// JSON reply into out (nil to drop it). A 404 is ErrEngineNotFound; 304 (already started or
// stopped) is not an error. Writes (#43) go through here.
func (e *Engine) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader = http.NoBody
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.c.Do(req)
	if err != nil {
		return fmt.Errorf("docker engine: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	reply, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("docker engine %s %s: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("docker engine %s %s: %w: %s", method, path, ErrEngineNotFound, engineMessage(reply))
	case resp.StatusCode == http.StatusNotModified:
		return nil
	case resp.StatusCode >= 300:
		return fmt.Errorf("docker engine %s %s: %s: %s", method, path, resp.Status, engineMessage(reply))
	}
	if out != nil && len(bytes.TrimSpace(reply)) > 0 {
		if err := json.Unmarshal(reply, out); err != nil {
			return fmt.Errorf("docker engine %s %s: %w", method, path, err)
		}
	}
	return nil
}

// Stream GETs an engine API path and hands its body to read until it ends (a followed log).
func (e *Engine) Stream(ctx context.Context, path string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := e.c.Do(req)
	if err != nil {
		return fmt.Errorf("docker engine: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("docker engine %s: %w: %s", path, ErrEngineNotFound, engineMessage(b))
		}
		return fmt.Errorf("docker engine %s: %s: %s", path, resp.Status, engineMessage(b))
	}
	return read(resp.Body)
}

// engineMessage is the engine's {"message": …}, or the body as it is.
func engineMessage(b []byte) string {
	var m struct{ Message string }
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(b))
}
