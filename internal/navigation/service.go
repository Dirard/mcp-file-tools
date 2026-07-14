package navigation

import (
	"context"
	"errors"
	"time"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/codeparse"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/cwd"
	"github.com/Dirard/mcp-file-tools/internal/present"
	"github.com/Dirard/mcp-file-tools/internal/rootfs"
	runtimepkg "github.com/Dirard/mcp-file-tools/internal/runtime"
	"github.com/Dirard/mcp-file-tools/internal/scanner"
)

const summaryReservationBytes uint64 = 128 << 10

type Service struct {
	Config      config.Runtime
	CWD         *cwd.Registry
	Parser      *codeparse.Service
	ScanLimiter *runtimepkg.SubLimiter
	Scanner     *scanner.Service
}

type Connection struct {
	Service *Service
	Cursors *cursor.Registry
}

func (connection *Connection) valid() bool {
	return connection != nil && connection.Service != nil && connection.Service.CWD != nil &&
		connection.Service.Scanner != nil && connection.Service.ScanLimiter != nil && connection.Cursors != nil
}

func (service *Service) scanLimits() scanner.Limits {
	return scanner.Limits{
		MaxFiles:         service.Config.ScanMaxFiles,
		MaxDirs:          service.Config.ScanMaxDirs,
		MaxBytes:         service.Config.ScanMaxBytes,
		MaxParserBytes:   service.Config.ParseMaxBytes,
		FrontierMaxBytes: service.Config.ScanFrontierMaxBytes,
		IgnoreDirsAdd:    append([]string(nil), service.Config.IgnoreDirsAdd...),
	}
}

func (service *Service) scanDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(service.Config.ScanTimeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	return deadline
}

func ordinary(work *runtimepkg.WorkLease, result api.Result) runtimepkg.Execution {
	if work != nil {
		work.WorkerReturned()
	}
	return runtimepkg.Execution{Kind: runtimepkg.ExecutionOrdinary, Result: result}
}

func ordinaryOwnedElsewhere(result api.Result) runtimepkg.Execution {
	return runtimepkg.Execution{Kind: runtimepkg.ExecutionOrdinary, Result: result}
}

func errorExecution(work *runtimepkg.WorkLease, code api.ErrorCode) runtimepkg.Execution {
	return ordinary(work, present.Error(code))
}

func rootfsErrorCode(err error) api.ErrorCode {
	switch {
	case errors.Is(err, rootfs.ErrNotFound):
		return api.ErrorNotFound
	case errors.Is(err, rootfs.ErrPermissionDenied):
		return api.ErrorPermissionDenied
	case errors.Is(err, rootfs.ErrNotDirectory),
		errors.Is(err, rootfs.ErrNotRegular),
		errors.Is(err, rootfs.ErrSpecial),
		errors.Is(err, rootfs.ErrSymlink),
		errors.Is(err, rootfs.ErrMountBoundary),
		errors.Is(err, rootfs.ErrWrongTargetKind),
		errors.Is(err, rootfs.ErrTargetConsumed),
		errors.Is(err, rootfs.ErrInvalidTarget):
		return api.ErrorInvalidInput
	default:
		return api.ErrorIOError
	}
}

func projectConsumer(_ context.Context, candidate scanner.Candidate, _ *rootfs.File) scanner.ConsumeResult {
	kind := scanner.RowFile
	if candidate.Kind == rootfs.EntryDir {
		kind = scanner.RowDirectory
	}
	return scanner.ConsumeResult{Rows: []scanner.Row{{Kind: kind, Path: candidate.Path.String()}}}
}

func closeLease(lease *rootfs.Lease) api.ErrorCode {
	if lease == nil {
		return ""
	}
	if err := lease.Close(); err != nil {
		return api.ErrorIOError
	}
	return ""
}
