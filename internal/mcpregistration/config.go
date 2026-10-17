// Package mcpregistration projects the central Studio MCP discovery contract.
// It registers this application's domain only and never publishes credentials.
package mcpregistration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const Domain = "world"
const ServicesPrefix = "/verdantflare/mcp/services/"

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}
type ServiceRegistration struct {
	Domain         string           `json:"domain"`
	Endpoint       string           `json:"endpoint"`
	HealthEndpoint string           `json:"health_endpoint"`
	Version        string           `json:"version"`
	UpdatedAt      string           `json:"updated_at"`
	Tools          []ToolDefinition `json:"tools"`
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var toolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
var dnsPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

func internalHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return dnsPattern.MatchString(host) && !strings.Contains(host, "..") && (strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".svc.cluster.local"))
}
func cleanURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || !internalHost(u.Hostname()) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || strings.ContainsAny(raw, "\\\r\n\t ") || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, false
	}
	return u, true
}
func registration(address, version string, tools []ToolDefinition) (ServiceRegistration, error) {
	u, ok := cleanURL(address)
	if !ok || u.Path != "/mcp" || !versionPattern.MatchString(version) {
		return ServiceRegistration{}, errors.New("invalid MCP registration")
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme != "https" && (ip == nil || !ip.IsLoopback()) {
		return ServiceRegistration{}, errors.New("MCP non-loopback advertisement requires TLS")
	}
	u.Path = "/healthz"
	r := ServiceRegistration{Domain: Domain, Endpoint: address, HealthEndpoint: u.String(), Version: version, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Tools: tools}
	if !validRegistration(ServicesPrefix+Domain, r) {
		return ServiceRegistration{}, errors.New("invalid MCP tool declaration")
	}
	return r, nil
}
func validRegistration(key string, r ServiceRegistration) bool {
	if key != ServicesPrefix+Domain || r.Domain != Domain || !versionPattern.MatchString(r.Version) || len(r.Tools) == 0 {
		return false
	}
	u, ok := cleanURL(r.Endpoint)
	if !ok || u.Path != "/mcp" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme != "https" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	u.Path = "/healthz"
	if r.HealthEndpoint != u.String() {
		return false
	}
	if _, err := time.Parse(time.RFC3339, r.UpdatedAt); err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, t := range r.Tools {
		if !toolPattern.MatchString(t.Name) || !strings.HasPrefix(t.Name, Domain+".") || t.Description == "" || t.InputSchema == nil || seen[t.Name] {
			return false
		}
		seen[t.Name] = true
	}
	_, err := json.Marshal(r)
	return err == nil
}
func endpoints(raw string) ([]string, error) {
	out := []string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errors.New("invalid registry endpoints")
		}
		if !strings.Contains(entry, "://") {
			entry = "http://" + entry
		}
		u, ok := cleanURL(entry)
		if !ok || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("invalid registry endpoints")
		}
		out = append(out, entry)
	}
	return out, nil
}

// Start is optional only when both registration settings are absent. Shutdown
// revokes the publication's lease before closing its private client.
func Start(ctx context.Context, address, registryEndpoints, version string, tools []ToolDefinition) (*Publication, error) {
	if address == "" && registryEndpoints == "" {
		return nil, nil
	}
	if address == "" || registryEndpoints == "" {
		return nil, errors.New("MCP address and registry endpoints are required together")
	}
	r, err := registration(address, version, tools)
	if err != nil {
		return nil, err
	}
	eps, err := endpoints(registryEndpoints)
	if err != nil {
		return nil, err
	}
	config, err := registryClientConfig(eps, os.Getenv)
	if err != nil {
		return nil, err
	}
	cli, err := clientv3.New(config)
	if err != nil {
		return nil, errors.New("registry client unavailable")
	}
	p, err := Register(ctx, cli, []ServiceRegistration{r})
	if err != nil {
		_ = cli.Close()
		return nil, err
	}
	p.closeClient = cli.Close
	return p, nil
}

type registryClient interface {
	Grant(context.Context, int64) (*clientv3.LeaseGrantResponse, error)
	Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error)
	Txn(context.Context) clientv3.Txn
	KeepAlive(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)
	Watch(context.Context, string, ...clientv3.OpOption) clientv3.WatchChan
	Revoke(context.Context, clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error)
}
