//go:build linux

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

const linuxDirectoryBufferSize = 32 * 1024

func enumeratePlatformDir(handle platformDir, parent pathspec.Relative, rootMount [16]byte, ctx context.Context, charge func(uint64) error, consume func(EnumerationOutcome) error) error {
	if !handle.valid {
		return ErrClosed
	}
	nameOffset := int(unsafe.Offsetof(unix.Dirent{}.Name))
	buffer := make([]byte, linuxDirectoryBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, err := unix.Getdents(handle.fd, buffer)
		if err != nil {
			return classifyLinuxEnumerationError(err)
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
			if recordLength < nameOffset+1 || recordLength > read-offset {
				return ErrIO
			}
			if err := charge(uint64(recordLength)); err != nil {
				return err
			}
			name := buffer[offset+nameOffset : offset+recordLength]
			terminator := bytes.IndexByte(name, 0)
			if terminator < 0 {
				return ErrIO
			}
			name = name[:terminator]
			offset += recordLength
			if bytes.Equal(name, []byte(".")) || bytes.Equal(name, []byte("..")) {
				continue
			}
			kindHint := entryKindFromLinuxDirentType(entry.Type)
			outcome, err := posixEnumerationOutcomeWithEvidence(parent, name, kindHint, func(component string) (EntryKind, Identity, bool, error) {
				return linuxEntryEvidence(handle.fd, component, rootMount, kindHint)
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

func entryKindFromLinuxDirentType(kind uint8) EntryKind {
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

func linuxEntryEvidence(directoryFD int, name string, rootMount [16]byte, kindHint EntryKind) (EntryKind, Identity, bool, error) {
	identity, mode, err := linuxIdentityAt(directoryFD, name)
	if err != nil {
		return 0, Identity{}, false, classifyLinuxEnumerationError(err)
	}
	if kindHint != EntrySymlink && identity.Mount != rootMount {
		return EntryBoundary, identity, true, nil
	}
	return entryKindFromUnixMode(mode), identity, true, nil
}

func linuxIdentityAt(directoryFD int, name string) (Identity, uint32, error) {
	var statx unix.Statx_t
	wantMask := uint32(unix.STATX_TYPE | unix.STATX_INO | unix.STATX_MNT_ID)
	err := unix.Statx(directoryFD, name, unix.AT_STATX_SYNC_AS_STAT, int(wantMask), &statx)
	if err == nil && statx.Mask&wantMask == wantMask {
		identity := Identity{Platform: pathspec.POSIX}
		binary.LittleEndian.PutUint64(identity.Mount[0:8], statx.Mnt_id)
		binary.LittleEndian.PutUint32(identity.Mount[8:12], statx.Dev_major)
		binary.LittleEndian.PutUint32(identity.Mount[12:16], statx.Dev_minor)
		binary.LittleEndian.PutUint64(identity.File[0:8], statx.Ino)
		return identity, uint32(statx.Mode) & unix.S_IFMT, nil
	}
	var stat unix.Stat_t
	if fallbackErr := unix.Fstatat(directoryFD, name, &stat, 0); fallbackErr != nil {
		if err != nil {
			return Identity{}, 0, err
		}
		return Identity{}, 0, fallbackErr
	}
	identity := Identity{Platform: pathspec.POSIX}
	binary.LittleEndian.PutUint64(identity.Mount[0:8], uint64(stat.Dev))
	binary.LittleEndian.PutUint64(identity.File[0:8], stat.Ino)
	return identity, stat.Mode & unix.S_IFMT, nil
}

func entryKindFromUnixMode(mode uint32) EntryKind {
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

func classifyLinuxEnumerationError(err error) error {
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
