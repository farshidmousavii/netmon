package device

import (
	"io"
	"time"

	"golang.org/x/crypto/ssh"
)

func sshToDevice(ip, port, username, password string) (*ssh.Client, error) {
	serverAddress := ip + ":" + port
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second * 5,
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

	conn, err := ssh.Dial("tcp", serverAddress, config)

	return conn, err

}

func runCisco(session *ssh.Session, enableSecret string) (string, error) {
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
		return "", err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := session.Shell(); err != nil {
		return "", err
	}
	commands := []string{
		"enable",
		enableSecret,
		"terminal length 0",
		"show running-config",
		"exit",
	}
	for _, cmd := range commands {
		stdin.Write([]byte(cmd + "\n"))
		time.Sleep(1 * time.Second)
	}
	output, _ := io.ReadAll(stdout)
	return string(output), nil
}
