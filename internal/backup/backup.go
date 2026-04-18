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
	if err := atomicWrite(filePath, []byte(output)); err != nil {
		return "", fmt.Errorf("write config file: %w", err)
	}

	if archivePath != "" {
		if err := moveFile(filePath, archivePath); err != nil {
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

func moveFile(src, destDir string) error {
	info, err := os.Stat(destDir)
	if err != nil {
		return fmt.Errorf("stat destination dir: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s is not directory", destDir)
	}

	now := time.Now().Format("2006-01-02_15-04")
	dirName := filepath.Join(destDir, now)

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
