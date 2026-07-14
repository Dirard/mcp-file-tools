//go:build windows

package rootfs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/windows"
)

const windowsDirectoryEnumerationBufferSize = 64 * 1024

type windowsFileIDBothDirectoryInfo struct {
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
	ShortNameLength uint32
	ShortName       [12]uint16
	FileID          uint64
	FileName        [1]uint16
}

type windowsFileFullDirectoryInfo struct {
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
	FileName        [1]uint16
}

func enumeratePlatformDir(handle platformDir, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	if !handle.valid {
		return ErrClosed
	}
	informationClass, continuationClass, recordsHaveFileID := windowsDirectoryInformationClasses(handle.handle)
	firstRead := true
	buffer := make([]byte, windowsDirectoryEnumerationBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := windows.GetFileInformationByHandleEx(handle.handle, informationClass, &buffer[0], uint32(len(buffer)))
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) ||
			(firstRead && errors.Is(err, windows.ERROR_FILE_NOT_FOUND)) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: GetFileInformationByHandleEx class %d: %v", classifyWindowsEnumerationError(err), informationClass, err)
		}
		if err := consumeWindowsDirectoryBuffer(handle.handle, buffer, recordsHaveFileID, parent, rootMount, ctx, charge, consume); err != nil {
			return err
		}
		informationClass = continuationClass
		firstRead = false
	}
}

func windowsDirectoryInformationClasses(handle windows.Handle) (uint32, uint32, bool) {
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, nil, 0); err == nil &&
		flags&windows.FILE_SUPPORTS_OBJECT_IDS != 0 && flags&windows.FILE_SUPPORTS_OPEN_BY_FILE_ID != 0 {
		return windows.FileIdBothDirectoryRestartInfo, windows.FileIdBothDirectoryInfo, true
	}
	return windows.FileFullDirectoryRestartInfo, windows.FileFullDirectoryInfo, false
}

func consumeWindowsDirectoryBuffer(directory windows.Handle, buffer []byte, recordsHaveFileID bool, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	headerLength := int(unsafe.Offsetof(windowsFileFullDirectoryInfo{}.FileName))
	if recordsHaveFileID {
		headerLength = int(unsafe.Offsetof(windowsFileIDBothDirectoryInfo{}.FileName))
	}
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(buffer)-offset < headerLength {
			return fmt.Errorf("%w: directory buffer truncated at offset %d", ErrIO, offset)
		}
		var nextEntryOffset, fileAttributes, fileNameLength uint32
		identity := Identity{Platform: pathspec.Windows, Mount: rootMount}
		identityKnown := false
		if recordsHaveFileID {
			information := (*windowsFileIDBothDirectoryInfo)(unsafe.Pointer(&buffer[offset]))
			nextEntryOffset = information.NextEntryOffset
			fileAttributes = information.FileAttributes
			fileNameLength = information.FileNameLength
			binary.LittleEndian.PutUint64(identity.File[:8], information.FileID)
			identityKnown = true
		} else {
			information := (*windowsFileFullDirectoryInfo)(unsafe.Pointer(&buffer[offset]))
			nextEntryOffset = information.NextEntryOffset
			fileAttributes = information.FileAttributes
			fileNameLength = information.FileNameLength
		}
		nameLength := int(fileNameLength)
		minimumRecordLength := headerLength + nameLength
		recordLength := int(nextEntryOffset)
		last := recordLength == 0
		if last {
			recordLength = alignWindowsDirectoryRecord(minimumRecordLength)
		}
		if nameLength < 0 || minimumRecordLength < headerLength || recordLength < minimumRecordLength || recordLength > len(buffer)-offset {
			return fmt.Errorf("%w: invalid directory record offset=%d next=%d name_bytes=%d buffer_bytes=%d", ErrIO, offset, nextEntryOffset, nameLength, len(buffer))
		}
		if err := charge(uint64(recordLength)); err != nil {
			return err
		}
		nameBytes := buffer[offset+headerLength : offset+headerLength+nameLength]
		kind := windowsEntryKind(fileAttributes, 0)
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
			decoded, decodedOK := decodeWindowsUTF16(rawName)
			if decodedOK && (decoded == "." || decoded == "..") {
				if last {
					return nil
				}
				offset += recordLength
				continue
			}
			outcome := windowsEnumerationOutcome(parent, rawName, kind, identity, identityKnown)
			if decodedOK && (!identityKnown || fileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0) {
				evidenceKind, evidenceIdentity, evidenceErr := windowsEntryEvidence(directory, decoded, rootMount)
				if evidenceErr != nil {
					if errors.Is(evidenceErr, ErrClosed) {
						return ErrClosed
					}
					disposition := EnumerationUnreadable
					if errors.Is(evidenceErr, ErrSourceChanged) || errors.Is(evidenceErr, ErrNotFound) {
						disposition = EnumerationSourceChanged
					}
					outcome = EnumerationOutcome{disposition: disposition, boundaryKind: kind}
				} else {
					outcome = windowsEnumerationOutcome(parent, rawName, evidenceKind, evidenceIdentity, true)
				}
			}
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

func windowsEntryEvidence(directory windows.Handle, name string, rootMount [16]byte) (EntryKind, Identity, error) {
	handle, err := openWindowsSearchRelative(directory, name, windowsMetadataAccess)
	if err != nil {
		return 0, Identity{}, err
	}
	defer windows.CloseHandle(handle)
	attributes, reparseTag, err := windowsAttributeTag(handle)
	if err != nil {
		return 0, Identity{}, classifyWindowsEnumerationError(err)
	}
	identity, err := windowsIdentity(handle)
	if err != nil {
		return 0, Identity{}, classifyWindowsEnumerationError(err)
	}
	if identity.Mount != rootMount {
		return EntryBoundary, identity, nil
	}
	return windowsEntryKind(attributes, reparseTag), identity, nil
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
