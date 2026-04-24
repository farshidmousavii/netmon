package device

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/retry"
	"golang.org/x/crypto/ssh"
)

func SSHConfigFromSettings(settings *config.SSHSettings) config.SSHSettings {
	return config.SSHSettings{
		Timeout: settings.Timeout,
		Retry: config.RetrySettings{
			MaxAttempts:  settings.Retry.MaxAttempts,
			InitialDelay: settings.Retry.InitialDelay,
			MaxDelay:     settings.Retry.MaxDelay,
			Multiplier:   settings.Retry.Multiplier,
		},
	}
}

func sshToDeviceWithRetry(ctx context.Context, ip, port, username, password string, config *config.SSHSettings) (*ssh.Client, error) {
	var client *ssh.Client

	err := retry.Do(ctx, config, fmt.Sprintf("SSH to %s", ip), func() error {
		var connectErr error
		client, connectErr = sshToDeviceWithContext(ctx, ip, port, username, password, time.Duration(config.Timeout)*time.Second)
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
		// if Context canselled
		return nil, fmt.Errorf("SSH connection cancelled: %w", ctx.Err())
	}
}

// Old use for backward compatibility
func sshToDevice(ip, port, username, password string, cfg *config.Config) (*ssh.Client, error) {
	sshSettings := cfg.GetSSHSettings()
	sshConfig := SSHConfigFromSettings(sshSettings)
	return sshToDeviceWithRetry(context.Background(), ip, port, username, password, &sshConfig)
}
