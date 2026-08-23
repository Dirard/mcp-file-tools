//go:build darwin

package rootfs

import (
	"encoding/binary"
	"errors"
	"runtime"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

type platformRoot struct {
	fd       int
	valid    bool
	mount    [16]byte
	identity Identity
}

type platformDir struct {
	fd    int
	valid bool
}

type platformFile struct {
	fd    int
	valid bool
}

const (
	darwinDirectoryFlags         = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC
	darwinStrictDirectoryFlags   = darwinDirectoryFlags | unix.O_NOFOLLOW
	darwinRegularFileFlags       = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK
	darwinStrictRegularFileFlags = darwinRegularFileFlags | unix.O_NOFOLLOW
	darwinSearchTargetFlags      = darwinRegularFileFlags
)

func openPlatformRoot(directory pathspec.RootDirectory) (platformRoot, string, Identity, error) {
	if directory.Target() != pathspec.POSIX {
		return platformRoot{}, "", Identity{}, ErrInvalidTarget
	}
	fd, err := unix.Open(directory.String(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return platformRoot{}, "", Identity{}, classifyDarwinOpenError(err, true)
	}
	handle := platformRoot{fd: fd, valid: true}
	canonical, err := darwinPathFromFD(fd)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	identity, err := darwinIdentity(fd)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	handle.mount = identity.Mount
	handle.identity = identity
	return handle, canonical, identity, nil
}

func duplicatePlatformRoot(handle platformRoot) (platformRoot, error) {
	if !handle.valid {
		return platformRoot{}, ErrClosed
	}
	fd, err := unix.FcntlInt(uintptr(handle.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return platformRoot{}, err
	}
	return platformRoot{fd: fd, valid: true, mount: handle.mount, identity: handle.identity}, nil
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

func openPlatformDir(root platformRoot, path pathspec.Relative) (platformDir, Identity, error) {
	if !root.valid {
		return platformDir{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.POSIX {
		return platformDir{}, Identity{}, ErrInvalidTarget
	}
	if path.String() == "." {
		fd, err := unix.Openat(root.fd, ".", darwinDirectoryFlags, 0)
		if err != nil {
			return platformDir{}, Identity{}, classifyDarwinOpenError(err, true)
		}
		handle := platformDir{fd: fd, valid: true}
		identity, err := verifyDarwinIdentity(fd, root.mount)
		if err != nil {
			_ = closePlatformDir(&handle)
			return platformDir{}, Identity{}, err
		}
		if identity != root.identity {
			_ = closePlatformDir(&handle)
			return platformDir{}, Identity{}, ErrSourceChanged
		}
		return handle, identity, nil
	}

	parentFD, finalName, throughSymlink, err := openDarwinParent(root, path.Components())
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, symlinkErr := darwinFinalIsSymlink(parentFD, finalName)
	if symlinkErr != nil {
		return platformDir{}, Identity{}, symlinkErr
	}
	throughSymlink = throughSymlink || finalSymlink
	fd, err := unix.Openat(parentFD, finalName, darwinDirectoryFlags, 0)
	if err != nil {
		return platformDir{}, Identity{}, classifyDarwinOpenError(err, true)
	}
	handle := platformDir{fd: fd, valid: true}
	identity, err := verifyDarwinIdentityAllow(fd, root.mount, throughSymlink)
	if err != nil {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, err
	}
	if !throughSymlink {
		if ancestryErr := verifyDarwinAncestry(fd, len(path.Components()), root); ancestryErr != nil {
			_ = closePlatformDir(&handle)
			return platformDir{}, Identity{}, ancestryErr
		}
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
	if path.String() == "." {
		return platformFile{}, Identity{}, ErrNotRegular
	}
	parentFD, finalName, throughSymlink, err := openDarwinParent(root, path.Components())
	if err != nil {
		return platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, symlinkErr := darwinFinalIsSymlink(parentFD, finalName)
	if symlinkErr != nil {
		return platformFile{}, Identity{}, symlinkErr
	}
	throughSymlink = throughSymlink || finalSymlink
	return openDarwinRegularAt(root, parentFD, len(path.Components())-1, finalName, throughSymlink)
}

func openDarwinRegularAt(root platformRoot, parentFD, parentDepth int, finalName string, throughSymlink bool) (platformFile, Identity, error) {
	fd, err := unix.Openat(parentFD, finalName, darwinRegularFileFlags, 0)
	if err != nil {
		return platformFile{}, Identity{}, classifyDarwinOpenError(err, false)
	}
	handle := platformFile{fd: fd, valid: true}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, ErrIO
	}
	mode := uint32(stat.Mode) & unix.S_IFMT
	if mode != unix.S_IFREG {
		_ = closePlatformFile(&handle)
		if mode == unix.S_IFDIR {
			return platformFile{}, Identity{}, ErrNotRegular
		}
		return platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := verifyDarwinIdentityAllow(fd, root.mount, throughSymlink)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, err
	}
	if !throughSymlink {
		if ancestryErr := verifyDarwinAncestry(parentFD, parentDepth, root); ancestryErr != nil {
			_ = closePlatformFile(&handle)
			return platformFile{}, Identity{}, ancestryErr
		}
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
	if path.String() == "." {
		fd, err := unix.Openat(root.fd, ".", darwinDirectoryFlags, 0)
		if err != nil {
			return 0, platformDir{}, platformFile{}, Identity{}, classifyDarwinOpenError(err, true)
		}
		identity, err := verifyDarwinIdentity(fd, root.mount)
		if err != nil {
			_ = unix.Close(fd)
			return 0, platformDir{}, platformFile{}, Identity{}, err
		}
		if identity != root.identity {
			_ = unix.Close(fd)
			return 0, platformDir{}, platformFile{}, Identity{}, ErrSourceChanged
		}
		return SearchTargetDirectory, platformDir{fd: fd, valid: true}, platformFile{}, identity, nil
	}
	components := path.Components()
	parentFD, finalName, throughSymlink, err := openDarwinParent(root, components)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, symlinkErr := darwinFinalIsSymlink(parentFD, finalName)
	if symlinkErr != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, symlinkErr
	}
	throughSymlink = throughSymlink || finalSymlink
	fd, err := unix.Openat(parentFD, finalName, darwinSearchTargetFlags, 0)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, classifyDarwinOpenError(err, false)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrIO
	}
	mode := uint32(stat.Mode) & unix.S_IFMT
	if mode != unix.S_IFDIR && mode != unix.S_IFREG {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := verifyDarwinIdentityAllow(fd, root.mount, throughSymlink)
	if err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	ancestryFD := parentFD
	ancestryDepth := len(components) - 1
	if mode == unix.S_IFDIR {
		ancestryFD = fd
		ancestryDepth = len(components)
	}
	if !throughSymlink {
		if ancestryErr := verifyDarwinAncestry(ancestryFD, ancestryDepth, root); ancestryErr != nil {
			_ = unix.Close(fd)
			return 0, platformDir{}, platformFile{}, Identity{}, ancestryErr
		}
	}
	if mode == unix.S_IFDIR {
		return SearchTargetDirectory, platformDir{fd: fd, valid: true}, platformFile{}, identity, nil
	}
	return SearchTargetRegular, platformDir{}, platformFile{fd: fd, valid: true}, identity, nil
}

func verifyDarwinAncestry(directoryFD, depth int, root platformRoot) error {
	currentFD := directoryFD
	owned := false
	defer func() {
		if owned {
			_ = unix.Close(currentFD)
		}
	}()

	for step := 0; step < depth; step++ {
		parentFD, err := unix.Openat(currentFD, "..", darwinDirectoryFlags, 0)
		if err != nil {
			return classifyDarwinOpenError(err, true)
		}
		if owned {
			_ = unix.Close(currentFD)
		}
		currentFD = parentFD
		owned = true
		if _, err := verifyDarwinIdentity(currentFD, root.mount); err != nil {
			return err
		}
	}

	identity, err := verifyDarwinIdentity(currentFD, root.mount)
	if err != nil {
		return err
	}
	if identity != root.identity {
		return ErrSourceChanged
	}
	return nil
}

func openDarwinParent(root platformRoot, components []string) (int, string, bool, error) {
	if len(components) == 0 {
		return -1, "", false, ErrIO
	}
	parentFD, err := unix.FcntlInt(uintptr(root.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, "", false, classifyDarwinOpenError(err, true)
	}
	throughSymlink := false
	for _, component := range components[:len(components)-1] {
		childFD, openErr := unix.Openat(parentFD, component, darwinStrictDirectoryFlags, 0)
		if openErr != nil {
			_ = unix.Close(parentFD)
			if !errors.Is(openErr, unix.ELOOP) {
				return -1, "", false, classifyDarwinOpenError(openErr, true)
			}
			childFD, openErr = unix.Openat(parentFD, component, darwinDirectoryFlags, 0)
			if openErr != nil {
				_ = unix.Close(parentFD)
				return -1, "", false, classifyDarwinOpenError(openErr, true)
			}
			throughSymlink = true
		}
		if _, proofErr := verifyDarwinIdentityAllow(childFD, root.mount, throughSymlink); proofErr != nil {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", false, proofErr
		}
		_ = unix.Close(parentFD)
		parentFD = childFD
	}
	return parentFD, components[len(components)-1], throughSymlink, nil
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

func darwinIdentity(fd int) (Identity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return Identity{}, err
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(fd, &filesystem); err != nil {
		return Identity{}, err
	}
	identity := Identity{Platform: pathspec.POSIX}
	binary.LittleEndian.PutUint32(identity.Mount[0:4], uint32(filesystem.Fsid.Val[0]))
	binary.LittleEndian.PutUint32(identity.Mount[4:8], uint32(filesystem.Fsid.Val[1]))
	binary.LittleEndian.PutUint32(identity.Mount[8:12], uint32(stat.Dev))
	binary.LittleEndian.PutUint64(identity.File[0:8], stat.Ino)
	binary.LittleEndian.PutUint32(identity.File[8:12], stat.Gen)
	return identity, nil
}

func verifyDarwinIdentity(fd int, rootMount [16]byte) (Identity, error) {
	return verifyDarwinIdentityAllow(fd, rootMount, false)
}

func verifyDarwinIdentityAllow(fd int, rootMount [16]byte, allowCrossMount bool) (Identity, error) {
	identity, err := darwinIdentity(fd)
	if err != nil {
		return Identity{}, ErrIO
	}
	if !allowCrossMount && identity.Mount != rootMount {
		return Identity{}, ErrMountBoundary
	}
	return identity, nil
}

func darwinFinalIsSymlink(parentFD int, name string) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, classifyDarwinOpenError(err, false)
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFLNK, nil
}

func darwinPathFromFD(fd int) (string, error) {
	var buffer [4096]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buffer[0])))
	runtime.KeepAlive(&buffer)
	if errno != 0 {
		return "", errno
	}
	path := unix.ByteSliceToString(buffer[:])
	if path == "" {
		return "", ErrIO
	}
	return path, nil
}

func classifyDarwinOpenError(err error, directory bool) error {
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
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return ErrPermissionDenied
	case errors.Is(err, unix.ESTALE):
		return ErrSourceChanged
	case errors.Is(err, unix.ENXIO), errors.Is(err, unix.ENODEV):
		return ErrSpecial
	default:
		return ErrIO
	}
}
