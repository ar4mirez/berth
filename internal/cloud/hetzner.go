// Package cloud provisions hosts for berth at a cloud provider (#52). The first one is Hetzner
// Cloud (docs/decisions/051-first-cloud-provider.md).
//
// It speaks the provider's HTTP API directly: the handful of calls berth needs don't justify an
// SDK in every berth binary, or a build tag that would leave it out of the released ones.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Labels on everything berth creates: how it finds its own resources again, and nobody else's.
const (
	LabelManaged = "berth.managed"
	LabelHost    = "berth.host"
)

// HetznerEndpoint is Hetzner Cloud's API.
const HetznerEndpoint = "https://api.hetzner.cloud/v1"

// MaxUserData is the largest cloud-init user data Hetzner takes.
const MaxUserData = 32 << 10

// Hetzner is a client for one project: a token is a project's.
type Hetzner struct {
	Token string
	// Endpoint is the API's base URL (HetznerEndpoint; a test's server).
	Endpoint string
	HTTP     *http.Client
}

// Server is a VM.
type Server struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Status string            `json:"status"`
	Labels map[string]string `json:"labels"`
	Public struct {
		IPv4 struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
	} `json:"public_net"`
}

// Firewall is a cloud firewall. berth's have no rules: nothing comes in.
type Firewall struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

// ServerSpec is a server to create.
type ServerSpec struct {
	Name, Type, Location, Image string
	UserData                    string
	Labels                      map[string]string
	Firewall                    int64
}

// APIError is the API saying no.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Hetzner: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

func (h *Hetzner) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	base := h.Endpoint
	if base == "" {
		base = HetznerEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Hetzner's API: %w", err) //nolint:staticcheck // a name
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		e := struct {
			Error APIError `json:"error"`
		}{}
		_ = json.Unmarshal(b, &e)
		e.Error.Status = resp.StatusCode
		if e.Error.Message == "" {
			e.Error.Message = strings.TrimSpace(string(b))
		}
		return &e.Error
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// selector is a label selector for berth's resources: all of them, or one host's.
func selector(host string) string {
	s := LabelManaged + "==true"
	if host != "" {
		s += "," + LabelHost + "==" + host
	}
	return url.QueryEscape(s)
}

// CreateFirewall makes a firewall with no rules, so nothing comes in.
func (h *Hetzner) CreateFirewall(ctx context.Context, name string, labels map[string]string) (Firewall, error) {
	var out struct {
		Firewall Firewall `json:"firewall"`
	}
	err := h.do(ctx, "POST", "/firewalls", map[string]any{"name": name, "labels": labels, "rules": []any{}}, &out)
	return out.Firewall, err
}

// CreateServer makes a server behind a firewall, with its user data.
func (h *Hetzner) CreateServer(ctx context.Context, s ServerSpec) (Server, error) {
	if len(s.UserData) > MaxUserData {
		return Server{}, fmt.Errorf("the cloud-init user data is %d bytes, and Hetzner takes %d", len(s.UserData), MaxUserData)
	}
	var out struct {
		Server Server `json:"server"`
	}
	err := h.do(ctx, "POST", "/servers", map[string]any{
		"name": s.Name, "server_type": s.Type, "location": s.Location, "image": s.Image,
		"user_data": s.UserData, "labels": s.Labels, "start_after_create": true,
		"firewalls": []any{map[string]any{"firewall": s.Firewall}},
	}, &out)
	return out.Server, err
}

// Servers are berth's servers: one host's, or all of them when host is "".
func (h *Hetzner) Servers(ctx context.Context, host string) ([]Server, error) {
	var out struct {
		Servers []Server `json:"servers"`
	}
	err := h.do(ctx, "GET", "/servers?per_page=50&label_selector="+selector(host), nil, &out)
	return out.Servers, err
}

// Firewalls are berth's firewalls: one host's, or all of them when host is "".
func (h *Hetzner) Firewalls(ctx context.Context, host string) ([]Firewall, error) {
	var out struct {
		Firewalls []Firewall `json:"firewalls"`
	}
	err := h.do(ctx, "GET", "/firewalls?per_page=50&label_selector="+selector(host), nil, &out)
	return out.Firewalls, err
}

// DeleteServer removes a server.
func (h *Hetzner) DeleteServer(ctx context.Context, id int64) error {
	return h.do(ctx, "DELETE", fmt.Sprintf("/servers/%d", id), nil, nil)
}

// DeleteFirewall removes a firewall. One still applied to a server that is being deleted is refused
// for a moment: it is tried again for up to wait.
func (h *Hetzner) DeleteFirewall(ctx context.Context, id int64, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := h.do(ctx, "DELETE", fmt.Sprintf("/firewalls/%d", id), nil, nil)
		var api *APIError
		if err == nil || !asAPI(err, &api) || api.Code != "resource_in_use" || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(2*time.Second, wait/4+time.Millisecond)):
		}
	}
}

func asAPI(err error, target **APIError) bool {
	e, ok := err.(*APIError) //nolint:errorlint // do returns it unwrapped
	if ok {
		*target = e
	}
	return ok
}

// Remove deletes everything of berth's with host's label (every host's when host is ""): servers
// first, then the firewalls they were behind. It returns what it removed.
func (h *Hetzner) Remove(ctx context.Context, host string, wait time.Duration) (removed []string, err error) {
	servers, err := h.Servers(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, s := range servers {
		if err := h.DeleteServer(ctx, s.ID); err != nil {
			return removed, fmt.Errorf("deleting server %s: %w", s.Name, err)
		}
		removed = append(removed, "server "+s.Name)
	}
	fws, err := h.Firewalls(ctx, host)
	if err != nil {
		return removed, err
	}
	for _, f := range fws {
		if err := h.DeleteFirewall(ctx, f.ID, wait); err != nil {
			return removed, fmt.Errorf("deleting firewall %s: %w", f.Name, err)
		}
		removed = append(removed, "firewall "+f.Name)
	}
	return removed, nil
}
