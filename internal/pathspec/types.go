package pathspec

import "unsafe"

const maxPathBytes = 4096

// TargetOS selects the lexical path contract independently of the host OS.
type TargetOS uint8

const (
	POSIX TargetOS = iota + 1
	Windows
)

// Relative is one validated, slash-normalized path relative to a registered root.
type Relative struct {
	target     TargetOS
	normalized string
	components []string
}

// Target reports the lexical contract used to validate the path.
func (path Relative) Target() TargetOS {
	return path.target
}

// String returns the validated slash-normalized spelling.
func (path Relative) String() string {
	return path.normalized
}

// Components returns a defensive copy of the validated path components.
func (path Relative) Components() []string {
	return append([]string(nil), path.components...)
}

// ByteLen reports the UTF-8 byte length of String.
func (path Relative) ByteLen() int {
	return len(path.normalized)
}

// RetainedBytes reports storage owned by the normalized spelling and the
// component index. Component strings are views into normalized.
func (path Relative) RetainedBytes() uint64 {
	return uint64(len(path.normalized)) + uint64(cap(path.components))*uint64(unsafe.Sizeof(""))
}

// RootDirectory is one validated absolute directory spelling for a target OS.
type RootDirectory struct {
	target     TargetOS
	normalized string
	components []string
}

// Target reports the lexical contract used to validate the directory.
func (directory RootDirectory) Target() TargetOS {
	return directory.target
}

// String returns the validated slash-normalized directory spelling.
func (directory RootDirectory) String() string {
	return directory.normalized
}

// Components returns a defensive copy of the directory components below its volume root.
func (directory RootDirectory) Components() []string {
	return append([]string(nil), directory.components...)
}

// ByteLen reports the UTF-8 byte length of String.
func (directory RootDirectory) ByteLen() int {
	return len(directory.normalized)
}
