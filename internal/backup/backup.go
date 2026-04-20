package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/farshidmousavii/netmon/internal/config"
	"github.com/farshidmousavii/netmon/internal/device"
	"github.com/farshidmousavii/netmon/internal/logger"
	"github.com/farshidmousavii/netmon/internal/report"
)

func BackupDevice(deviceCfg config.DeviceConfig, cfg *config.Config, wg *sync.WaitGroup, reports chan<- report.DeviceReport) {
	defer wg.Done()
	report := report.DeviceReport{
		Name: deviceCfg.Name,
		IP:   deviceCfg.IP,
		Type: deviceCfg.Vendor,
	}

	cred, err := cfg.GetCredential(deviceCfg.Credential)
	if err != nil {
		logger.Error("device %s: failed to get credential: %v", deviceCfg.Name, err)
		report.Error = err
		reports <- report
		return
	}

	// new device
	device, err := device.NewDevice(deviceCfg, cred)
	if err != nil {
		logger.Error("device %s: failed to create: %v", deviceCfg.Name, err)
		report.Error = err
		reports <- report
		return
	}

	output, err := device.ShowCommand()
	if err != nil {
		logger.Error("device %s: failed to get config: %v", device.IP, err)
		report.Error = fmt.Errorf("device %s: failed to get config: %w", device.IP, err)
		reports <- report
		return
	}
	// Extract hostname from config
	hostname := extractHostname(device.Type(), output)
	if hostname == "" {
		hostname = device.IP
	}

	filePathAddress, err := WriteToFile(hostname, device.Type(), output, cfg.Backup.Directory, cfg.Backup.ArchivePath)
	if err != nil {
		logger.Error("device %s: failed to write backup: %v", device.IP, err)
		report.Error = fmt.Errorf("device %s: failed to write backup: %w", device.IP, err)
		reports <- report
		return
	}

	if cfg.Backup.ArchivePath != "" {
		report.BackupPath = filepath.Join(cfg.Backup.ArchivePath, device.Type(), filepath.Base(filePathAddress))
	} else {

		report.BackupPath = filePathAddress
	}

	report.Online = true
	reports <- report

}

func WriteToFile(hostname, deviceType, output, backupDirectory, archivePath string) (string, error) {
	now := time.Now().Format("2006-01-02_15-04")

	dirPath := filepath.Join(backupDirectory, deviceType, now)

	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	ext, err := deviceExtension(deviceType)
	if err != nil {
		return "", err
	}

	hostname = filepath.Base(hostname)
	fileName := fmt.Sprintf("%s.%s", hostname, ext)
	filePath := filepath.Join(dirPath, fileName)

	logger.Info("Start backup from %s", hostname)
	// atomic write
	if err := atomicWrite(filePath, []byte(normalizeCiscoOutput(output, deviceType))); err != nil {
		return "", fmt.Errorf("write config file: %w", err)
	}

	if archivePath != "" {
		if err := moveFile(filePath, archivePath, deviceType); err != nil {
			return "", fmt.Errorf("move to archive: %w", err)
		}
	}

	return filePath, nil
}

func deviceExtension(deviceType string) (string, error) {
	switch deviceType {
	case "cisco":
		return "txt", nil
	case "mikrotik":
		return "rsc", nil
	default:
		return "", fmt.Errorf("unsupported device type: %s", deviceType)
	}
}

func atomicWrite(dst string, data []byte) error {
	dir := filepath.Dir(dst)

	tmpFile, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// prevent overwrite
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination file already exists: %s", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), dst); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

func moveFile(src, destDir, deviceType string) error {
	info, err := os.Stat(destDir)
	if err != nil {
		return fmt.Errorf("stat destination dir: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not directory", destDir)
	}

	now := time.Now().Format("2006-01-02_15-04")
	dirName := filepath.Join(destDir, now, deviceType)

	if err = os.MkdirAll(dirName, 0755); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}

	dst := filepath.Join(dirName, filepath.Base(src))

	// Try rename first (fast path)
	if err := os.Rename(src, dst); err == nil {
		// check folder is empty ?
		cleanupEmptyDir(filepath.Dir(src))
		return nil
	}

	// Fallback to copy + remove
	if err := copyFile(src, dst); err != nil {
		return err
	}

	// remove file
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source file: %w", err)
	}

	// check folder is empty ?
	cleanupEmptyDir(filepath.Dir(src))

	return nil
}

func copyFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination file already exists: %s", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		srcFile.Close()
		return fmt.Errorf("create destination: %w", err)
	}

	_, copyErr := io.Copy(dstFile, srcFile)

	srcFile.Close()
	dstFile.Close()

	if copyErr != nil {
		os.Remove(dst)
		return fmt.Errorf("copy data: %w", copyErr)
	}

	return nil
}

func cleanupEmptyDir(dir string) {
	//check is empty ?
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warning("Failed to read directory %s: %v", dir, err)
		return
	}

	// if folder is empty , remove it
	if len(entries) == 0 {
		if err := os.Remove(dir); err != nil {
			logger.Warning("Failed to remove empty directory %s: %v", dir, err)
		} else {
			logger.Info("Removed empty backup directory: %s", dir)
		}
	}
}

func extractHostname(deviceType, backupConfig string) string {

	var match []string

	switch deviceType {
	case "cisco":
		re := regexp.MustCompile(`\bhostname\s+(\S+)`)
		match = re.FindStringSubmatch(backupConfig)
	case "mikrotik":
		re := regexp.MustCompile(`(?m)^set\s+name=([^\s]+)`)
		match = re.FindStringSubmatch(backupConfig)
	}

	if len(match) > 1 {
		return match[1]
	}

	return ""
}

func normalizeCiscoOutput(output, deviceType string) string {
	if deviceType != "cisco" {
		return output
	}
	output = strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(output, "\n")

	var result []string
	start := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		// start of config
		if strings.HasPrefix(trim, "version") ||
			strings.HasPrefix(trim, "!") ||
			strings.HasPrefix(trim, "interface") {
			start = true
		}

		if !start {
			continue
		}

		// remove prompt
		if isCiscoPrompt(trim) {
			continue
		}

		if trim == "" {
			continue
		}

		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

var promptRegex = regexp.MustCompile(`^[a-zA-Z0-9\-_\.]+[>#]`)

func isCiscoPrompt(line string) bool {
	return promptRegex.MatchString(line)
}
