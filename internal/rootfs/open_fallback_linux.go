//go:build linux

package rootfs

import (
	"errors"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/unix"
)

const linuxPathComponentFlags = unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC

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
		identity, err := verifyLinuxFallbackIdentity(fd, root)
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
	parentFD, finalName, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	defer unix.Close(parentFD)
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
	if mode == unix.S_IFLNK {
		return platformDir{}, Identity{}, ErrSymlink
	}
	if mode != unix.S_IFDIR {
		return platformDir{}, Identity{}, ErrNotDirectory
	}
	pathIdentity, err := verifyLinuxFallbackIdentity(pathFD, root)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	readFD, err := unix.Openat(pathFD, ".", linuxDirectoryFlags|unix.O_NOFOLLOW, 0)
	if err != nil {
		return platformDir{}, Identity{}, classifyFallbackOpenError(err, true)
	}
	handle := platformDir{fd: readFD, valid: true}
	identity, err := verifyLinuxFallbackIdentity(readFD, root)
	if err != nil {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, err
	}
	if identity != pathIdentity {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, ErrSourceChanged
	}
	if err := verifyLinuxFallbackAncestry(readFD, len(components), root); err != nil {
		_ = closePlatformDir(&handle)
		return platformDir{}, Identity{}, err
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
	parentFD, finalName, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	return openLinuxFallbackRegularAt(root, parentFD, len(components)-1, finalName)
}

func openLinuxFallbackRegularAt(root platformRoot, parentFD, parentDepth int, finalName string) (platformFile, Identity, error) {
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
	identity, err := verifyLinuxFallbackIdentity(fd, root)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, err
	}
	if err := verifyLinuxFallbackAncestry(parentFD, parentDepth, root); err != nil {
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
		identity, err := verifyLinuxFallbackIdentity(fd, root)
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
	parentFD, finalName, err := openLinuxFallbackParent(root, components)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, finalName, linuxSearchTargetFlags, 0)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, classifyFallbackOpenError(err, false)
	}
	mode, err := linuxFileMode(fd)
	if err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrIO
	}
	if mode == unix.S_IFLNK {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrSymlink
	}
	if mode != unix.S_IFDIR && mode != unix.S_IFREG {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrSpecial
	}
	identity, err := verifyLinuxFallbackIdentity(fd, root)
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
	if err := verifyLinuxFallbackAncestry(ancestryFD, ancestryDepth, root); err != nil {
		_ = unix.Close(fd)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	if mode == unix.S_IFDIR {
		return SearchTargetDirectory, platformDir{fd: fd, valid: true}, platformFile{}, identity, nil
	}
	return SearchTargetRegular, platformDir{}, platformFile{fd: fd, valid: true}, identity, nil
}

func verifyLinuxFallbackAncestry(directoryFD, depth int, root platformRoot) error {
	currentFD := directoryFD
	owned := false
	defer func() {
		if owned {
			_ = unix.Close(currentFD)
		}
	}()

	for step := 0; step < depth; step++ {
		parentFD, err := unix.Openat(currentFD, "..", linuxPathComponentFlags, 0)
		if err != nil {
			return classifyFallbackOpenError(err, true)
		}
		if owned {
			_ = unix.Close(currentFD)
		}
		currentFD = parentFD
		owned = true
		mode, err := linuxFileMode(currentFD)
		if err != nil {
			return ErrIO
		}
		if mode != unix.S_IFDIR {
			return ErrSourceChanged
		}
		if _, err := verifyLinuxFallbackIdentity(currentFD, root); err != nil {
			return err
		}
	}

	identity, err := verifyLinuxFallbackIdentity(currentFD, root)
	if err != nil {
		return err
	}
	if identity != root.identity {
		return ErrSourceChanged
	}
	return nil
}

func openLinuxFallbackParent(root platformRoot, components []string) (int, string, error) {
	if len(components) == 0 {
		return -1, "", ErrIO
	}
	parentFD, err := unix.FcntlInt(uintptr(root.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, "", classifyFallbackOpenError(err, true)
	}
	for _, component := range components[:len(components)-1] {
		childFD, openErr := unix.Openat(parentFD, component, linuxPathComponentFlags, 0)
		if openErr != nil {
			_ = unix.Close(parentFD)
			return -1, "", classifyFallbackOpenError(openErr, true)
		}
		mode, modeErr := linuxFileMode(childFD)
		if modeErr != nil {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", ErrIO
		}
		if mode == unix.S_IFLNK {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", ErrSymlink
		}
		if mode != unix.S_IFDIR {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", ErrNotDirectory
		}
		if _, proofErr := verifyLinuxFallbackIdentity(childFD, root); proofErr != nil {
			_ = unix.Close(childFD)
			_ = unix.Close(parentFD)
			return -1, "", proofErr
		}
		_ = unix.Close(parentFD)
		parentFD = childFD
	}
	return parentFD, components[len(components)-1], nil
}

func verifyLinuxFallbackIdentity(fd int, root platformRoot) (Identity, error) {
	identity, mountProof, err := linuxIdentityEvidence(fd)
	if err != nil || !mountProof {
		return Identity{}, ErrIO
	}
	if identity.Mount != root.mount {
		return Identity{}, ErrMountBoundary
	}
	return identity, nil
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
