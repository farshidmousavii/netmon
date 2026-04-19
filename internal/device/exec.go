package device

import (
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// RunCommand - one command
func (d Device) RunCommand(command string) (string, error) {
	return d.RunCommands([]string{command})
}

// RunCommands - multiple commands
func (d Device) RunCommands(commands []string) (string, error) {
	sshClient, err := sshToDevice(d.IP, d.Port, d.Username, d.Password)
	if err != nil {
		return "", fmt.Errorf("SSH connection failed: %w", err)
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	switch d.Type() {
	case "cisco":
		return d.runCiscoCommands(session, commands)
	case "mikrotik":
		return d.runMikrotikCommands(session, commands)
	default:
		return "", fmt.Errorf("unsupported device type: %s", d.Type())
	}
}

func executeCiscoShell(session *ssh.Session, commands []string) (string, error) {
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty("xterm", 80, 40, modes); err != nil {
		return "", fmt.Errorf("request PTY: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("start shell: %w", err)
	}

	//execute commands
	for _, cmd := range commands {
		stdin.Write([]byte(cmd + "\n"))
		time.Sleep(1 * time.Second)
	}

	output, _ := io.ReadAll(stdout)
	return string(output), nil
}

func (d Device) runCiscoCommands(session *ssh.Session, userCommands []string) (string, error) {
	// need config mode or no
	needsConfigMode := false
	for _, cmd := range userCommands {
		if !isShowCommand(cmd) {
			needsConfigMode = true
			break
		}
	}

	commands := []string{
		"enable",
		d.Password,
		"terminal length 0",
	}

	if needsConfigMode {
		commands = append(commands, "conf t")
	}

	commands = append(commands, userCommands...)

	if needsConfigMode {
		commands = append(commands, "end")
	}

	commands = append(commands, "exit")

	return executeCiscoShell(session, commands)
}

func (d Device) runMikrotikCommands(session *ssh.Session, commands []string) (string, error) {
	fullCommand := strings.Join(commands, "\n")
	output, err := session.CombinedOutput(fullCommand)
	if err != nil {
		return "", fmt.Errorf("run commands: %w", err)
	}
	return string(output), nil
}

// recognize show command
func isShowCommand(cmd string) bool {
	cmd = strings.TrimSpace(strings.ToLower(cmd))

	showPrefixes := []string{
		"show",
		"dir",
		"more",
		"ping",
		"traceroute",
	}

	for _, prefix := range showPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return true
		}
	}

	return false
}
