//go:build linux

package rootfs

import (
	"encoding/binary"
	"errors"
	"os"
	"strconv"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

type platformRoot struct {
	fd            int
	valid         bool
	mount         [16]byte
	identity      Identity
	mountProof    bool
	forceFallback bool
}

type platformDir struct {
	fd    int
	valid bool
}

type platformFile struct {
	fd    int
	valid bool
}

func openPlatformRoot(directory pathspec.RootDirectory) (platformRoot, string, Identity, error) {
	if directory.Target() != pathspec.POSIX {
		return platformRoot{}, "", Identity{}, ErrInvalidTarget
	}
	fd, err := unix.Open(directory.String(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return platformRoot{}, "", Identity{}, classifyRootOpenError(err)
	}
	handle := platformRoot{fd: fd, valid: true}
	canonical, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	identity, mountProof, err := linuxIdentityEvidence(fd)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	handle.mount = identity.Mount
	handle.identity = identity
	handle.mountProof = mountProof
	return handle, canonical, identity, nil
}

func closePlatformRoot(handle *platformRoot) error {
	if handle == nil || !handle.valid {
		return nil
	}
	fd := handle.fd
	handle.fd = 0
	handle.valid = false
	return unix.Close(fd)
}

func duplicatePlatformRoot(handle platformRoot) (platformRoot, error) {
	if !handle.valid {
		return platformRoot{}, ErrClosed
	}
	fd, err := unix.FcntlInt(uintptr(handle.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return platformRoot{}, err
	}
	return platformRoot{
		fd:            fd,
		valid:         true,
		mount:         handle.mount,
		identity:      handle.identity,
		mountProof:    handle.mountProof,
		forceFallback: handle.forceFallback,
	}, nil
}

const (
	linuxStrictResolve     = unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV
	linuxSymlinkResolve    = unix.RESOLVE_NO_MAGICLINKS
	linuxDirectoryFlags    = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
	linuxRegularFileFlags  = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK
	linuxSearchTargetFlags = linuxRegularFileFlags
)

func openLinuxFollowing(root platformRoot, path string, flags int) (int, error) {
	how := unix.OpenHow{Flags: uint64(flags), Resolve: uint64(linuxStrictResolve)}
	fd, err := unix.Openat2(root.fd, path, &how)
	if errors.Is(err, unix.ELOOP) {
		how.Resolve = uint64(linuxSymlinkResolve)
		return unix.Openat2(root.fd, path, &how)
	}
	return fd, err
}

func openPlatformDir(root platformRoot, path pathspec.Relative) (platformDir, Identity, error) {
	if !root.valid {
		return platformDir{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.POSIX {
		return platformDir{}, Identity{}, ErrInvalidTarget
	}
	if root.forceFallback {
		return openLinuxFallbackDir(root, path)
	}
	fd, err := openLinuxFollowing(root, path.String(), linuxDirectoryFlags)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return openLinuxFallbackDir(root, path)
		}
		return platformDir{}, Identity{}, classifyContainedOpenError(err, true)
	}
	handle := platformDir{fd: fd, valid: true}
	identity, err := linuxIdentity(fd)
	if err != nil {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, ErrIO
	}
	return handle, identity, nil
}

func openPlatformRegular(root platformRoot, path pathspec.Relative) (platformFile, Identity, error) {
	if !root.valid {
		return platformFile{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.POSIX {
		return platformFile{}, Identity{}, ErrInvalidTarget
	}
	if root.forceFallback {
		return openLinuxFallbackRegular(root, path)
	}
	fd, err := openLinuxFollowing(root, path.String(), linuxRegularFileFlags)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return openLinuxFallbackRegular(root, path)
		}
		return platformFile{}, Identity{}, classifyContainedOpenError(err, false)
	}
	handle := platformFile{fd: fd, valid: true}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, ErrIO
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = closePlatformFile(&handle)
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			return platformFile{}, Identity{}, ErrNotRegular
		}
		return platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := linuxIdentity(fd)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, ErrIO
	}
	return handle, identity, nil
}

func openPlatformSearchTarget(root platformRoot, path pathspec.Relative) (SearchTargetKind, platformDir, platformFile, Identity, error) {
	if !root.valid {
		return 0, platformDir{}, platformFile{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.POSIX {
		return 0, platformDir{}, platformFile{}, Identity{}, ErrInvalidTarget
	}
	if root.forceFallback {
		return openLinuxFallbackSearchTarget(root, path)
	}
	fd, err := openLinuxFollowing(root, path.String(), linuxSearchTargetFlags)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return openLinuxFallbackSearchTarget(root, path)
		}
		return 0, platformDir{}, platformFile{}, Identity{}, classifyContainedOpenError(err, false)
	}
	mode, err := linuxFileMode(fd)
	if err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrIO
	}
	if mode != unix.S_IFDIR && mode != unix.S_IFREG {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := linuxIdentity(fd)
	if err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrIO
	}
	if mode == unix.S_IFDIR {
		return SearchTargetDirectory, platformDir{fd: fd, valid: true}, platformFile{}, identity, nil
	}
	return SearchTargetRegular, platformDir{}, platformFile{fd: fd, valid: true}, identity, nil
}

func closePlatformDir(handle *platformDir) error {
	if handle == nil || !handle.valid {
		return nil
	}
	fd := handle.fd
	handle.fd = 0
	handle.valid = false
	return unix.Close(fd)
}

func closePlatformFile(handle *platformFile) error {
	if handle == nil || !handle.valid {
		return nil
	}
	fd := handle.fd
	handle.fd = 0
	handle.valid = false
	return unix.Close(fd)
}

func readPlatformFile(handle platformFile, destination []byte) (int, error) {
	if !handle.valid {
		return 0, ErrClosed
	}
	return unix.Read(handle.fd, destination)
}

func resolvedPlatformDir(_ platformDir, requested pathspec.Relative) pathspec.Relative {
	return requested
}

func resolvedPlatformFile(_ platformFile, requested pathspec.Relative) pathspec.Relative {
	return requested
}

func linuxIdentity(fd int) (Identity, error) {
	identity, _, err := linuxIdentityEvidence(fd)
	return identity, err
}

func linuxIdentityEvidence(fd int) (Identity, bool, error) {
	identity := Identity{Platform: pathspec.POSIX}
	var statx unix.Statx_t
	err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_INO|unix.STATX_MNT_ID, &statx)
	wantMask := uint32(unix.STATX_INO | unix.STATX_MNT_ID)
	if err == nil && statx.Mask&wantMask == wantMask {
		binary.LittleEndian.PutUint64(identity.Mount[0:8], statx.Mnt_id)
		binary.LittleEndian.PutUint32(identity.Mount[8:12], statx.Dev_major)
		binary.LittleEndian.PutUint32(identity.Mount[12:16], statx.Dev_minor)
		binary.LittleEndian.PutUint64(identity.File[0:8], statx.Ino)
		return identity, true, nil
	}

	var stat unix.Stat_t
	if fallbackErr := unix.Fstat(fd, &stat); fallbackErr != nil {
		if err != nil {
			return Identity{}, false, err
		}
		return Identity{}, false, fallbackErr
	}
	binary.LittleEndian.PutUint64(identity.Mount[0:8], uint64(stat.Dev))
	binary.LittleEndian.PutUint64(identity.File[0:8], stat.Ino)
	return identity, false, nil
}

func classifyRootOpenError(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return ErrNotFound
	case errors.Is(err, unix.ENOTDIR):
		return ErrNotDirectory
	case errors.Is(err, unix.ELOOP):
		return ErrSymlink
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return ErrPermissionDenied
	default:
		return ErrIO
	}
}

func classifyContainedOpenError(err error, directory bool) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return ErrNotFound
	case errors.Is(err, unix.ENOTDIR):
		if directory {
			return ErrNotDirectory
		}
		return ErrNotFound
	case errors.Is(err, unix.ELOOP):
		return ErrSymlink
	case errors.Is(err, unix.EXDEV):
		return ErrMountBoundary
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return ErrPermissionDenied
	case errors.Is(err, unix.ENXIO), errors.Is(err, unix.ENODEV):
		return ErrSpecial
	case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.ESTALE):
		return ErrSourceChanged
	default:
		return ErrIO
	}
}
