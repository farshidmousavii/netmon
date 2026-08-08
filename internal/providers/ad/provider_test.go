package ad

// Provider integration tests: a fake LDAP client serves recorded computer
// entries (real-shaped AD attribute values) while the provider writes to a
// real Postgres — gated on BIDAR_TEST_DATABASE_URL, same pattern as the
// Phase 0 integration tests. The LDAP wire protocol itself is go-ldap's
// tested responsibility; this covers our bind/search/parse/matching/write
// logic end to end.

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshidmousavii/bidar/internal/envconfig"
	"github.com/farshidmousavii/bidar/internal/store"
	"github.com/farshidmousavii/bidar/internal/testdb"
)

// fakeClient implements the ad.client interface against recorded entries.
// A single instance is shared across every dial so tests can assert on the
// requests the provider actually made.
type fakeClient struct {
	entries     []*ldap.Entry
	dialErr     error
	bindErr     error
	searchErr   error
	boundDN     string
	boundPass   string
	searchReq   *ldap.SearchRequest
	searchCalls int
}

func (f *fakeClient) Bind(username, password string) error {
	f.boundDN, f.boundPass = username, password
	return f.bindErr
}

func (f *fakeClient) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searchCalls++
	f.searchReq = req
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return &ldap.SearchResult{Entries: f.entries}, nil
}

func (f *fakeClient) Close() error { return nil }

// fakeDial returns a dialFunc that always hands back the same fakeClient.
func fakeDial(entries []*ldap.Entry, dialErr error) (dialFunc, *fakeClient) {
	fc := &fakeClient{entries: entries, dialErr: dialErr}
	return func(ctx context.Context, cfg Config) (client, error) {
		if fc.dialErr != nil {
			return nil, fc.dialErr
		}
		return fc, nil
	}, fc
}

// computerEntry builds a recorded AD computer object with realistic
// attribute values. Pass nil for guid/sid to omit the binary attributes.
func computerEntry(dn, cn, fqdn string, guid []byte, sid []byte, lastLogon string) *ldap.Entry {
	e := &ldap.Entry{
		DN: dn,
		Attributes: []*ldap.EntryAttribute{
			{Name: "cn", Values: []string{cn}},
			{Name: "dNSHostName", Values: []string{fqdn}},
			{Name: "distinguishedName", Values: []string{dn}},
			{Name: "lastLogonTimestamp", Values: []string{lastLogon}},
		},
	}
	if len(guid) > 0 {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: "objectGUID", ByteValues: [][]byte{guid}})
	}
	if len(sid) > 0 {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: "objectSid", ByteValues: [][]byte{sid}})
	}
	return e
}

// guid16 encodes a UUID's first three groups little-endian, exactly as AD
// serializes objectGUID on the wire.
func guid16(a, b, c uint32, tail [8]byte) []byte {
	raw := make([]byte, 16)
	binary.LittleEndian.PutUint32(raw[0:4], a)
	binary.LittleEndian.PutUint16(raw[4:6], uint16(b))
	binary.LittleEndian.PutUint16(raw[6:8], uint16(c))
	copy(raw[8:], tail[:])
	return raw
}

func sidBytes(subs ...uint32) []byte {
	raw := make([]byte, 8+len(subs)*4)
	raw[0], raw[1] = 1, byte(len(subs))
	copy(raw[2:8], []byte{0, 0, 0, 0, 0, 5})
	for i, s := range subs {
		binary.LittleEndian.PutUint32(raw[8+i*4:], s)
	}
	return raw
}

// testHarness wires the provider against a real (scratch) Postgres and a
// shared fake client.
type testHarness struct {
	provider *Provider
	fake     *fakeClient
	pool     *pgxpool.Pool
}

func newTestHarness(t *testing.T, entries []*ldap.Entry, dialErr error) *testHarness {
	t.Helper()
	// Each test gets its own scratch database so concurrent package
	// binaries never wipe each other's tables.
	url := testdb.ScratchURL(t, testdb.BaseURL(t))
	pool := testdb.Open(t, url)

	dial, fake := fakeDial(entries, dialErr)
	p, err := newWithDialer(Config{
		URL:          "ldap://fake.invalid",
		BindDN:       "CN=svc,DC=corp,DC=local",
		BindPassword: "secret",
		BaseDN:       "DC=corp,DC=local",
		Timeout:      5 * time.Second,
	}, store.New(pool), slog.Default(), dial)
	if err != nil {
		t.Fatalf("newWithDialer: %v", err)
	}
	return &testHarness{provider: p, fake: fake, pool: pool}
}

func (h *testHarness) countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestRunInsertsHostsAndObservations(t *testing.T) {
	h := newTestHarness(t, []*ldap.Entry{
		computerEntry("CN=PC1,OU=IT,DC=corp,DC=local", "PC1", "pc1.corp.local",
			guid16(0x7ab2e864, 0x1c5e, 0x4ada, [8]byte{0x9c, 0x2f, 0x8b, 0x2f, 0x4f, 0xe8, 0x20, 0x1f}),
			sidBytes(21, 397955417, 626881126, 188441444, 512),
			"133371313317887634"),
		computerEntry("CN=SRV1,OU=SERVERS,DC=corp,DC=local", "SRV1", "srv1.corp.local",
			guid16(0x11111111, 0x2222, 0x3333, [8]byte{0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb}),
			sidBytes(1000, 2000),
			""),
	}, nil)

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsFound != 2 {
		t.Errorf("ItemsFound = %d, want 2", res.ItemsFound)
	}
	if !h.provider.Health().Healthy {
		t.Errorf("Health = %+v, want healthy", h.provider.Health())
	}

	// Bind + search request shape.
	if h.fake.boundDN != "CN=svc,DC=corp,DC=local" || h.fake.boundPass != "secret" {
		t.Errorf("bind = %q/%q", h.fake.boundDN, h.fake.boundPass)
	}
	if h.fake.searchReq == nil {
		t.Fatal("search was not performed")
	}
	if h.fake.searchReq.BaseDN != "DC=corp,DC=local" || h.fake.searchReq.Filter != "(objectClass=computer)" {
		t.Errorf("search = %q %q", h.fake.searchReq.BaseDN, h.fake.searchReq.Filter)
	}

	// Host rows.
	ctx := context.Background()
	pc1, err := h.provider.store.FindHostByHostname(ctx, "pc1")
	if err != nil {
		t.Fatalf("find pc1: %v", err)
	}
	if pc1.ADObjectGUID == nil || *pc1.ADObjectGUID != "7ab2e864-1c5e-4ada-9c2f-8b2f4fe8201f" {
		t.Errorf("pc1 guid = %v", pc1.ADObjectGUID)
	}
	if pc1.ADObjectSID == nil || *pc1.ADObjectSID != "S-1-5-21-397955417-626881126-188441444-512" {
		t.Errorf("pc1 sid = %v", pc1.ADObjectSID)
	}
	if pc1.ADOU == nil || *pc1.ADOU != "IT" || pc1.ADDomain == nil || *pc1.ADDomain != "corp.local" {
		t.Errorf("pc1 ou/domain = %v/%v", pc1.ADOU, pc1.ADDomain)
	}
	if pc1.FQDN == nil || *pc1.FQDN != "pc1.corp.local" {
		t.Errorf("pc1 fqdn = %v", pc1.FQDN)
	}
	if pc1.ADStatus != "known" || pc1.MatchStatus != "matched" {
		t.Errorf("pc1 status = %q/%q", pc1.ADStatus, pc1.MatchStatus)
	}
	if pc1.ADLastLogonAt == nil {
		t.Error("pc1 lastLogon should be set")
	}

	srv1, err := h.provider.store.FindHostByHostname(ctx, "srv1")
	if err != nil {
		t.Fatalf("find srv1: %v", err)
	}
	if srv1.ADLastLogonAt != nil {
		t.Errorf("srv1 lastLogon = %v, want nil (never logged on)", srv1.ADLastLogonAt)
	}

	// Observations: one per entry, linked to the matched host.
	if got := h.countRows(t, "host_observations"); got != 2 {
		t.Errorf("observations = %d, want 2", got)
	}
	var linked int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM host_observations WHERE source = 'ad' AND host_id IS NOT NULL`).Scan(&linked); err != nil {
		t.Fatalf("count linked: %v", err)
	}
	if linked != 2 {
		t.Errorf("linked observations = %d, want 2", linked)
	}
}

func TestRunIdempotent(t *testing.T) {
	h := newTestHarness(t, []*ldap.Entry{
		computerEntry("CN=PC1,DC=corp,DC=local", "PC1", "pc1.corp.local",
			guid16(1, 2, 3, [8]byte{4, 5, 6, 7, 8, 9, 10, 11}), sidBytes(500), ""),
	}, nil)

	for i := 0; i < 2; i++ {
		if _, err := h.provider.Run(context.Background()); err != nil {
			t.Fatalf("Run %d: %v", i+1, err)
		}
	}
	if got := h.countRows(t, "hosts"); got != 1 {
		t.Errorf("hosts after 2 runs = %d, want 1 (GUID match must not duplicate)", got)
	}
}

func TestRunHostnameMatchLinksNewGUID(t *testing.T) {
	h := newTestHarness(t, []*ldap.Entry{
		computerEntry("CN=PC1,DC=corp,DC=local", "PC1", "pc1.corp.local",
			guid16(1, 2, 3, [8]byte{4, 5, 6, 7, 8, 9, 10, 11}), sidBytes(500), ""),
	}, nil)
	if _, err := h.provider.Run(context.Background()); err != nil {
		t.Fatalf("Run 1: %v", err)
	}

	// Same hostname, new GUID (object recreated in AD): rule 2 hostname
	// match must update the existing row, not insert a second one.
	h2 := newTestHarness(t, []*ldap.Entry{
		computerEntry("CN=PC1,DC=corp,DC=local", "PC1", "pc1.corp.local",
			guid16(9, 9, 9, [8]byte{8, 8, 8, 8, 8, 8, 8, 8}), sidBytes(501), ""),
	}, nil)
	if _, err := h2.provider.Run(context.Background()); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	if got := h2.countRows(t, "hosts"); got != 1 {
		t.Fatalf("hosts after hostname match = %d, want 1", got)
	}
	byGUID, err := h2.provider.store.FindHostByGUID(context.Background(), "00000009-0009-0009-0808-080808080808")
	if err != nil {
		t.Fatalf("new GUID not linked after hostname match: %v", err)
	}
	if byGUID.Hostname == nil || *byGUID.Hostname != "PC1" {
		t.Errorf("linked host hostname = %v, want PC1", byGUID.Hostname)
	}
}

func TestRunSkipsEntryWithoutGUID(t *testing.T) {
	h := newTestHarness(t, []*ldap.Entry{
		computerEntry("CN=GHOST,DC=corp,DC=local", "GHOST", "ghost.corp.local",
			nil, sidBytes(1), ""),
	}, nil)

	res, err := h.provider.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.countRows(t, "hosts"); got != 0 {
		t.Errorf("hosts = %d, want 0 for GUID-less entry", got)
	}
	if res.ItemsFound != 1 {
		t.Errorf("ItemsFound = %d, want 1 (directory entries returned)", res.ItemsFound)
	}
}

func TestRunUnreachableDirectory(t *testing.T) {
	h := newTestHarness(t, nil, errors.New("connection refused"))

	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable directory")
	}
	if !strings.Contains(err.Error(), "ad connect") {
		t.Errorf("error should be wrapped as ad connect, got: %v", err)
	}
	if h.provider.Health().Healthy {
		t.Error("Health should be unhealthy after connect failure")
	}
	if h.provider.Health().LastError == "" {
		t.Error("Health.LastError should record the failure")
	}
}

func TestRunBindFailure(t *testing.T) {
	h := newTestHarness(t, nil, nil)
	h.fake.bindErr = errors.New("invalid credentials")

	_, err := h.provider.Run(context.Background())
	if err == nil {
		t.Fatal("expected bind error")
	}
	if h.provider.Health().Healthy {
		t.Errorf("Health should be unhealthy, got %+v", h.provider.Health())
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Run("missing values", func(t *testing.T) {
		t.Setenv(envconfig.ADURL, "")
		t.Setenv(envconfig.ADBindDN, "")
		t.Setenv(envconfig.ADBindPassword, "")
		t.Setenv(envconfig.ADBaseDN, "")
		_, err := ConfigFromEnv()
		if err == nil {
			t.Fatal("expected error for missing AD config")
		}
		if !strings.Contains(err.Error(), envconfig.ADURL) {
			t.Errorf("error should name the missing var, got: %v", err)
		}
	})

	t.Run("complete", func(t *testing.T) {
		t.Setenv(envconfig.ADURL, "ldaps://dc01")
		t.Setenv(envconfig.ADBindDN, "CN=svc,DC=corp,DC=local")
		t.Setenv(envconfig.ADBindPassword, "pw")
		t.Setenv(envconfig.ADBaseDN, "DC=corp,DC=local")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv: %v", err)
		}
		if cfg.URL != "ldaps://dc01" || cfg.BaseDN != "DC=corp,DC=local" {
			t.Errorf("cfg = %+v", cfg)
		}
	})
}
