package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

func (h *Handler) resolveRefactorPath(pathCtx PathContext, inputPath, fieldName string) (string, string, error) {
	return h.resolveToolPath(pathCtx, inputPath, fieldName)
}

func ensureWriteEligibleTextFile(ctx context.Context, path string, writeThreshold int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a writable text file")
	}
	if writeThreshold > 0 && info.Size() > writeThreshold {
		return fmt.Errorf("file exceeds MCP_WRITE_THRESHOLD")
	}
	if err := rejectBinaryFileSample(path); err != nil {
		return err
	}
	if err := validateUTF8File(ctx, path); err != nil {
		return err
	}
	return nil
}

func validateUTF8File(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		if err := contextError(ctx); err != nil {
			return err
		}
		r, size, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if r == utf8.RuneError && size == 1 {
			return fmt.Errorf("unsupported encoding: only UTF-8/ASCII text writes are supported")
		}
	}
}

func rejectSymlinkPath(path string, checkFinal bool) error {
	cleaned := filepath.Clean(path)
	if checkFinal {
		info, err := os.Lstat(cleaned)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink paths are not supported for refactor writes: %s", path)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	dir := filepath.Dir(cleaned)
	volume := filepath.VolumeName(dir)
	for {
		if dir == "." || dir == string(filepath.Separator) || dir == volume+string(filepath.Separator) || dir == volume {
			return nil
		}
		info, err := os.Lstat(dir)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink parent directories are not supported for refactor writes: %s", dir)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func sameFileOrPath(source, target string) (bool, error) {
	sourceInfo, sourceErr := os.Stat(source)
	targetInfo, targetErr := os.Stat(target)
	if sourceErr == nil && targetErr == nil {
		return os.SameFile(sourceInfo, targetInfo), nil
	}
	if sourceErr != nil && !os.IsNotExist(sourceErr) {
		return false, sourceErr
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return false, targetErr
	}
	sourceClean := filepath.Clean(source)
	targetClean := filepath.Clean(target)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(sourceClean, targetClean), nil
	}
	return sourceClean == targetClean, nil
}

func fingerprintMatches(actual FileFingerprint, expected FileFingerprint) bool {
	return expected.SHA256 != "" && strings.EqualFold(actual.SHA256, expected.SHA256)
}
