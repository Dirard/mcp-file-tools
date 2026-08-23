//go:build linux

package rootfs

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

const (
	linuxPathComponentFlags       = unix.O_PATH | unix.O_CLOEXEC
	linuxStrictPathComponentFlags = linuxPathComponentFlags | unix.O_NOFOLLOW
)

func openLinuxFallbackDir(root platformRoot, path pathspec.Relative) (platformDir, Identity, error) {
	if !root.mountProof {
		return platformDir{}, Identity{}, ErrIO
	}
	if path.String() == "." {
		fd, err := unix.Openat(root.fd, ".", linuxDirectoryFlags|unix.O_NOFOLLOW, 0)
		if err != nil {
			return platformDir{}, Identity{}, classifyFallbackOpenError(err, true)
		}
		handle := platformDir{fd: fd, valid: true}
		identity, err := verifyLinuxFallbackIdentity(fd, root, false)
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

	components := path.Components()
	parentFD, finalName, throughSymlink, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, err := linuxFallbackFinalIsSymlink(parentFD, finalName)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	throughSymlink = throughSymlink || finalSymlink
	pathFD, err := unix.Openat(parentFD, finalName, linuxPathComponentFlags, 0)
	if err != nil {
		return platformDir{}, Identity{}, classifyFallbackOpenError(err, true)
	}
	pathHandle := platformDir{fd: pathFD, valid: true}
	defer closePlatformDir(&pathHandle)
	mode, err := linuxFileMode(pathFD)
	if err != nil {
		return platformDir{}, Identity{}, ErrIO
	}
	if mode != unix.S_IFDIR {
		return platformDir{}, Identity{}, ErrNotDirectory
	}
	pathIdentity, err := verifyLinuxFallbackIdentity(pathFD, root, throughSymlink)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	readFD, err := unix.Openat(pathFD, ".", linuxDirectoryFlags|unix.O_NOFOLLOW, 0)
	if err != nil {
		return platformDir{}, Identity{}, classifyFallbackOpenError(err, true)
	}
	handle := platformDir{fd: readFD, valid: true}
	identity, err := verifyLinuxFallbackIdentity(readFD, root, throughSymlink)
	if err != nil {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, err
	}
	if identity != pathIdentity {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, ErrSourceChanged
	}
	return handle, identity, nil
}

func openLinuxFallbackRegular(root platformRoot, path pathspec.Relative) (platformFile, Identity, error) {
	if !root.mountProof {
		return platformFile{}, Identity{}, ErrIO
	}
	if path.String() == "." {
		return platformFile{}, Identity{}, ErrNotRegular
	}
	components := path.Components()
	parentFD, finalName, throughSymlink, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, symlinkErr := linuxFallbackFinalIsSymlink(parentFD, finalName)
	if symlinkErr != nil {
		return platformFile{}, Identity{}, symlinkErr
	}
	throughSymlink = throughSymlink || finalSymlink
	return openLinuxFallbackRegularAt(root, parentFD, finalName, throughSymlink)
}

func openLinuxFallbackRegularAt(root platformRoot, parentFD int, finalName string, throughSymlink bool) (platformFile, Identity, error) {
	fd, err := unix.Openat(parentFD, finalName, linuxRegularFileFlags, 0)
	if err != nil {
		return platformFile{}, Identity{}, classifyFallbackOpenError(err, false)
	}
	handle := platformFile{fd: fd, valid: true}
	mode, err := linuxFileMode(fd)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, ErrIO
	}
	if mode != unix.S_IFREG {
		_ = closePlatformFile(&handle)
		if mode == unix.S_IFDIR {
			return platformFile{}, Identity{}, ErrNotRegular
		}
		return platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := verifyLinuxFallbackIdentity(fd, root, throughSymlink)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, err
	}
	return handle, identity, nil
}

func openLinuxFallbackSearchTarget(root platformRoot, path pathspec.Relative) (SearchTargetKind, platformDir, platformFile, Identity, error) {
	if !root.mountProof {
		return 0, platformDir{}, platformFile{}, Identity{}, ErrIO
	}
	if path.String() == "." {
		fd, err := unix.Openat(root.fd, ".", linuxSearchTargetFlags, 0)
		if err != nil {
			return 0, platformDir{}, platformFile{}, Identity{}, classifyFallbackOpenError(err, true)
		}
		identity, err := verifyLinuxFallbackIdentity(fd, root, false)
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
	parentFD, finalName, throughSymlink, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	finalSymlink, symlinkErr := linuxFallbackFinalIsSymlink(parentFD, finalName)
	if symlinkErr != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, symlinkErr
	}
	throughSymlink = throughSymlink || finalSymlink
	fd, err := unix.Openat(parentFD, finalName, linuxSearchTargetFlags, 0)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, classifyFallbackOpenError(err, false)
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
	identity, err := verifyLinuxFallbackIdentity(fd, root, throughSymlink)
	if err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	if mode == unix.S_IFDIR {
		return SearchTargetDirectory, platformDir{fd: fd, valid: true}, platformFile{}, identity, nil
	}
	return SearchTargetRegular, platformDir{}, platformFile{fd: fd, valid: true}, identity, nil
}

func openLinuxFallbackParent(root platformRoot, components []string) (int, string, bool, error) {
	if len(components) == 0 {
		return -1, "", false, ErrIO
	}
	parentFD, err := unix.FcntlInt(uintptr(root.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, "", false, classifyFallbackOpenError(err, true)
	}
	throughSymlink := false
	for _, component := range components[:len(components)-1] {
		childFD, openErr := unix.Openat(parentFD, component, linuxStrictPathComponentFlags, 0)
		if openErr != nil {
			_ = unix.Close(parentFD)
			return -1, "", false, classifyFallbackOpenError(openErr, true)
		}
		mode, modeErr := linuxFileMode(childFD)
		if modeErr != nil {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", false, ErrIO
		}
		if mode == unix.S_IFLNK {
			_ = unix.Close(childFD)
			childFD, openErr = unix.Openat(parentFD, component, linuxPathComponentFlags, 0)
			if openErr != nil {
				_ = unix.Close(parentFD)
				return -1, "", false, classifyFallbackOpenError(openErr, true)
			}
			throughSymlink = true
			if mode, modeErr = linuxFileMode(childFD); modeErr != nil {
				_ = unix.Close(childFD)
				_ = unix.Close(parentFD)
				return -1, "", false, ErrIO
			}
		}
		if mode != unix.S_IFDIR {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", false, ErrNotDirectory
		}
		if _, proofErr := verifyLinuxFallbackIdentity(childFD, root, throughSymlink); proofErr != nil {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", false, proofErr
		}
		_ = unix.Close(parentFD)
		parentFD = childFD
	}
	return parentFD, components[len(components)-1], throughSymlink, nil
}

func verifyLinuxFallbackIdentity(fd int, root platformRoot, allowCrossMount bool) (Identity, error) {
	identity, mountProof, err := linuxIdentityEvidence(fd)
	if err != nil || !mountProof {
		return Identity{}, ErrIO
	}
	if !allowCrossMount && identity.Mount != root.mount {
		return Identity{}, ErrMountBoundary
	}
	return identity, nil
}

func linuxFallbackFinalIsSymlink(parentFD int, name string) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, classifyFallbackOpenError(err, false)
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFLNK, nil
}

func linuxFileMode(fd int) (uint32, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, err
	}
	return stat.Mode & unix.S_IFMT, nil
}

func classifyFallbackOpenError(err error, directory bool) error {
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
	case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.ESTALE):
		return ErrSourceChanged
	case errors.Is(err, unix.ENXIO), errors.Is(err, unix.ENODEV):
		return ErrSpecial
	default:
		return ErrIO
	}
}
