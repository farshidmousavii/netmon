package device

import (
	"time"

	"golang.org/x/crypto/ssh"
)

func sshToDevice(ip, port, username, password string) (*ssh.Client, error) {
	serverAddress := ip + ":" + port
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				//for each question return password
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
			//regular method
			ssh.Password(password),
		},
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
