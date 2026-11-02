package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"

	"github.com/davidlukac/escp-go"
)

// switchSpec is one switch the provider is asked to talk to, with the environment fallbacks applied.
type switchSpec struct {
	name            string // "" for the provider's own switch
	host            string
	mac             string
	username        string
	password        string
	timeout         time.Duration
	allowUnverified bool
}

// label names the switch in diagnostics.
func (s switchSpec) label() string {
	if s.name == "" {
		return "the provider's own switch"
	}
	return s.name
}

// Configure builds the registry of switches: the provider's own switch (host,
// username, password, with environment fallbacks) as the default, and every
// entry of the `switches` map by name. Clients are created lazily; nothing
// touches the network here.
func (p *tplinkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	reportUnknownProviderValues(cfg, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var entries map[string]switchModel
	resp.Diagnostics.Append(cfg.Switches.ElementsAs(ctx, &entries, false)...)
	reportUnknownSwitchValues(entries, &resp.Diagnostics)
	own, hasOwn := ownSwitch(cfg, len(entries), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	providerAllow, err := envBoolOr(cfg.AllowUnverifiedFirmware, envAllowUnverified)
	if err != nil {
		resp.Diagnostics.AddAttributeError(pathRoot("allow_unverified_firmware"), "Invalid allow_unverified_firmware", err.Error())
		return
	}
	providerTimeout := timeoutOr(cfg.TimeoutSeconds, defaultTimeout)
	b := &registryBuilder{newClient: p.newClient, diags: &resp.Diagnostics, reg: &switchRegistry{named: map[string]SwitchClient{}}, seen: map[string]string{}}
	if hasOwn {
		own.timeout, own.allowUnverified = providerTimeout, providerAllow
		b.add(own)
	}
	for _, name := range sortedKeys(entries) {
		b.add(entrySpec(name, entries[name], providerTimeout, providerAllow))
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.ResourceData = b.reg
	resp.DataSourceData = b.reg
}

// reportUnknownProviderValues refuses a provider argument that is only known after apply (for
// example taken from another resource): it cannot be used to build a client during planning, and
// without a client every resource and data source would dereference nil, so the provider fails
// with an explanation instead.
func reportUnknownProviderValues(cfg providerModel, diags *diag.Diagnostics) {
	for _, attr := range []struct {
		name  string
		value attr.Value
	}{{"host", cfg.Host}, {"mac", cfg.MAC}, {"username", cfg.Username}, {"password", cfg.Password}, {"switches", cfg.Switches}} {
		if attr.value.IsUnknown() {
			diags.AddAttributeError(pathRoot(attr.name), "Unknown switch "+attr.name,
				"The provider cannot create the switch client because `"+attr.name+"` is not known yet. Apply the resource that provides it first "+
					"(`terraform apply -target=...`), set the value statically, or use the TPLINK_EASYSMART_"+strings.ToUpper(attr.name)+" environment variable.")
		}
	}
}

// reportUnknownSwitchValues is reportUnknownProviderValues for the entries of the `switches` map.
func reportUnknownSwitchValues(entries map[string]switchModel, diags *diag.Diagnostics) {
	for name, e := range entries {
		for _, f := range []struct {
			attr  string
			value attr.Value
		}{{"host", e.Host}, {"mac", e.MAC}, {"username", e.Username}, {"password", e.Password}} {
			if f.value.IsUnknown() {
				diags.AddAttributeError(pathRoot("switches").AtMapKey(name).AtName(f.attr), "Unknown switch "+f.attr,
					"The provider cannot create the client for switch "+name+" because `"+f.attr+"` is not known yet; set it statically or apply the resource that provides it first.")
			}
		}
	}
}

// ownSwitch reads the provider's own switch (host or MAC, username and password, with the
// environment fallbacks) and reports what is missing. It is there when a host or MAC is given, or
// when the `switches` map is empty (then a missing host is the error to report).
func ownSwitch(cfg providerModel, entries int, diags *diag.Diagnostics) (switchSpec, bool) {
	own := switchSpec{
		host:     envOr(cfg.Host, envHost),
		mac:      envOr(cfg.MAC, envMAC),
		username: envOr(cfg.Username, envUsername),
		password: envOr(cfg.Password, envPassword),
	}
	present := own.host != "" || own.mac != "" || entries == 0
	if present && own.host == "" && own.mac == "" {
		diags.AddAttributeError(pathRoot("host"), "Missing switch host or MAC address",
			"Set `host` or `mac` (or TPLINK_EASYSMART_HOST / TPLINK_EASYSMART_MAC), or define switches with `switches`.")
	}
	if _, err := escp.ParseMAC(own.mac); own.mac != "" && err != nil {
		diags.AddAttributeError(pathRoot("mac"), "Invalid MAC address", err.Error())
	}
	if present && (own.username == "" || own.password == "") {
		diags.AddError("Missing credentials", "Set `username`/`password` or TPLINK_EASYSMART_USERNAME/TPLINK_EASYSMART_PASSWORD.")
	}
	return own, present
}

// entrySpec is the switch of one `switches` entry: its own timeout and firmware opt-in when it
// sets them, the provider's otherwise.
func entrySpec(name string, e switchModel, providerTimeout time.Duration, providerAllow bool) switchSpec {
	allow := providerAllow
	if !e.AllowUnverifiedFirmware.IsNull() {
		allow = e.AllowUnverifiedFirmware.ValueBool()
	}
	return switchSpec{
		name: name, host: e.Host.ValueString(), mac: e.MAC.ValueString(),
		username: e.Username.ValueString(), password: e.Password.ValueString(),
		timeout: timeoutOr(e.TimeoutSeconds, providerTimeout), allowUnverified: allow,
	}
}

// registryBuilder creates the clients of the switches and files them in the registry, reporting
// into the diagnostics. A device is never configured twice: two clients on one switch would compete.
type registryBuilder struct {
	newClient func(escp.Config) (SwitchClient, error)
	diags     *diag.Diagnostics
	reg       *switchRegistry
	seen      map[string]string // "host x" or "mac x" -> the switch that has it
}

// add creates the client of one switch.
func (b *registryBuilder) add(s switchSpec) {
	label := s.label()
	var hw escp.MAC
	var err error
	if s.mac != "" {
		hw, err = escp.ParseMAC(s.mac)
	}
	if err != nil { // the schema validator catches this earlier; the environment is checked here
		b.diags.AddError("Invalid MAC address", fmt.Sprintf("%s: %s", label, err))
		return
	}
	if !b.claim(label, hw, s.host) {
		return
	}
	c, err := b.newClient(escp.Config{Host: s.host, MAC: hw, Username: s.username, Password: escp.Password(s.password), Timeout: s.timeout, WriteGate: firmwareWriteGate(s.allowUnverified), Logger: newTflogLogger()})
	if err != nil {
		b.diags.AddError("Cannot configure switch client", fmt.Sprintf("%s: %s", label, err))
		return
	}
	if s.name == "" {
		b.reg.def = c
		return
	}
	b.reg.named[s.name] = c
}

// claim records the host and the MAC address of a switch and refuses one that another switch has.
func (b *registryBuilder) claim(label string, mac escp.MAC, host string) bool {
	for _, id := range []struct{ kind, value string }{{"host", host}, {"mac", mac.String()}} {
		if id.value == "" {
			continue
		}
		key := id.kind + " " + id.value
		if other, dup := b.seen[key]; dup {
			b.diags.AddError("Same switch configured twice", fmt.Sprintf("%s and %s have the same %s %q; two clients on one switch would compete for it.", other, label, id.kind, id.value))
			return false
		}
		b.seen[key] = label
	}
	return true
}
