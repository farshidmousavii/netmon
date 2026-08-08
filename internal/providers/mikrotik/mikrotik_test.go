package mikrotik

// Tests against a fake RouterOS API server speaking the real wire framing
// (same word encoding the client uses), so the protocol handling —
// including the legacy MD5 challenge login — is exercised for real, with
// no live RouterOS device.

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeRouterOS is a minimal RouterOS API server for tests.
type fakeRouterOS struct {
	t        *testing.T
	ln       net.Listener
	password string
	mode     string // "plain" | "challenge"
	leases   []map[string]string
}

func newFakeRouterOS(t *testing.T, mode, password string, leases []map[string]string) *fakeRouterOS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeRouterOS{t: t, ln: ln, mode: mode, password: password, leases: leases}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeRouterOS) addr() string { return f.ln.Addr().String() }

func (f *fakeRouterOS) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeRouterOS) handle(conn net.Conn) {
	defer conn.Close()
	r := newWordReader(conn)
	w := newWordWriter(conn)

	for {
		sentence, err := r.read()
		if err != nil {
			return
		}
		if len(sentence) == 0 {
			continue
		}
		switch sentence[0] {
		case "/login":
			if attr(sentence, "password") != "" {
				if f.mode == "challenge" {
					w.write([]string{"!done", "=ret=0123456789abcdef"})
				} else if attr(sentence, "password") == f.password {
					w.write([]string{"!done"})
				} else {
					w.write([]string{"!trap", "=message=invalid credentials"})
				}
				continue
			}
			// Challenge response path.
			expected := "00" + md5hex("0123456789abcdef\x00"+f.password)
			if attr(sentence, "response") == expected {
				w.write([]string{"!done"})
			} else {
				w.write([]string{"!trap", "=message=invalid credentials"})
			}
		case "/ip/dhcp-server/lease/print":
			for _, row := range f.leases {
				words := []string{"!re"}
				for k, v := range row {
					words = append(words, "="+k+"="+v)
				}
				w.write(words)
			}
			w.write([]string{"!done"})
		default:
			w.write([]string{"!trap", "=message=unknown command"})
		}
	}
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func attr(sentence []string, key string) string {
	prefix := "=" + key + "="
	for _, w := range sentence {
		if strings.HasPrefix(w, prefix) {
			return strings.TrimPrefix(w, prefix)
		}
	}
	return ""
}

// wordReader/wordWriter wrap the production framing for the server side.
type wordReader struct{ r *bufio.Reader }
type wordWriter struct{ w *bufio.Writer }

func newWordReader(r io.Reader) *wordReader { return &wordReader{r: bufio.NewReader(r)} }
func newWordWriter(w io.Writer) *wordWriter { return &wordWriter{w: bufio.NewWriter(w)} }

func (r *wordReader) read() ([]string, error) {
	var out []string
	for {
		w, err := readWord(r.r)
		if err != nil {
			return nil, err
		}
		if w == "" {
			return out, nil
		}
		out = append(out, w)
	}
}

func (w *wordWriter) write(words []string) error {
	for _, word := range words {
		if err := writeWord(w.w, word); err != nil {
			return err
		}
	}
	if err := writeWord(w.w, ""); err != nil {
		return err
	}
	return w.w.Flush()
}

func dialFake(ctx context.Context, f *fakeRouterOS, username, password string) (*Client, error) {
	host, port, _ := net.SplitHostPort(f.addr())
	var portNum int
	fmt.Sscanf(port, "%d", &portNum)
	return Dial(ctx, Config{Host: host, Port: portNum, Username: username, Password: password, Timeout: 5 * time.Second})
}

func TestPlainLoginAndLeasePrint(t *testing.T) {
	leases := []map[string]string{
		{"id": "*1", "address": "192.0.2.50", "mac-address": "02:11:22:33:44:55", "host-name": "pc-one", "server": "dhcp1", "status": "bound"},
		{"id": "*2", "address": "192.0.2.51", "mac-address": "02:11:22:33:44:66", "host-name": "", "server": "dhcp1", "status": "bound"},
	}
	f := newFakeRouterOS(t, "plain", "s3cret", leases)

	c, err := dialFake(context.Background(), f, "admin", "s3cret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	got, err := c.DHCPLeases(context.Background())
	if err != nil {
		t.Fatalf("DHCPLeases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d leases, want 2", len(got))
	}
	if got[0].Address.String() != "192.0.2.50" || got[0].MAC.String() != "02:11:22:33:44:55" || got[0].Hostname != "pc-one" {
		t.Errorf("lease 0 = %+v", got[0])
	}
	if got[1].Hostname != "" {
		t.Errorf("lease 1 hostname = %q, want empty", got[1].Hostname)
	}
}

func TestChallengeLogin(t *testing.T) {
	f := newFakeRouterOS(t, "challenge", "s3cret", nil)

	c, err := dialFake(context.Background(), f, "admin", "s3cret")
	if err != nil {
		t.Fatalf("Dial with correct password: %v", err)
	}
	c.Close()

	if _, err := dialFake(context.Background(), f, "admin", "wrong"); err == nil {
		t.Error("expected login failure with wrong password")
	}
}

func TestTrapError(t *testing.T) {
	f := newFakeRouterOS(t, "plain", "s3cret", nil)

	c, err := dialFake(context.Background(), f, "admin", "s3cret")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	_, err = c.RunCommand(context.Background(), "/no/such/command", nil)
	if err == nil {
		t.Fatal("expected trap error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error should carry the trap message, got: %v", err)
	}
}
