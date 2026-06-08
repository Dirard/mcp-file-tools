package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func writeFileReplace(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	var existingMode os.FileMode
	existingModeSet := false
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		existingMode = info.Mode().Perm()
		existingModeSet = true
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := write(temp); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if existingModeSet {
		if err := temp.Chmod(existingMode); err != nil {
			return err
		}
	}
	if err := temp.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	syncParentDir(dir)
	return nil
}

func writeFileCreateNew(path string, write func(io.Writer) error) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := write(file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	syncParentDir(filepath.Dir(path))
	return nil
}

func createSidecarBackup(path string, role string) (BackupResult, error) {
	result := BackupResult{
		File:      path,
		Role:      role,
		Requested: true,
	}
	if err := rejectSymlinkPath(path, true); err != nil {
		return backupCreationError(result, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return backupCreationError(result, err)
	}
	if info.IsDir() {
		err := fmt.Errorf("cannot backup directory")
		return backupCreationError(result, err)
	}
	source, err := os.Open(path)
	if err != nil {
		return backupCreationError(result, err)
	}
	defer source.Close()

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	hash := shortBackupHash(path, info)
	for attempt := 0; attempt < 10; attempt++ {
		backupPath := filepath.Join(dir, fmt.Sprintf(".%s.%s.%s.%02d.bak", base, time.Now().UTC().Format("20060102T150405.000000000Z"), hash, attempt))
		target, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			result.BackupPath = backupPath
			return backupCreationError(result, err)
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			_ = target.Close()
			result.BackupPath = backupPath
			return backupCreationError(result, err)
		}
		_, copyErr := io.Copy(target, source)
		syncErr := target.Sync()
		closeErr := target.Close()
		if copyErr != nil {
			_ = os.Remove(backupPath)
			result.BackupPath = backupPath
			return backupCreationError(result, copyErr)
		}
		if syncErr != nil {
			_ = os.Remove(backupPath)
			result.BackupPath = backupPath
			return backupCreationError(result, syncErr)
		}
		if closeErr != nil {
			_ = os.Remove(backupPath)
			result.BackupPath = backupPath
			return backupCreationError(result, closeErr)
		}
		syncParentDir(dir)
		result.Created = true
		result.BackupPath = backupPath
		return result, nil
	}
	err = fmt.Errorf("could not create unique backup path after retries")
	return backupCreationError(result, err)
}

func backupCreationError(result BackupResult, err error) (BackupResult, error) {
	result.ErrorCode = "backup_creation_failed"
	result.Error = err.Error()
	return result, fmt.Errorf("backup_creation_failed: %w", err)
}

func shortBackupHash(path string, info os.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:])[:12]
}

func syncParentDir(dir string) {
	parent, err := os.Open(dir)
	if err != nil {
		return
	}
	defer parent.Close()
	_ = parent.Sync()
}
