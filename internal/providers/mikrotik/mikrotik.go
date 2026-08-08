// Package mikrotik is the single RouterOS API client in the codebase.
// Phase 1 uses it for DHCP lease reads (the DHCP provider); Phase 2
// extends this same package with ARP/wireless-registration/interface
// polling — never a second client talking to the same devices.
//
// Wire protocol: the RouterOS API (port 8728) — length-prefixed words,
// sentences terminated by an empty word, commands like
// "/ip/dhcp-server/lease/print" with "=key=value" parameters, responses
// as "!done" / "!re" (row) / "!trap" (error) sentences. Implemented with
// the standard library only.
//
// SECURITY NOTE: the plain RouterOS API is unencrypted; credentials cross
// the LAN in cleartext (except the legacy MD5 challenge path, which still
// only obfuscates the password). API-SSL (8729) is deliberately not
// implemented in Phase 1 — flag before any deployment that carries
// MikroTik credentials over untrusted segments.
package mikrotik

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"
)

// DefaultPort is the RouterOS API port.
const DefaultPort = 8728

// Config configures a RouterOS API connection.
type Config struct {
	Host     string
	Port     int // default 8728
	Username string
	Password string
	Timeout  time.Duration // default 10s
}

// Client is one authenticated RouterOS API session. Not safe for
// concurrent use.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	cfg  Config
}

// Dial connects and logs in (plain login first, legacy MD5 challenge
// fallback for older RouterOS). ctx bounds the whole handshake.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("mikrotik: host is empty")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	timeout := cfg.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)), timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	c := &Client{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn), cfg: cfg}
	if err := c.login(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// login authenticates: plain name/password first (RouterOS 6.43+ and
// 7.x); if the server answers the plain login with a challenge (=ret= on
// the !done sentence), retry with the legacy MD5 challenge response.
func (c *Client) login(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, done, err := c.runCommandRaw(ctx, "/login", map[string]string{
		"name":     c.cfg.Username,
		"password": c.cfg.Password,
	})
	if err != nil {
		return err
	}
	challenge := done["ret"]
	if challenge == "" {
		return nil // plain login accepted
	}
	// Old RouterOS: respond with the MD5 challenge hash.
	hash := md5.Sum([]byte(challenge + "\x00" + c.cfg.Password))
	response := "00" + hex.EncodeToString(hash[:])
	_, _, err = c.runCommandRaw(ctx, "/login", map[string]string{
		"name":     c.cfg.Username,
		"response": response,
	})
	return err
}

// RunCommand sends one command with parameters and returns every !re row
// as attribute maps. A !trap response is returned as an error carrying
// the message.
func (c *Client) RunCommand(ctx context.Context, command string, params map[string]string) ([]map[string]string, error) {
	rows, _, err := c.runCommandRaw(ctx, command, params)
	return rows, err
}

// runCommandRaw is RunCommand plus the !done sentence's attributes (some
// commands — /login's challenge — carry data on the !done sentence that
// rows never see).
func (c *Client) runCommandRaw(ctx context.Context, command string, params map[string]string) (rows []map[string]string, done map[string]string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	words := make([]string, 0, 1+len(params))
	words = append(words, command)
	words = append(words, paramWords(params)...)
	if err := c.writeSentence(words); err != nil {
		return nil, nil, fmt.Errorf("mikrotik: send %s: %w", command, err)
	}

	for {
		sentence, err := c.readSentence()
		if err != nil {
			return nil, nil, fmt.Errorf("mikrotik: read %s: %w", command, err)
		}
		if len(sentence) == 0 {
			return nil, nil, fmt.Errorf("mikrotik: empty response sentence")
		}
		switch sentence[0] {
		case "!done":
			return rows, sentenceToMap(sentence[1:]), nil
		case "!trap", "!fatal":
			msg := attribute(sentence, "message")
			return nil, nil, fmt.Errorf("mikrotik: %s rejected: %s", command, msg)
		case "!re":
			rows = append(rows, sentenceToMap(sentence[1:]))
		default:
			return nil, nil, fmt.Errorf("mikrotik: unexpected sentence %q", sentence[0])
		}
	}
}

// DHCPLease is one /ip/dhcp-server/lease row.
type DHCPLease struct {
	ID           string
	Address      netip.Addr
	MAC          net.HardwareAddr
	Hostname     string
	Server       string
	Status       string
	LastSeen     string // RouterOS duration string, kept raw in detail
	ExpiresAfter string // RouterOS duration string, kept raw in detail
}

// DHCPLeases prints the active leases (the "who is on the network right
// now" view). The .proplist limits payload; unknown fields stay empty.
func (c *Client) DHCPLeases(ctx context.Context) ([]DHCPLease, error) {
	rows, err := c.RunCommand(ctx, "/ip/dhcp-server/lease/print", map[string]string{
		"active":    "true",
		".proplist": ".id,address,mac-address,host-name,server,status,last-seen,expires-after",
	})
	if err != nil {
		return nil, err
	}

	leases := make([]DHCPLease, 0, len(rows))
	for _, row := range rows {
		l := DHCPLease{
			ID:           row["id"],
			Hostname:     row["host-name"],
			Server:       row["server"],
			Status:       row["status"],
			LastSeen:     row["last-seen"],
			ExpiresAfter: row["expires-after"],
		}
		if a, err := netip.ParseAddr(row["address"]); err == nil {
			l.Address = a
		} else {
			continue // a lease without a usable address is noise
		}
		if m, err := net.ParseMAC(row["mac-address"]); err == nil {
			l.MAC = m
		} else {
			continue // same for a lease without a usable MAC
		}
		leases = append(leases, l)
	}
	return leases, nil
}

// -- wire protocol -----------------------------------------------------------

// writeSentence writes words followed by the empty terminator.
func (c *Client) writeSentence(words []string) error {
	for _, w := range words {
		if err := writeWord(c.w, w); err != nil {
			return err
		}
	}
	if err := writeWord(c.w, ""); err != nil {
		return err
	}
	return c.w.Flush()
}

// readSentence reads words until the empty terminator.
func (c *Client) readSentence() ([]string, error) {
	var words []string
	for {
		w, err := readWord(c.r)
		if err != nil {
			return nil, err
		}
		if w == "" {
			return words, nil
		}
		words = append(words, w)
	}
}

// writeWord encodes one word with its length prefix.
func writeWord(w io.Writer, word string) error {
	b := []byte(word)
	n := len(b)
	var prefix []byte
	switch {
	case n < 0x80:
		prefix = []byte{byte(n)}
	case n < 0x4000:
		prefix = []byte{0x80 | byte(n>>8), byte(n)}
	case n < 0x200000:
		prefix = []byte{0xC0 | byte(n>>16), byte(n >> 8), byte(n)}
	case n < 0x10000000:
		prefix = []byte{0xE0 | byte(n>>24), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		return fmt.Errorf("mikrotik: word too long (%d bytes)", n)
	}
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// readWord decodes one length-prefixed word.
func readWord(r *bufio.Reader) (string, error) {
	b0, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	var n int
	switch {
	case b0 < 0x80:
		n = int(b0)
	case b0 < 0xC0:
		b1, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		n = int(b0&0x3F)<<8 | int(b1)
	case b0 < 0xE0:
		b1, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b2, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		n = int(b0&0x1F)<<16 | int(b1)<<8 | int(b2)
	case b0 < 0xF0:
		b1, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b2, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b3, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		n = int(b0&0x0F)<<24 | int(b1)<<16 | int(b2)<<8 | int(b3)
	default:
		return "", fmt.Errorf("mikrotik: invalid length byte 0x%02x", b0)
	}
	if n < 0 || n > 1<<20 {
		return "", fmt.Errorf("mikrotik: implausible word length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func paramWords(params map[string]string) []string {
	out := make([]string, 0, len(params))
	for k, v := range params {
		out = append(out, "="+k+"="+v)
	}
	return out
}

// attribute finds "=key=value" in a sentence.
func attribute(sentence []string, key string) string {
	prefix := "=" + key + "="
	for _, w := range sentence {
		if strings.HasPrefix(w, prefix) {
			return strings.TrimPrefix(w, prefix)
		}
	}
	return ""
}

func sentenceToMap(words []string) map[string]string {
	out := make(map[string]string, len(words))
	for _, w := range words {
		if len(w) >= 3 && w[0] == '=' {
			// Split "=key=value" on the SECOND '='.
			if i := strings.IndexByte(w[1:], '='); i >= 0 {
				out[w[1:1+i]] = w[1+i+1:]
			}
		}
	}
	return out
}
