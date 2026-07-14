//go:build windows

package rootfs

import (
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
	"golang.org/x/sys/windows"
)

const (
	windowsShareMode        = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	windowsDirectoryAccess  = windows.FILE_LIST_DIRECTORY | windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
	windowsRegularAccess    = windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
	windowsSearchAccess     = windowsRegularAccess
	windowsDirectoryOptions = windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT
	windowsRegularOptions   = windows.FILE_NON_DIRECTORY_FILE | windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT
	windowsSearchOptions    = windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT
	windowsMaxFinalPath     = windows.MAX_LONG_PATH
)

type platformRoot struct {
	handle   windows.Handle
	valid    bool
	volume   [16]byte
	identity Identity
}

type platformDir struct {
	handle   windows.Handle
	valid    bool
	resolved pathspec.Relative
}

type platformFile struct {
	handle   windows.Handle
	valid    bool
	resolved pathspec.Relative
}

type windowsFileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

type windowsFileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

func openPlatformRoot(directory pathspec.RootDirectory) (platformRoot, string, Identity, error) {
	if directory.Target() != pathspec.Windows {
		return platformRoot{}, "", Identity{}, ErrInvalidTarget
	}
	name, err := windows.UTF16PtrFromString(`\\?\` + strings.ReplaceAll(directory.String(), "/", `\`))
	if err != nil {
		return platformRoot{}, "", Identity{}, ErrIO
	}
	handleValue, err := windows.CreateFile(
		name,
		windowsDirectoryAccess,
		windowsShareMode,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return platformRoot{}, "", Identity{}, classifyWindowsOpenError(err, true)
	}
	handle := platformRoot{handle: handleValue, valid: true}
	attributes, tag, err := windowsAttributeTag(handleValue)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, classifyWindowsReparseTag(tag)
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrNotDirectory
	}
	identity, err := windowsIdentity(handleValue)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	canonical, err := windowsFinalPath(handleValue)
	if err != nil {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	parsedCanonical, code := pathspec.ParseRootDirectory(pathspec.Windows, canonical)
	if code != "" {
		_ = closePlatformRoot(&handle)
		return platformRoot{}, "", Identity{}, ErrIO
	}
	handle.volume = identity.Mount
	handle.identity = identity
	return handle, parsedCanonical.String(), identity, nil
}

func duplicatePlatformRoot(handle platformRoot) (platformRoot, error) {
	if !handle.valid {
		return platformRoot{}, ErrClosed
	}
	duplicate, err := duplicateWindowsHandle(handle.handle)
	if err != nil {
		return platformRoot{}, err
	}
	return platformRoot{handle: duplicate, valid: true, volume: handle.volume, identity: handle.identity}, nil
}

func closePlatformRoot(handle *platformRoot) error {
	if handle == nil || !handle.valid {
		return nil
	}
	value := handle.handle
	handle.handle = windows.InvalidHandle
	handle.valid = false
	return windows.CloseHandle(value)
}

func openPlatformDir(root platformRoot, path pathspec.Relative) (platformDir, Identity, error) {
	if !root.valid {
		return platformDir{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.Windows {
		return platformDir{}, Identity{}, ErrInvalidTarget
	}
	if path.String() == "." {
		handleValue, identity, err := reopenWindowsDirectory(root)
		if err != nil {
			return platformDir{}, Identity{}, err
		}
		return platformDir{handle: handleValue, valid: true, resolved: path}, identity, nil
	}
	components := path.Components()
	parent, actual, err := openWindowsParent(root, components)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	defer windows.CloseHandle(parent)
	handleValue, err := openWindowsRelative(parent, components[len(components)-1], true)
	if err != nil {
		return platformDir{}, Identity{}, err
	}
	identity, actualName, err := verifyWindowsHandle(handleValue, root.volume, true)
	if err != nil {
		_ = windows.CloseHandle(handleValue)
		return platformDir{}, Identity{}, err
	}
	if !strings.EqualFold(actualName, components[len(components)-1]) {
		_ = windows.CloseHandle(handleValue)
		return platformDir{}, Identity{}, ErrSourceChanged
	}
	if err := verifyWindowsPathContained(handleValue, root); err != nil {
		_ = windows.CloseHandle(handleValue)
		return platformDir{}, Identity{}, err
	}
	actual = append(actual, actualName)
	resolved, err := windowsResolvedRelative(actual)
	if err != nil {
		_ = windows.CloseHandle(handleValue)
		return platformDir{}, Identity{}, err
	}
	return platformDir{handle: handleValue, valid: true, resolved: resolved}, identity, nil
}

func openPlatformRegular(root platformRoot, path pathspec.Relative) (platformFile, Identity, error) {
	if !root.valid {
		return platformFile{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.Windows {
		return platformFile{}, Identity{}, ErrInvalidTarget
	}
	if path.String() == "." {
		return platformFile{}, Identity{}, ErrNotRegular
	}
	components := path.Components()
	parent, actual, err := openWindowsParent(root, components)
	if err != nil {
		return platformFile{}, Identity{}, err
	}
	defer windows.CloseHandle(parent)
	handle, identity, actualName, err := openWindowsRegularAt(root, parent, components[len(components)-1])
	if err != nil {
		return platformFile{}, Identity{}, err
	}
	actual = append(actual, actualName)
	resolved, err := windowsResolvedRelative(actual)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, err
	}
	handle.resolved = resolved
	return handle, identity, nil
}

func openWindowsRegularAt(root platformRoot, parent windows.Handle, finalName string) (platformFile, Identity, string, error) {
	handleValue, err := openWindowsRelative(parent, finalName, false)
	if err != nil {
		return platformFile{}, Identity{}, "", err
	}
	handle := platformFile{handle: handleValue, valid: true}
	identity, actualName, err := verifyWindowsHandle(handleValue, root.volume, false)
	if err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, "", err
	}
	if !strings.EqualFold(actualName, finalName) {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, "", ErrSourceChanged
	}
	if err := verifyWindowsPathContained(handleValue, root); err != nil {
		_ = closePlatformFile(&handle)
		return platformFile{}, Identity{}, "", err
	}
	return handle, identity, actualName, nil
}

func openPlatformSearchTarget(root platformRoot, path pathspec.Relative) (SearchTargetKind, platformDir, platformFile, Identity, error) {
	if !root.valid {
		return 0, platformDir{}, platformFile{}, Identity{}, ErrClosed
	}
	if path.Target() != pathspec.Windows {
		return 0, platformDir{}, platformFile{}, Identity{}, ErrInvalidTarget
	}
	if path.String() == "." {
		handleValue, identity, err := reopenWindowsDirectory(root)
		if err != nil {
			return 0, platformDir{}, platformFile{}, Identity{}, err
		}
		return SearchTargetDirectory, platformDir{handle: handleValue, valid: true, resolved: path}, platformFile{}, identity, nil
	}
	components := path.Components()
	parent, actual, err := openWindowsParent(root, components)
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	defer windows.CloseHandle(parent)
	handleValue, err := openWindowsSearchRelative(parent, components[len(components)-1])
	if err != nil {
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	kind, identity, actualName, err := verifyWindowsSearchHandle(handleValue, root.volume)
	if err != nil {
		_ = windows.CloseHandle(handleValue)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	if !strings.EqualFold(actualName, components[len(components)-1]) {
		_ = windows.CloseHandle(handleValue)
		return 0, platformDir{}, platformFile{}, Identity{}, ErrSourceChanged
	}
	if err := verifyWindowsPathContained(handleValue, root); err != nil {
		_ = windows.CloseHandle(handleValue)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	actual = append(actual, actualName)
	resolved, err := windowsResolvedRelative(actual)
	if err != nil {
		_ = windows.CloseHandle(handleValue)
		return 0, platformDir{}, platformFile{}, Identity{}, err
	}
	if kind == SearchTargetDirectory {
		return kind, platformDir{handle: handleValue, valid: true, resolved: resolved}, platformFile{}, identity, nil
	}
	return kind, platformDir{}, platformFile{handle: handleValue, valid: true, resolved: resolved}, identity, nil
}

func openWindowsParent(root platformRoot, components []string) (windows.Handle, []string, error) {
	if len(components) == 0 {
		return windows.InvalidHandle, nil, ErrIO
	}
	parent, err := duplicateWindowsHandle(root.handle)
	if err != nil {
		return windows.InvalidHandle, nil, classifyWindowsOpenError(err, true)
	}
	actual := make([]string, 0, len(components)-1)
	for _, component := range components[:len(components)-1] {
		child, openErr := openWindowsRelative(parent, component, true)
		if openErr != nil {
			_ = windows.CloseHandle(parent)
			return windows.InvalidHandle, nil, openErr
		}
		_, actualName, verifyErr := verifyWindowsHandle(child, root.volume, true)
		if verifyErr != nil {
			_ = windows.CloseHandle(child)
			_ = windows.CloseHandle(parent)
			return windows.InvalidHandle, nil, verifyErr
		}
		if !strings.EqualFold(actualName, component) {
			_ = windows.CloseHandle(child)
			_ = windows.CloseHandle(parent)
			return windows.InvalidHandle, nil, ErrSourceChanged
		}
		actual = append(actual, actualName)
		_ = windows.CloseHandle(parent)
		parent = child
	}
	return parent, actual, nil
}

func openWindowsRelative(parent windows.Handle, component string, directory bool) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(component)
	if err != nil {
		return windows.InvalidHandle, ErrIO
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	access := uint32(windowsRegularAccess)
	options := uint32(windowsRegularOptions)
	if directory {
		access = windowsDirectoryAccess
		options = windowsDirectoryOptions
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access,
		&attributes,
		&status,
		nil,
		0,
		windowsShareMode,
		windows.FILE_OPEN,
		options,
		0,
		0,
	)
	runtime.KeepAlive(objectName)
	if err != nil {
		return windows.InvalidHandle, classifyWindowsOpenError(err, directory)
	}
	return handle, nil
}

func openWindowsSearchRelative(parent windows.Handle, component string) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(component)
	if err != nil {
		return windows.InvalidHandle, ErrIO
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		windowsSearchAccess,
		&attributes,
		&status,
		nil,
		0,
		windowsShareMode,
		windows.FILE_OPEN,
		windowsSearchOptions,
		0,
		0,
	)
	runtime.KeepAlive(objectName)
	if err != nil {
		return windows.InvalidHandle, classifyWindowsOpenError(err, false)
	}
	return handle, nil
}

func reopenWindowsDirectory(root platformRoot) (windows.Handle, Identity, error) {
	canonical, err := windowsFinalPath(root.handle)
	if err != nil {
		return windows.InvalidHandle, Identity{}, ErrSourceChanged
	}
	directory, code := pathspec.ParseRootDirectory(pathspec.Windows, canonical)
	if code != "" {
		return windows.InvalidHandle, Identity{}, ErrSourceChanged
	}
	reopened, _, identity, err := openPlatformRoot(directory)
	if err != nil {
		return windows.InvalidHandle, Identity{}, err
	}
	if identity != root.identity {
		_ = closePlatformRoot(&reopened)
		return windows.InvalidHandle, Identity{}, ErrSourceChanged
	}
	handle := reopened.handle
	reopened.handle = windows.InvalidHandle
	reopened.valid = false
	return handle, identity, nil
}

func verifyWindowsPathContained(handle windows.Handle, root platformRoot) error {
	rootPath, err := windowsFinalPath(root.handle)
	if err != nil {
		return ErrSourceChanged
	}
	openedPath, err := windowsFinalPath(handle)
	if err != nil {
		return ErrSourceChanged
	}
	if strings.EqualFold(openedPath, rootPath) {
		return nil
	}
	prefix := rootPath
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if len(openedPath) < len(prefix) || !strings.EqualFold(openedPath[:len(prefix)], prefix) {
		return ErrSourceChanged
	}
	return nil
}

func verifyWindowsHandle(handle windows.Handle, rootVolume [16]byte, directory bool) (Identity, string, error) {
	identity, err := verifyWindowsHandleIdentity(handle, rootVolume, directory)
	if err != nil {
		return Identity{}, "", err
	}
	actualName, err := windowsHandleLeafName(handle)
	if err != nil {
		return Identity{}, "", err
	}
	return identity, actualName, nil
}

func verifyWindowsHandleIdentity(handle windows.Handle, rootVolume [16]byte, directory bool) (Identity, error) {
	attributes, tag, err := windowsAttributeTag(handle)
	if err != nil {
		return Identity{}, ErrIO
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return Identity{}, classifyWindowsReparseTag(tag)
	}
	if directory {
		if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return Identity{}, ErrNotDirectory
		}
	} else {
		if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return Identity{}, ErrNotRegular
		}
		if attributes&windows.FILE_ATTRIBUTE_DEVICE != 0 {
			return Identity{}, ErrSpecial
		}
		fileType, typeErr := windows.GetFileType(handle)
		if typeErr != nil {
			return Identity{}, ErrIO
		}
		if fileType != windows.FILE_TYPE_DISK {
			return Identity{}, ErrSpecial
		}
	}
	identity, err := windowsIdentity(handle)
	if err != nil {
		return Identity{}, ErrIO
	}
	if identity.Mount != rootVolume {
		return Identity{}, ErrMountBoundary
	}
	return identity, nil
}

func verifyWindowsSearchHandle(handle windows.Handle, rootVolume [16]byte) (SearchTargetKind, Identity, string, error) {
	attributes, tag, err := windowsAttributeTag(handle)
	if err != nil {
		return 0, Identity{}, "", ErrIO
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return 0, Identity{}, "", classifyWindowsReparseTag(tag)
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return 0, Identity{}, "", ErrIO
	}
	if fileType != windows.FILE_TYPE_DISK || attributes&windows.FILE_ATTRIBUTE_DEVICE != 0 {
		return 0, Identity{}, "", ErrSpecial
	}
	kind := SearchTargetRegular
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		kind = SearchTargetDirectory
	}
	identity, err := windowsIdentity(handle)
	if err != nil {
		return 0, Identity{}, "", ErrIO
	}
	if identity.Mount != rootVolume {
		return 0, Identity{}, "", ErrMountBoundary
	}
	actualName, err := windowsHandleLeafName(handle)
	if err != nil {
		return 0, Identity{}, "", err
	}
	return kind, identity, actualName, nil
}

func windowsAttributeTag(handle windows.Handle) (uint32, uint32, error) {
	var information windowsFileAttributeTagInfo
	err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	if err != nil {
		return 0, 0, err
	}
	return information.FileAttributes, information.ReparseTag, nil
}

func windowsIdentity(handle windows.Handle) (Identity, error) {
	var information windowsFileIDInfo
	err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileIdInfo,
		(*byte)(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	if err != nil {
		return Identity{}, err
	}
	identity := Identity{Platform: pathspec.Windows, File: information.FileID}
	binary.LittleEndian.PutUint64(identity.Mount[:8], information.VolumeSerialNumber)
	return identity, nil
}

func windowsHandleLeafName(handle windows.Handle) (string, error) {
	path, err := windowsFinalPath(handle)
	if err != nil {
		return "", ErrSourceChanged
	}
	separator := strings.LastIndexByte(path, '/')
	if separator < 0 || separator+1 >= len(path) {
		return "", ErrSourceChanged
	}
	return path[separator+1:], nil
}

func windowsFinalPath(handle windows.Handle) (string, error) {
	size := uint32(512)
	for size <= windowsMaxFinalPath {
		buffer := make([]uint16, size)
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err == nil && length < size {
			raw := buffer[:length]
			if len(raw) > 0 && raw[len(raw)-1] == 0 {
				raw = raw[:len(raw)-1]
			}
			decoded, ok := decodeWindowsUTF16(raw)
			if !ok || decoded == "" {
				return "", ErrIO
			}
			if strings.HasPrefix(decoded, `\\?\`) {
				decoded = decoded[4:]
			}
			return strings.ReplaceAll(decoded, `\`, "/"), nil
		}
		if length >= size {
			size = length + 1
			continue
		}
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			size *= 2
			continue
		}
		return "", err
	}
	return "", ErrIO
}

func windowsResolvedRelative(components []string) (pathspec.Relative, error) {
	if len(components) == 0 {
		return pathspec.Relative{}, ErrSourceChanged
	}
	resolved, code := pathspec.ParseRelative(pathspec.Windows, strings.Join(components, "/"), false)
	if code != "" {
		return pathspec.Relative{}, ErrSourceChanged
	}
	return resolved, nil
}

func duplicateWindowsHandle(handle windows.Handle) (windows.Handle, error) {
	current := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(current, handle, current, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return windows.InvalidHandle, err
	}
	return duplicate, nil
}

func closePlatformDir(handle *platformDir) error {
	if handle == nil || !handle.valid {
		return nil
	}
	value := handle.handle
	handle.handle = windows.InvalidHandle
	handle.valid = false
	return windows.CloseHandle(value)
}

func closePlatformFile(handle *platformFile) error {
	if handle == nil || !handle.valid {
		return nil
	}
	value := handle.handle
	handle.handle = windows.InvalidHandle
	handle.valid = false
	return windows.CloseHandle(value)
}

func readPlatformFile(handle platformFile, destination []byte) (int, error) {
	if !handle.valid {
		return 0, ErrClosed
	}
	var read uint32
	err := windows.ReadFile(handle.handle, destination, &read, nil)
	if errors.Is(err, windows.ERROR_HANDLE_EOF) {
		return 0, nil
	}
	return int(read), err
}

func resolvedPlatformDir(handle platformDir, _ pathspec.Relative) pathspec.Relative {
	return handle.resolved
}

func resolvedPlatformFile(handle platformFile, _ pathspec.Relative) pathspec.Relative {
	return handle.resolved
}

func classifyWindowsReparseTag(tag uint32) error {
	if tag == windows.IO_REPARSE_TAG_MOUNT_POINT {
		return ErrMountBoundary
	}
	return ErrSymlink
}

func classifyWindowsOpenError(err error, directory bool) error {
	switch {
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND),
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND),
		windowsStatusIs(err, windows.STATUS_OBJECT_NAME_NOT_FOUND),
		windowsStatusIs(err, windows.STATUS_OBJECT_PATH_NOT_FOUND):
		return ErrNotFound
	case errors.Is(err, windows.ERROR_DIRECTORY),
		windowsStatusIs(err, windows.STATUS_NOT_A_DIRECTORY):
		if directory {
			return ErrNotDirectory
		}
		return ErrNotFound
	case windowsStatusIs(err, windows.STATUS_FILE_IS_A_DIRECTORY):
		return ErrNotRegular
	case errors.Is(err, windows.ERROR_ACCESS_DENIED),
		windowsStatusIs(err, windows.STATUS_ACCESS_DENIED):
		return ErrPermissionDenied
	case errors.Is(err, windows.ERROR_CANT_RESOLVE_FILENAME),
		windowsStatusIs(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED),
		windowsStatusIs(err, windows.STATUS_REPARSE_POINT_NOT_RESOLVED),
		windowsStatusIs(err, windows.STATUS_IO_REPARSE_TAG_NOT_HANDLED):
		return ErrSymlink
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION),
		windowsStatusIs(err, windows.STATUS_SHARING_VIOLATION),
		windowsStatusIs(err, windows.STATUS_DELETE_PENDING):
		return ErrSourceChanged
	default:
		return ErrIO
	}
}

func windowsStatusIs(err error, want windows.NTStatus) bool {
	var status windows.NTStatus
	return errors.As(err, &status) && status == want
}
