package rootfs

import "github.com/Dirard/mcp-file-tools/internal/pathspec"

// These assertions intentionally describe the complete mode-free open seam.
var (
	_ interface {
		OpenRegular(pathspec.Relative) (*File, error)
	} = (*Lease)(nil)
	_ interface {
		OpenSearchTarget(pathspec.Relative) (*SearchTarget, error)
	} = (*Lease)(nil)
)
