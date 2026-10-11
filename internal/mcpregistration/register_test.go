package mcpregistration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func toolsFixture() []ToolDefinition {
	return []ToolDefinition{{Name: Domain + ".list", Description: "List visible items", InputSchema: map[string]any{"type": "object"}}}
}
func registrationFixture(t *testing.T) ServiceRegistration {
	t.Helper()
	r, err := registration("https://"+Domain+".studio.svc.cluster.local:8091/mcp", "0.1.0", toolsFixture())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestRegistrationClosedInternalAddressAndDomain(t *testing.T) {
	r := registrationFixture(t)
	if r.Domain != Domain || r.HealthEndpoint != "https://"+Domain+".studio.svc.cluster.local:8091/healthz" {
		t.Fatal("domain or health projection mismatch")
	}
	for _, address := range []string{"https://public.example/mcp", "http://project.studio.svc:8091/mcp", "https://name:credential@project.studio.svc/mcp", "https://project.studio.svc/mcp?token=secret", "https://project.studio.svc/%6dcp", "https://project.studio.svc/mcp#fragment", "https://project.studio.svc/mcp?", "https://project.studio.svc/other"} {
		if _, err := registration(address, "0.1.0", toolsFixture()); err == nil {
			t.Fatal("invalid advertisement accepted")
		}
	}
	if _, err := registration("http://127.0.0.1:8091/mcp", "0.1.0", toolsFixture()); err != nil {
		t.Fatal(err)
	}
	if _, err := registration(r.Endpoint, "latest", toolsFixture()); err == nil {
		t.Fatal("mutable version accepted")
	}
	foreign := toolsFixture()
	foreign[0].Name = "foreign.list"
	if _, err := registration(r.Endpoint, "0.1.0", foreign); err == nil {
		t.Fatal("foreign domain tool accepted")
	}
	r.Domain = "foreign"
	if validRegistration(ServicesPrefix+Domain, r) {
		t.Fatal("foreign registration key accepted")
	}
}
func TestOptionalRegistrationRequiresBothSettings(t *testing.T) {
	if p, err := Start(context.Background(), "", "", "0.1.0", toolsFixture()); p != nil || err != nil {
		t.Fatal("absent config should remain disabled")
	}
	for _, pair := range [][2]string{{"https://project.studio.svc/mcp", ""}, {"", "http://etcd.studio.svc:2379"}} {
		if _, err := Start(context.Background(), pair[0], pair[1], "0.1.0", toolsFixture()); err == nil {
			t.Fatal("half config accepted")
		}
	}
	for _, endpoint := range []string{"http://name:credential@etcd.studio.svc:2379", "https://public.example:2379", "http://etcd.studio.svc:2379?token=secret", "http://etcd.studio.svc:2379/unknown", "http://etcd.studio.svc:2379,"} {
		if _, err := endpoints(endpoint); err == nil {
			t.Fatal("untrusted registry endpoint accepted")
		}
	}
	if _, err := endpoints("etcd.studio.svc.cluster.local:2379, http://127.0.0.1:2379"); err != nil {
		t.Fatal(err)
	}
}

type fakeRegistry struct {
	mu        sync.Mutex
	current   *mvccpb.KeyValue
	granted   clientv3.LeaseID
	revoked   []clientv3.LeaseID
	checks    []clientv3.Cmp
	writes    []clientv3.Op
	succeeded bool
}

func (f *fakeRegistry) Grant(context.Context, int64) (*clientv3.LeaseGrantResponse, error) {
	return &clientv3.LeaseGrantResponse{ID: f.granted, TTL: 300}, nil
}
func (f *fakeRegistry) Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &clientv3.GetResponse{}
	if f.current != nil {
		copy := &mvccpb.KeyValue{Key: append([]byte{}, f.current.Key...), Value: append([]byte{}, f.current.Value...), ModRevision: f.current.ModRevision, Lease: f.current.Lease}
		r.Kvs = []*mvccpb.KeyValue{copy}
	}
	return r, nil
}
func (f *fakeRegistry) Txn(context.Context) clientv3.Txn { return &fakeTxn{f: f} }
func (f *fakeRegistry) KeepAlive(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	return make(chan *clientv3.LeaseKeepAliveResponse), nil
}
func (f *fakeRegistry) Watch(context.Context, string, ...clientv3.OpOption) clientv3.WatchChan {
	return make(chan clientv3.WatchResponse)
}
func (f *fakeRegistry) Revoke(_ context.Context, lease clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, lease)
	if f.current != nil && f.current.Lease == int64(lease) {
		f.current = nil
	}
	return &clientv3.LeaseRevokeResponse{}, nil
}

type fakeTxn struct{ f *fakeRegistry }

func (tx *fakeTxn) If(checks ...clientv3.Cmp) clientv3.Txn  { tx.f.checks = checks; return tx }
func (tx *fakeTxn) Then(writes ...clientv3.Op) clientv3.Txn { tx.f.writes = writes; return tx }
func (tx *fakeTxn) Else(...clientv3.Op) clientv3.Txn        { return tx }
func (tx *fakeTxn) Commit() (*clientv3.TxnResponse, error) {
	f := tx.f
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.checks) != 1 || len(f.writes) != 1 {
		return nil, errors.New("expected one domain CAS")
	}
	if f.succeeded {
		op := f.writes[0]
		f.current = &mvccpb.KeyValue{Key: op.KeyBytes(), Value: op.ValueBytes(), ModRevision: 8, Lease: int64(f.granted)}
	}
	return &clientv3.TxnResponse{Succeeded: f.succeeded, Header: &etcdserverpb.ResponseHeader{Revision: 8}}, nil
}
func TestPublicationClosesOnlyItsLeaseAfterReplacement(t *testing.T) {
	f := &fakeRegistry{granted: 11, succeeded: true}
	p, err := Register(context.Background(), f, []ServiceRegistration{registrationFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.checks) != 1 || string(f.writes[0].KeyBytes()) != ServicesPrefix+Domain {
		t.Fatal("registration escaped its domain")
	}
	var published ServiceRegistration
	if json.Unmarshal(f.writes[0].ValueBytes(), &published) != nil || published.Domain != Domain || published.Version != "0.1.0" || len(published.Tools) != 1 {
		t.Fatal("invalid publication")
	}
	f.mu.Lock()
	f.current.Lease = 22
	f.mu.Unlock()
	p.Close()
	p.Close()
	if len(f.revoked) != 1 || f.revoked[0] != 11 || f.current == nil || f.current.Lease != 22 {
		t.Fatal("shutdown revoked another instance")
	}
}
func TestPublicationRefusesForeignEndpointAndConcurrentOverwrite(t *testing.T) {
	r := registrationFixture(t)
	prior := r
	prior.Endpoint = "https://other.studio.svc:8091/mcp"
	data, _ := json.Marshal(prior)
	f := &fakeRegistry{granted: 11, succeeded: true, current: &mvccpb.KeyValue{Key: []byte(ServicesPrefix + Domain), Value: data, Lease: 22, ModRevision: 7}}
	if _, _, _, err := publish(context.Background(), f, []ServiceRegistration{r}); err == nil {
		t.Fatal("overwrote another endpoint")
	}
	if f.current == nil || f.current.Lease != 22 || len(f.writes) != 0 || len(f.revoked) != 1 {
		t.Fatal("failed registration affected existing publication")
	}
	data, _ = json.Marshal(r)
	f = &fakeRegistry{granted: 11, succeeded: false, current: &mvccpb.KeyValue{Key: []byte(ServicesPrefix + Domain), Value: data, Lease: 22, ModRevision: 7}}
	if _, _, _, err := publish(context.Background(), f, []ServiceRegistration{r}); err == nil {
		t.Fatal("accepted failed CAS")
	}
	if f.current == nil || f.current.Lease != 22 || len(f.revoked) != 1 || f.revoked[0] != 11 {
		t.Fatal("CAS failure revoked the existing lease")
	}
}
