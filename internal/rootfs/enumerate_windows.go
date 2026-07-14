//go:build windows

package rootfs

import (
	"context"
	"encoding/binary"
	"errors"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/windows"
)

const windowsDirectoryEnumerationBufferSize = 64 * 1024

type windowsFileIDExtdDirectoryInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32
	EaSize          uint32
	ReparsePointTag uint32
	FileID          [16]byte
	FileName        [1]uint16
}

func enumeratePlatformDir(handle platformDir, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	if !handle.valid {
		return ErrClosed
	}
	informationClass := uint32(windows.FileIdExtdDirectoryRestartInfo)
	buffer := make([]byte, windowsDirectoryEnumerationBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := windows.GetFileInformationByHandleEx(handle.handle, informationClass, &buffer[0], uint32(len(buffer)))
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return nil
		}
		if err != nil {
			return classifyWindowsEnumerationError(err)
		}
		if err := consumeWindowsDirectoryBuffer(buffer, parent, rootMount, ctx, charge, consume); err != nil {
			return err
		}
		informationClass = windows.FileIdExtdDirectoryInfo
	}
}

func consumeWindowsDirectoryBuffer(buffer []byte, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	headerLength := int(unsafe.Offsetof(windowsFileIDExtdDirectoryInfo{}.FileName))
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(buffer)-offset < headerLength {
			return ErrIO
		}
		information := (*windowsFileIDExtdDirectoryInfo)(unsafe.Pointer(&buffer[offset]))
		nameLength := int(information.FileNameLength)
		minimumRecordLength := headerLength + nameLength
		recordLength := int(information.NextEntryOffset)
		last := recordLength == 0
		if last {
			recordLength = alignWindowsDirectoryRecord(minimumRecordLength)
		}
		if nameLength < 0 || minimumRecordLength < headerLength || recordLength < minimumRecordLength || recordLength > len(buffer)-offset {
			return ErrIO
		}
		if err := charge(uint64(recordLength)); err != nil {
			return err
		}
		nameBytes := buffer[offset+headerLength : offset+headerLength+nameLength]
		kind := windowsEntryKind(information.FileAttributes, information.ReparsePointTag)
		identity := Identity{Platform: pathspec.Windows, Mount: rootMount, File: information.FileID}
		if nameLength%2 != 0 {
			outcome := EnumerationOutcome{disposition: EnumerationPathEncodingUnsupported, boundaryKind: kind}
			if err := consume(outcome); err != nil {
				return err
			}
		} else {
			rawName := make([]uint16, nameLength/2)
			for index := range rawName {
				rawName[index] = binary.LittleEndian.Uint16(nameBytes[index*2 : index*2+2])
			}
			if decoded, ok := decodeWindowsUTF16(rawName); ok && (decoded == "." || decoded == "..") {
				if last {
					return nil
				}
				offset += recordLength
				continue
			}
			outcome := windowsEnumerationOutcome(parent, rawName, kind, identity, true)
			if err := consume(outcome); err != nil {
				return err
			}
		}
		if last {
			return nil
		}
		offset += recordLength
	}
}

func alignWindowsDirectoryRecord(length int) int {
	return (length + 7) &^ 7
}

func windowsEntryKind(attributes, reparseTag uint32) EntryKind {
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		if reparseTag == windows.IO_REPARSE_TAG_MOUNT_POINT {
			return EntryBoundary
		}
		return EntrySymlink
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return EntryDir
	}
	if attributes&windows.FILE_ATTRIBUTE_DEVICE != 0 {
		return EntrySpecial
	}
	return EntryFile
}

func classifyWindowsEnumerationError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_INVALID_HANDLE):
		return ErrClosed
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return ErrPermissionDenied
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND),
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND),
		errors.Is(err, windows.ERROR_DELETE_PENDING):
		return ErrSourceChanged
	default:
		return ErrIO
	}
}
