package backup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/farshidmousavii/netmon/internal/logger"
)


func WriteToFile(hostname, deviceType, output, backupDirectory, archivePath string) (string , error) {
	now := time.Now().Format("2006-01-02_15-04-05")

	dirPath := filepath.Join(backupDirectory, deviceType, now)

	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "",fmt.Errorf("create backup directory: %w", err)
	}

	ext, err := deviceExtension(deviceType)
	if err != nil {
		return "",err
	}

	hostname = filepath.Base(hostname)
	fileName := fmt.Sprintf("%s.%s", hostname, ext)
	filePath := filepath.Join(dirPath, fileName)

	logger.Info("Start backup from %s" , hostname)
	// atomic write
	if err := atomicWrite(filePath, []byte(output)); err != nil {
		return "",fmt.Errorf("write config file: %w", err)
	}

	if archivePath != "" {
		if err := moveFile(filePath, archivePath); err != nil {
			return "",fmt.Errorf("move to archive: %w", err)
		}
	}

	return filePath,nil
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

func moveFile(src, destDir string) error {
	info, err := os.Stat(destDir)
	if err != nil {
		return fmt.Errorf("stat destination dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not directory", destDir)
	}

	dst := filepath.Join(destDir, filepath.Base(src))

	// try rename first (fast path)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// fallback to copy + remove
	return copyAndRemove(src, dst)
}

func copyAndRemove(src, dst string) error {
	// prevent overwrite
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination file already exists: %s", dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	tmpFile, err := os.CreateTemp(filepath.Dir(dst), "tmp-*")
	if err != nil {
		return fmt.Errorf("create temp destination: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		tmpFile.Close()
		return fmt.Errorf("copy data: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("sync destination: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), dst); err != nil {
		return fmt.Errorf("final rename: %w", err)
	}

	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source: %w", err)
	}

	return nil
}
