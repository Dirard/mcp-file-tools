package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

type fileTextInfo struct {
	resolvedPath string
	displayPath  string
	stat         os.FileInfo
	encoding     encodingResult
	fingerprint  FileFingerprint
}

func (h *Handler) inspectTextFileForRefactor(ctx context.Context, requestedPath string) (fileTextInfo, error) {
	if err := contextError(ctx); err != nil {
		return fileTextInfo{}, err
	}
	v := h.ResolvePath(requestedPath)
	if !v.Ok() {
		return fileTextInfo{}, v.Err
	}
	stat, err := os.Stat(v.Path)
	if err != nil {
		return fileTextInfo{}, fmt.Errorf("cannot access file: %w", err)
	}
	if stat.IsDir() {
		return fileTextInfo{}, fmt.Errorf("%q is a directory, not a file", requestedPath)
	}
	if err := rejectBinaryFileSample(v.Path); err != nil {
		return fileTextInfo{}, err
	}
	encResult, err := h.resolveEncodingSample("", v.Path)
	if err != nil {
		return fileTextInfo{}, fmt.Errorf("cannot detect encoding: %w", err)
	}
	fingerprint, err := computeFileFingerprint(ctx, v.Path, stat, encResult)
	if err != nil {
		return fileTextInfo{}, err
	}
	return fileTextInfo{
		resolvedPath: v.Path,
		displayPath:  h.displayResolvedPath(requestedPath, v.Path),
		stat:         stat,
		encoding:     encResult,
		fingerprint:  fingerprint,
	}, nil
}

func (h *Handler) inspectTextFileForRefactorWriteEligible(ctx context.Context, requestedPath string) (fileTextInfo, error) {
	if err := contextError(ctx); err != nil {
		return fileTextInfo{}, err
	}
	v := h.ResolvePath(requestedPath)
	if !v.Ok() {
		return fileTextInfo{}, v.Err
	}
	if err := ensureWriteEligibleTextFile(ctx, v.Path, h.config.WriteThreshold); err != nil {
		return fileTextInfo{}, err
	}
	return h.inspectTextFileForRefactor(ctx, requestedPath)
}

func rejectBinaryFileSample(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open file: %w", err)
	}
	defer file.Close()
	sample := make([]byte, binaryCheckSize)
	n, err := file.Read(sample)
	if err != nil && err != io.EOF {
		return fmt.Errorf("cannot inspect file sample: %w", err)
	}
	if isBinaryFile(sample[:n]) {
		return fmt.Errorf("binary files are not supported")
	}
	return nil
}

func computeFileFingerprint(ctx context.Context, path string, stat os.FileInfo, encResult encodingResult) (FileFingerprint, error) {
	file, err := os.Open(path)
	if err != nil {
		return FileFingerprint{}, fmt.Errorf("cannot open file for fingerprint: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		if err := contextError(ctx); err != nil {
			return FileFingerprint{}, err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			if _, err := hash.Write(buf[:n]); err != nil {
				return FileFingerprint{}, err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return FileFingerprint{}, fmt.Errorf("cannot read file for fingerprint: %w", readErr)
		}
	}

	lineCount := 0
	if stat.Size() > 0 {
		count, err := countDecodedLines(ctx, path, encResult)
		if err != nil {
			return FileFingerprint{}, fmt.Errorf("cannot count lines for fingerprint: %w", err)
		}
		lineCount = count
	}
	return FileFingerprint{
		SHA256:           hex.EncodeToString(hash.Sum(nil)),
		SizeBytes:        stat.Size(),
		LineCount:        lineCount,
		ModifiedUnixNano: stat.ModTime().UnixNano(),
	}, nil
}
