// Package ad is the Active Directory evidence provider (Phase 1): a
// read-only LDAP(S) bind, a computer-object pull, observations into
// host_observations, and hosts upserted via the matching rules in
// docs/architecture.md §Phase 1 (GUID -> hostname -> insert).
//
// Read-only by design, like every provider. It must never import
// internal/device or anything on the SSH/exec path.
//
// The wire protocol is go-ldap's responsibility; this package programs
// against a tiny client interface so tests feed it recorded LDAP entries
// without a live directory.
package ad

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/farshidmousavii/bidar/internal/domain"
	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/internal/providers"
	"github.com/farshidmousavii/bidar/internal/store"
)

// Config is the AD provider's own configuration (provider isolation rule).
type Config struct {
	URL          string // ldap://host or ldaps://host
	BindDN       string
	BindPassword string
	BaseDN       string
	// Timeout bounds the connection and each LDAP operation.
	Timeout time.Duration
}

// ConfigFromEnv loads Config from the BIDAR_AD_* environment variables
// (pinned in docs/roadmap.md Phase 1 task 4). Fails loudly on a missing
// required value — a silently wrong directory config is worse than no
// config.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		URL:    strings.TrimSpace(os.Getenv(envconfig.ADURL)),
		BindDN: strings.TrimSpace(os.Getenv(envconfig.ADBindDN)),
		BaseDN: strings.TrimSpace(os.Getenv(envconfig.ADBaseDN)),
		// BindPassword is intentionally not trimmed: passwords may
		// legitimately have surrounding whitespace.
		BindPassword: os.Getenv(envconfig.ADBindPassword),
		Timeout:      15 * time.Second,
	}
	var missing []string
	for _, v := range []struct{ name, val string }{
		{envconfig.ADURL, cfg.URL},
		{envconfig.ADBindDN, cfg.BindDN},
		{envconfig.ADBindPassword, cfg.BindPassword},
		{envconfig.ADBaseDN, cfg.BaseDN},
	} {
		if v.val == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("AD provider configuration incomplete; set %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// client is the minimal LDAP surface the provider uses, so tests can
// substitute a fake. *ldap.Conn satisfies it.
type client interface {
	Bind(username, password string) error
	Search(req *ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// dialFunc opens (and must not yet bind) a connection for cfg.
type dialFunc func(ctx context.Context, cfg Config) (client, error)

// Provider implements providers.Provider for Active Directory.
type Provider struct {
	cfg    Config
	store  *store.Store
	logger *slog.Logger
	dial   dialFunc

	health providers.Health
}

// New returns an AD provider bound to the given store and logger, using
// real LDAP dialing.
func New(cfg Config, st *store.Store, logger *slog.Logger) (*Provider, error) {
	return newWithDialer(cfg, st, logger, realDial)
}

// newWithDialer is New with an injectable dialer (tests pass a fake).
func newWithDialer(cfg Config, st *store.Store, logger *slog.Logger, dial dialFunc) (*Provider, error) {
	if cfg.URL == "" || cfg.BindDN == "" || cfg.BaseDN == "" {
		return nil, fmt.Errorf("ad: incomplete config (url, bind_dn, base_dn are required)")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{cfg: cfg, store: st, logger: logger, dial: dial}, nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return "ad" }

// Health implements providers.Provider: outcome of the last Run.
func (p *Provider) Health() providers.Health { return p.health }

// Run implements providers.Provider: bind, pull computer objects, write
// observations, upsert hosts. Any failure (unreachable directory, bad
// bind, search error) is returned wrapped and recorded in Health — it
// never panics and never touches anything outside the AD code path.
func (p *Provider) Run(ctx context.Context) (providers.Result, error) {
	p.health = providers.Health{LastRunAt: time.Now()}

	if err := ctx.Err(); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("ad run cancelled: %w", err)
	}

	conn, err := p.dial(ctx, p.cfg)
	if err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("ad connect: %w", err)
	}
	defer conn.Close()

	if err := conn.Bind(p.cfg.BindDN, p.cfg.BindPassword); err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("ad bind: %w", err)
	}

	req := &ldap.SearchRequest{
		BaseDN:       p.cfg.BaseDN,
		Scope:        ldap.ScopeWholeSubtree,
		Filter:       "(objectClass=computer)",
		Attributes:   []string{"cn", "dNSHostName", "distinguishedName", "objectGUID", "objectSid", "lastLogonTimestamp"},
		SizeLimit:    0,
		TimeLimit:    int(p.cfg.Timeout.Seconds()),
		TypesOnly:    false,
		DerefAliases: ldap.NeverDerefAliases,
	}
	res, err := conn.Search(req)
	if err != nil {
		p.fail(err)
		return providers.Result{}, fmt.Errorf("ad search: %w", err)
	}

	now := time.Now().UTC()
	for _, entry := range res.Entries {
		select {
		case <-ctx.Done():
			p.fail(ctx.Err())
			return providers.Result{}, fmt.Errorf("ad run cancelled: %w", ctx.Err())
		default:
		}

		host, skip, err := p.normalizeAndUpsert(ctx, entry, now)
		if err != nil {
			p.fail(err)
			return providers.Result{}, fmt.Errorf("ad entry %s: %w", entry.DN, err)
		}
		if skip {
			continue
		}

		detail, err := json.Marshal(map[string]any{
			"dn":          entry.DN,
			"object_guid": host.ADObjectGUID,
			"object_sid":  host.ADObjectSID,
			"ou":          host.ADOU,
			"domain":      host.ADDomain,
		})
		if err != nil {
			p.fail(err)
			return providers.Result{}, fmt.Errorf("ad marshal detail: %w", err)
		}

		obs := &domain.Observation{
			HostID:     &host.ID,
			Source:     "ad",
			Hostname:   host.Hostname,
			Detail:     detail,
			ObservedAt: now,
		}
		if err := p.store.InsertObservation(ctx, obs); err != nil {
			p.fail(err)
			return providers.Result{}, err
		}
	}

	p.health = providers.Health{Healthy: true, LastRunAt: time.Now()}
	return providers.Result{ItemsFound: len(res.Entries)}, nil
}

// normalizeAndUpsert applies the Phase 1 matching rules for one AD entry:
//  1. match by objectGUID if already linked;
//  2. else match by hostname (case-insensitive);
//  3. else insert a new host.
//
// Returns the (created-or-updated) host, or skip=true for entries that
// carry no usable identity (no cn and no dNSHostName).
func (p *Provider) normalizeAndUpsert(ctx context.Context, entry *ldap.Entry, now time.Time) (*domain.Host, bool, error) {
	cn := entry.GetAttributeValue("cn")
	if cn == "" {
		cn = shortHostname(entry.GetAttributeValue("dNSHostName"))
	}
	if cn == "" {
		return nil, true, nil // no usable identity: record nothing
	}

	guid, err := parseGUID(rawAttr(entry, "objectGUID"))
	if err != nil {
		// A computer without a parseable GUID is unusable for stable
		// matching — skip rather than create an unlinkable row.
		p.logger.Warn("ad: skipping entry without parseable objectGUID", "dn", entry.DN, "err", err)
		return nil, true, nil
	}
	sid, err := parseSID(rawAttr(entry, "objectSid"))
	if err != nil {
		return nil, true, fmt.Errorf("parse objectSid: %w", err)
	}
	lastLogon, err := parseFileTime(entry.GetAttributeValue("lastLogonTimestamp"))
	if err != nil {
		return nil, true, fmt.Errorf("parse lastLogonTimestamp: %w", err)
	}

	ou, adDomain := splitDN(entry.GetAttributeValue("distinguishedName"))

	h := &domain.Host{
		Hostname:      &cn,
		FQDN:          nullableString(entry.GetAttributeValue("dNSHostName")),
		ADDomain:      nullableString(adDomain),
		ADOU:          nullableString(ou),
		ADObjectGUID:  &guid,
		ADObjectSID:   nullableString(sid),
		ADLastLogonAt: lastLogon,
		ADStatus:      "known",
		MatchStatus:   "matched",
		LastADSyncAt:  &now,
	}

	// Rule 1: GUID already linked?
	existing, err := p.store.FindHostByGUID(ctx, guid)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, true, err
	}
	// Rule 2: hostname match?
	if existing == nil {
		existing, err = p.store.FindHostByHostname(ctx, cn)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, true, err
		}
	}

	if existing != nil {
		h.ID = existing.ID
		if err := p.store.UpdateHostFromAD(ctx, existing.ID, h); err != nil {
			return nil, true, err
		}
		return h, false, nil
	}

	// Rule 3: fresh identity — insert.
	id, err := p.store.InsertHost(ctx, h)
	if err != nil {
		return nil, true, err
	}
	h.ID = id
	return h, false, nil
}

func (p *Provider) fail(err error) {
	p.health.Healthy = false
	p.health.LastError = err.Error()
}

// realDial opens the LDAP connection with the configured timeout. ctx is
// honoured at entry and via the deadline-derived timeout.
func realDial(ctx context.Context, cfg Config) (client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	conn, err := ldap.DialURL(cfg.URL, ldap.DialWithDialer(&net.Dialer{Timeout: timeout}))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(timeout)
	return conn, nil
}

// -- helpers ---------------------------------------------------------------

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// rawAttr returns the first byte value of a named attribute (AD returns
// objectGUID/objectSid as binary values; go-ldap stores them in
// EntryAttribute.Bytes).
func rawAttr(e *ldap.Entry, name string) []byte {
	for _, attr := range e.Attributes {
		if attr.Name == name && len(attr.ByteValues) > 0 {
			return attr.ByteValues[0]
		}
	}
	return nil
}

// shortHostname strips the DNS domain from an FQDN.
func shortHostname(fqdn string) string {
	if i := strings.IndexByte(fqdn, '.'); i >= 0 {
		return fqdn[:i]
	}
	return fqdn
}

// splitDN derives the OU path and the DNS domain from a distinguishedName
// like "CN=PC1,OU=IT,OU=Floor 1,DC=corp,DC=local":
//   - ou     = "IT/Floor 1" (RDNs between the CN and the DCs, in order)
//   - domain = "corp.local" (DC components in DN order: most specific
//     first, exactly how AD presents them)
//
// Naive split on commas: RDN values containing escaped commas are not
// handled yet (flag before relying on them for weird OUs).
func splitDN(dn string) (ou, domain string) {
	var ous, dcs []string
	for _, rdn := range strings.Split(dn, ",") {
		kv := strings.SplitN(strings.TrimSpace(rdn), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch strings.ToUpper(kv[0]) {
		case "OU":
			ous = append(ous, kv[1])
		case "DC":
			dcs = append(dcs, kv[1])
		}
	}
	return strings.Join(ous, "/"), strings.Join(dcs, ".")
}

// parseGUID converts AD's 16-byte objectGUID (mixed-endian: first three
// groups little-endian) into canonical UUID form.
func parseGUID(raw []byte) (string, error) {
	if len(raw) != 16 {
		return "", fmt.Errorf("objectGUID is %d bytes, want 16", len(raw))
	}
	var u [16]byte
	copy(u[:], raw)
	// AD stores the first three fields little-endian.
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		binary.LittleEndian.Uint32(u[0:4]),
		binary.LittleEndian.Uint16(u[4:6]),
		binary.LittleEndian.Uint16(u[6:8]),
		u[8], u[9], u[10], u[11], u[12], u[13], u[14], u[15]), nil
}

// parseSID converts a binary SID into SDDL form ("S-1-5-21-...").
func parseSID(raw []byte) (string, error) {
	if len(raw) < 8 {
		return "", fmt.Errorf("objectSid is %d bytes, want >= 8", len(raw))
	}
	revision := int(raw[0])
	subAuthCount := int(raw[1])
	if len(raw) != 8+subAuthCount*4 {
		return "", fmt.Errorf("objectSid length %d does not match %d sub-authorities", len(raw), subAuthCount)
	}
	var authority uint64
	for i := 0; i < 6; i++ {
		authority = authority<<8 | uint64(raw[2+i])
	}
	sid := fmt.Sprintf("S-%d-%d", revision, authority)
	for i := 0; i < subAuthCount; i++ {
		sub := binary.LittleEndian.Uint32(raw[8+i*4:])
		sid += fmt.Sprintf("-%d", sub)
	}
	return sid, nil
}

// parseFileTime converts AD's lastLogonTimestamp (Windows FILETIME:
// 100ns intervals since 1601-01-01 UTC) into a time.Time. The value is
// absent for accounts that never logged on.
func parseFileTime(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var ticks uint64
	if _, err := fmt.Sscanf(s, "%d", &ticks); err != nil {
		return nil, fmt.Errorf("invalid FILETIME %q: %w", s, err)
	}
	const unixEpochDiff = 11644473600 // seconds between 1601-01-01 and 1970-01-01
	secs := int64(ticks/10_000_000) - unixEpochDiff
	t := time.Unix(secs, int64(ticks%10_000_000)*100).UTC()
	return &t, nil
}
