package rootfs

import (
	"context"
	"io"
	"math"
	"sync"

	"github.com/Dirard/mcp-file-tools/internal/pathspec"
)

// OpenRoot resolves the selected root exactly once and derives all evidence from
// that opened directory handle.
func OpenRoot(directory pathspec.RootDirectory) (*Root, error) {
	handle, canonical, identity, err := openPlatformRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Root{
		handle:    handle,
		canonical: canonical,
		identity:  identity,
	}, nil
}

// CanonicalPath returns the final path captured from the opened root handle.
func (root *Root) CanonicalPath() string {
	if root == nil {
		return ""
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	return root.canonical
}

// Identity returns the immutable identity captured from the opened root handle.
func (root *Root) Identity() Identity {
	if root == nil {
		return Identity{}
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	return root.identity
}

// Duplicate creates an independently owned close-on-exec root handle.
func (root *Root) Duplicate() (*Lease, error) {
	if root == nil {
		return nil, ErrClosed
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.closed {
		return nil, ErrClosed
	}
	handle, err := duplicatePlatformRoot(root.handle)
	if err != nil {
		return nil, ErrIO
	}
	return &Lease{handle: handle, identity: root.identity}, nil
}

// Close releases the platform handle at most once.
func (root *Root) Close() error {
	if root == nil {
		return nil
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.closed {
		return nil
	}
	root.closed = true
	if err := closePlatformRoot(&root.handle); err != nil {
		return ErrIO
	}
	return nil
}

// OpenDir opens a directory relative to this lease while no close is pending.
func (lease *Lease) OpenDir(path pathspec.Relative) (*Dir, error) {
	return lease.openDir(path, 0)
}

// OpenRegular opens and verifies a regular file relative to this lease.
func (lease *Lease) OpenRegular(path pathspec.Relative) (*File, error) {
	return lease.openRegular(path, 0)
}

// OpenSearchTarget opens and classifies one initial-search handle without reading it.
func (lease *Lease) OpenSearchTarget(path pathspec.Relative) (*SearchTarget, error) {
	if lease == nil {
		return nil, ErrClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if err := lease.operationAllowed(0); err != nil {
		return nil, err
	}
	kind, directoryHandle, fileHandle, identity, err := openPlatformSearchTarget(lease.handle, path)
	if err != nil {
		return nil, err
	}
	target := &SearchTarget{kind: kind}
	switch kind {
	case SearchTargetDirectory:
		target.directory = &Dir{
			handle:   directoryHandle,
			identity: identity,
			resolved: resolvedPlatformDir(directoryHandle, path),
		}
	case SearchTargetRegular:
		target.file = &File{
			handle:   fileHandle,
			identity: identity,
			resolved: resolvedPlatformFile(fileHandle, path),
		}
	default:
		_ = closePlatformDir(&directoryHandle)
		_ = closePlatformFile(&fileHandle)
		return nil, ErrIO
	}
	return target, nil
}

func (lease *Lease) openDir(path pathspec.Relative, generation uint64) (*Dir, error) {
	if lease == nil {
		return nil, ErrClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if err := lease.operationAllowed(generation); err != nil {
		return nil, err
	}
	handle, identity, err := openPlatformDir(lease.handle, path)
	if err != nil {
		return nil, err
	}
	return &Dir{handle: handle, identity: identity, resolved: resolvedPlatformDir(handle, path)}, nil
}

func (lease *Lease) openRegular(path pathspec.Relative, generation uint64) (*File, error) {
	if lease == nil {
		return nil, ErrClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if err := lease.operationAllowed(generation); err != nil {
		return nil, err
	}
	handle, identity, err := openPlatformRegular(lease.handle, path)
	if err != nil {
		return nil, err
	}
	return &File{handle: handle, identity: identity, resolved: resolvedPlatformFile(handle, path)}, nil
}

func (lease *Lease) operationAllowed(generation uint64) error {
	if lease.closed {
		return ErrClosed
	}
	if generation == 0 {
		if lease.closeRequested {
			return ErrClosed
		}
		return nil
	}
	if _, active := lease.activeGenerations[generation]; !active {
		return ErrBorrowExpired
	}
	return nil
}

// Close requests close immediately, or after the final active borrow returns.
func (lease *Lease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.closeRequested {
		return nil
	}
	lease.closeRequested = true
	if lease.activeBorrows != 0 {
		return nil
	}
	return lease.closeLocked()
}

func (lease *Lease) closeLocked() error {
	lease.closed = true
	if err := closePlatformRoot(&lease.handle); err != nil {
		return ErrIO
	}
	return nil
}

func (lease *Lease) beginBorrow() (uint64, error) {
	if lease == nil {
		return 0, ErrClosed
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.closeRequested || lease.activeBorrows == math.MaxUint64 || lease.nextGeneration == math.MaxUint64 {
		return 0, ErrClosed
	}
	lease.nextGeneration++
	generation := lease.nextGeneration
	if lease.activeGenerations == nil {
		lease.activeGenerations = make(map[uint64]struct{})
	}
	lease.activeGenerations[generation] = struct{}{}
	lease.activeBorrows++
	return generation, nil
}

func (lease *Lease) endBorrow(generation uint64) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if _, active := lease.activeGenerations[generation]; !active || lease.activeBorrows == 0 {
		panic("rootfs: invalid borrow release")
	}
	delete(lease.activeGenerations, generation)
	lease.activeBorrows--
	if lease.closeRequested && lease.activeBorrows == 0 {
		return lease.closeLocked()
	}
	return nil
}

// WithBorrow grants continuation-only access and invalidates it on every return path.
func WithBorrow[T any](lease *Lease, callback func(Borrowed) T) (result T, resultErr error) {
	if callback == nil {
		return result, ErrInvalidCallback
	}
	generation, err := lease.beginBorrow()
	if err != nil {
		return result, err
	}
	view := &borrowedView{lease: lease, generation: generation, active: true}
	view.condition = sync.NewCond(&view.mu)
	defer func() {
		recovered := recover()
		view.invalidate()
		closeErr := lease.endBorrow(generation)
		if recovered != nil {
			panic(recovered)
		}
		if resultErr == nil {
			resultErr = closeErr
		}
	}()
	result = callback(view)
	return result, nil
}

func (view *borrowedView) OpenDir(path pathspec.Relative) (*Dir, error) {
	lease, generation, err := view.beginOperation()
	if err != nil {
		return nil, err
	}
	defer view.endOperation()
	return lease.openDir(path, generation)
}

func (view *borrowedView) OpenRegular(path pathspec.Relative) (*File, error) {
	lease, generation, err := view.beginOperation()
	if err != nil {
		return nil, err
	}
	defer view.endOperation()
	return lease.openRegular(path, generation)
}

func (view *borrowedView) beginOperation() (*Lease, uint64, error) {
	if view == nil {
		return nil, 0, ErrBorrowExpired
	}
	view.mu.Lock()
	defer view.mu.Unlock()
	if !view.active {
		return nil, 0, ErrBorrowExpired
	}
	view.operations++
	return view.lease, view.generation, nil
}

func (view *borrowedView) endOperation() {
	view.mu.Lock()
	view.operations--
	if view.operations == 0 {
		view.condition.Broadcast()
	}
	view.mu.Unlock()
}

func (view *borrowedView) invalidate() {
	view.mu.Lock()
	view.active = false
	for view.operations != 0 {
		view.condition.Wait()
	}
	view.lease = nil
	view.mu.Unlock()
}

// Kind reports the immutable kind captured from the verified target handle.
func (target *SearchTarget) Kind() SearchTargetKind {
	if target == nil {
		return 0
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	return target.kind
}

// TakeDir transfers a directory handle and consumes this wrapper.
func (target *SearchTarget) TakeDir() (*Dir, error) {
	if target == nil {
		return nil, ErrTargetConsumed
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.consumed {
		return nil, ErrTargetConsumed
	}
	if target.kind != SearchTargetDirectory {
		return nil, ErrWrongTargetKind
	}
	directory := target.directory
	if directory == nil {
		return nil, ErrTargetConsumed
	}
	target.directory = nil
	target.consumed = true
	return directory, nil
}

// TakeFile transfers a regular-file handle and consumes this wrapper.
func (target *SearchTarget) TakeFile() (*File, error) {
	if target == nil {
		return nil, ErrTargetConsumed
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.consumed {
		return nil, ErrTargetConsumed
	}
	if target.kind != SearchTargetRegular {
		return nil, ErrWrongTargetKind
	}
	file := target.file
	if file == nil {
		return nil, ErrTargetConsumed
	}
	target.file = nil
	target.consumed = true
	return file, nil
}

// Close releases an untaken target handle at most once.
func (target *SearchTarget) Close() error {
	if target == nil {
		return nil
	}
	target.mu.Lock()
	if target.consumed {
		target.mu.Unlock()
		return nil
	}
	directory := target.directory
	file := target.file
	target.directory = nil
	target.file = nil
	target.consumed = true
	target.mu.Unlock()
	if directory != nil {
		return directory.Close()
	}
	if file != nil {
		return file.Close()
	}
	return nil
}

func (directory *Dir) Identity() Identity {
	if directory == nil {
		return Identity{}
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	return directory.identity
}

func (directory *Dir) ResolvedPath() pathspec.Relative {
	if directory == nil {
		return pathspec.Relative{}
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	return directory.resolved
}

func (directory *Dir) Close() error {
	if directory == nil {
		return nil
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if directory.closed {
		return nil
	}
	directory.closed = true
	if err := closePlatformDir(&directory.handle); err != nil {
		return ErrIO
	}
	return nil
}

func (file *File) Identity() Identity {
	if file == nil {
		return Identity{}
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	return file.identity
}

func (file *File) ResolvedPath() pathspec.Relative {
	if file == nil {
		return pathspec.Relative{}
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	return file.resolved
}

func (file *File) ReadContext(ctx context.Context, destination []byte) (int, error) {
	if len(destination) < 1 || len(destination) > 4096 {
		return 0, ErrInvalidReadSize
	}
	if ctx == nil {
		return 0, ErrIO
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	if file == nil {
		return 0, ErrClosed
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return 0, ErrClosed
	}
	read, err := readPlatformFile(file.handle, destination)
	if err != nil {
		return read, ErrIO
	}
	if read == 0 {
		return 0, io.EOF
	}
	return read, nil
}

func (file *File) Close() error {
	if file == nil {
		return nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return nil
	}
	file.closed = true
	if err := closePlatformFile(&file.handle); err != nil {
		return ErrIO
	}
	return nil
}
