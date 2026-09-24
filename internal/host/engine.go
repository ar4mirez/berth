package host

import (
	"context"
	"encoding/json"
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
