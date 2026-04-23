package device

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/farshidmousavii/netmon/internal/retry"
	"golang.org/x/crypto/ssh"
)

func sshToDeviceWithRetry(ctx context.Context, ip, port, username, password string, config SSHConfig) (*ssh.Client, error) {
	var client *ssh.Client

	err := retry.Do(ctx, config.RetryConfig, fmt.Sprintf("SSH to %s", ip), func() error {
		var connectErr error
		client, connectErr = sshToDeviceWithContext(ctx, ip, port, username, password, config.Timeout)
		return connectErr
	})

	return client, err
}

// sshToDeviceWithTimeout - SSH connection with timeout
func sshToDeviceWithTimeout(ip, port, username, password string, timeout time.Duration) (*ssh.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
		Config: ssh.Config{
			KeyExchanges: []string{
				"diffie-hellman-group1-sha1",
				"diffie-hellman-group14-sha1",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
			},
			Ciphers: []string{
				"aes128-cbc",
				"aes192-cbc",
				"aes256-cbc",
				"3des-cbc",
				"aes128-ctr",
				"aes192-ctr",
				"aes256-ctr",
			},
		},
	}

	address := net.JoinHostPort(ip, port)

	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH dial failed: %w", err)
	}

	return client, nil
}

func sshToDeviceWithContext(ctx context.Context, ip, port, username, password string, timeout time.Duration) (*ssh.Client, error) {
	type result struct {
		client *ssh.Client
		err    error
	}

	resultChan := make(chan result, 1)

	// SSH connection in a separate goroutine
	go func() {
		client, err := sshToDeviceWithTimeout(ip, port, username, password, timeout)
		resultChan <- result{client: client, err: err}
	}()

	// Wait with context
	select {
	case res := <-resultChan:
		return res.client, res.err
	case <-ctx.Done():
		// Context cancel شد
		return nil, fmt.Errorf("SSH connection cancelled: %w", ctx.Err())
	}
}

// Old use for backward compatibility
func sshToDevice(ip, port, username, password string) (*ssh.Client, error) {
	return sshToDeviceWithRetry(context.Background(), ip, port, username, password, DefaultSSHConfig())
}
