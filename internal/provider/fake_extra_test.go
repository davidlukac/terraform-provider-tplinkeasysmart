package provider

import (
	"context"
	"errors"
	"sync"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// extraState holds the fake switch behaviour for the device description and
// the configuration backup. It lives beside fakeClient (keyed by pointer) so
// the main fake stays small.
type extraState struct {
	mu              sync.Mutex
	active          bool // when false, Info falls back to the static fake data
	hostname        string
	setCalls        int
	setErr          error
	ignoreSet       bool  // accept the write but keep the old name, like a device that silently ignores a setting
	mutateIP        bool  // simulate a write that corrupts the management IP
	infoErr         error // Info fails
	infoErrAfterSet bool  // Info starts failing once a write happened (read-back fails)
	backup          []byte
	backupErr       error
	ip              string
}

var (
	extrasMu sync.Mutex
	extras   = map[*fakeClient]*extraState{}
)

func (f *fakeClient) extra() *extraState {
	extrasMu.Lock()
	defer extrasMu.Unlock()
	if e, ok := extras[f]; ok {
		return e
	}
	e := &extraState{ip: "192.0.2.120"}
	extras[f] = e
	return e
}

func (e *extraState) info() (escp.Info, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.infoErr != nil {
		return escp.Info{}, true, e.infoErr
	}
	if !e.active {
		return escp.Info{}, false, nil
	}
	return escp.Info{Model: "TL-SG108E", Hostname: e.hostname, MAC: "00:00:5e:00:53:01",
		IP: e.ip, Netmask: "255.255.255.0", Gateway: "192.0.2.1", DHCP: true}, true, nil
}

func (f *fakeClient) SetHostname(_ context.Context, name string) error {
	if err := escp.ValidateHostname(name); err != nil {
		return err
	}
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setCalls++
	if e.infoErrAfterSet {
		e.infoErr = errBoom
	}
	if e.setErr != nil {
		return e.setErr
	}
	if !e.ignoreSet {
		e.hostname = name
	}
	if e.mutateIP {
		e.ip = "192.0.2.250"
	}
	return nil
}

func (f *fakeClient) ConfigBackup(context.Context) ([]byte, error) {
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backupErr != nil {
		return nil, e.backupErr
	}
	return append([]byte(nil), e.backup...), nil
}

// newHostnameFake returns a fake whose device description starts as name.
func newHostnameFake(name string) *fakeClient {
	f := &fakeClient{}
	e := f.extra()
	e.active, e.hostname = true, name
	return f
}

var errBoom = errors.New("boom")
