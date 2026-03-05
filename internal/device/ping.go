package device

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
)

func pingDevice(ip string) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("ping", "-n", "1", "-w", "1000", ip)
	case "linux":
		cmd = exec.Command("ping", "-c", "1", "-W", "1", ip)
	default:
		return "", fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ping to %s failed: %w", ip, err)
	}

	re := regexp.MustCompile(`time[=<]?\s*(\d+)ms`)
	match := re.FindStringSubmatch(string(output))
	if len(match) < 2 {
		return "", fmt.Errorf("could not extract ping time")
	}
	return match[1] + "ms", nil
}
