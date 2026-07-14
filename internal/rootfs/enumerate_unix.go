//go:build darwin

package rootfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

const darwinDirectoryBufferSize = 32 * 1024

func enumeratePlatformDir(handle platformDir, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	if !handle.valid {
		return ErrClosed
	}
	nameOffset := int(unsafe.Offsetof(unix.Dirent{}.Name))
	buffer := make([]byte, darwinDirectoryBufferSize)
	var base uintptr
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := unix.Getdirentries(handle.fd, buffer, &base)
		if err != nil {
			return classifyDarwinEnumerationError(err)
		}
		if read == 0 {
			return nil
		}
		for offset := 0; offset < read; {
			if err := ctx.Err(); err != nil {
				return err
			}
			if read-offset < nameOffset {
				return ErrIO
			}
			entry := (*unix.Dirent)(unsafe.Pointer(&buffer[offset]))
			recordLength := int(entry.Reclen)
			nameLength := int(entry.Namlen)
			if recordLength < nameOffset || recordLength > read-offset || nameLength < 1 || nameLength > recordLength-nameOffset {
				return ErrIO
			}
			if err := charge(uint64(recordLength)); err != nil {
				return err
			}
			name := buffer[offset+nameOffset : offset+nameOffset+nameLength]
			offset += recordLength
			if bytes.Equal(name, []byte(".")) || bytes.Equal(name, []byte("..")) {
				continue
			}
			kindHint := entryKindFromDarwinDirentType(entry.Type)
			outcome, err := posixEnumerationOutcomeWithEvidence(parent, name, kindHint, func(component string) (EntryKind, Identity, bool, error) {
				return darwinEntryEvidence(handle.fd, component, rootMount)
			})
			if err != nil {
				return err
			}
			if err := consume(outcome); err != nil {
				return err
			}
		}
	}
}

func entryKindFromDarwinDirentType(kind uint8) EntryKind {
	switch kind {
	case unix.DT_REG:
		return EntryFile
	case unix.DT_DIR:
		return EntryDir
	case unix.DT_LNK:
		return EntrySymlink
	default:
		return EntrySpecial
	}
}

func darwinEntryEvidence(directoryFD int, name string, rootMount [16]byte) (EntryKind, Identity, bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, Identity{}, false, classifyDarwinEnumerationError(err)
	}
	identity := Identity{Platform: pathspec.POSIX}
	binary.LittleEndian.PutUint64(identity.File[0:8], stat.Ino)
	binary.LittleEndian.PutUint32(identity.File[8:12], stat.Gen)
	rootDevice := binary.LittleEndian.Uint32(rootMount[8:12])
	if uint32(stat.Dev) != rootDevice {
		binary.LittleEndian.PutUint32(identity.Mount[8:12], uint32(stat.Dev))
		return EntryBoundary, identity, false, nil
	}
	identity.Mount = rootMount
	return entryKindFromDarwinMode(uint32(stat.Mode)), identity, true, nil
}

func entryKindFromDarwinMode(mode uint32) EntryKind {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return EntryFile
	case unix.S_IFDIR:
		return EntryDir
	case unix.S_IFLNK:
		return EntrySymlink
	default:
		return EntrySpecial
	}
}

func classifyDarwinEnumerationError(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ESTALE):
		return ErrSourceChanged
	case errors.Is(err, unix.EBADF):
		return ErrClosed
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return ErrPermissionDenied
	default:
		return ErrIO
	}
}
