package navigation

import (
	"crypto/sha256"
	"math"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/cursor"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
	"github.com/Dirard/mcp-file-tools/internal/present"
)

type readShared struct {
	snapshot  navmodel.ReadSnapshot
	plan      present.ReadPlan
	digest    [32]byte
	footprint uint64
}

type readState struct {
	shared *readShared
	cwdID  uint64
	page   uint64
}

var (
	_ cursor.SharedAllocation = (*readShared)(nil)
	_ cursor.State            = (*readState)(nil)
)

func newReadShared(snapshot navmodel.ReadSnapshot, plan present.ReadPlan) (*readShared, error) {
	if snapshot.Validate() != nil || plan.PageCount() < 2 || plan.Footprint() == 0 {
		return nil, errNavigationPresentation
	}
	shared := &readShared{snapshot: snapshot, plan: plan}
	footprint, ok := addReadBytes(uint64(unsafe.Sizeof(readShared{})), snapshot.Footprint())
	if !ok {
		return nil, errNavigationPresentation
	}
	footprint, ok = addReadBytes(footprint, plan.Footprint())
	if !ok {
		return nil, errNavigationPresentation
	}
	shared.footprint = footprint

	digest := sha256.New()
	writeDynamicString(digest, "read-shared-v1")
	writeDynamicUint64(digest, plan.PageCount())
	writeDynamicUint64(digest, snapshot.Footprint())
	writeDynamicUint64(digest, plan.Footprint())
	for pageIndex := uint64(0); pageIndex < plan.PageCount(); pageIndex++ {
		pageCursor := present.Cursor("")
		if pageIndex+1 < plan.PageCount() {
			pageCursor = present.Cursor((cursor.Token{}).String())
		}
		page, err := plan.Render(pageIndex, pageCursor)
		if err != nil || page.Complete != (pageIndex+1 == plan.PageCount()) {
			return nil, errNavigationPresentation
		}
		text, ok := page.Result.Text()
		if !ok {
			return nil, errNavigationPresentation
		}
		writeDynamicString(digest, text)
		if page.Result.IsError() {
			digest.Write([]byte{1})
		} else {
			digest.Write([]byte{0})
		}
	}
	copy(shared.digest[:], digest.Sum(nil))
	return shared, nil
}

func (shared *readShared) Digest() [32]byte {
	if shared == nil {
		return sha256.Sum256(nil)
	}
	return shared.digest
}

func (shared *readShared) Footprint() uint64 {
	if shared == nil {
		return 0
	}
	return shared.footprint
}

func newReadState(shared *readShared, cwdID, page uint64) *readState {
	state := &readState{shared: shared, cwdID: cwdID, page: page}
	if !state.valid() {
		return nil
	}
	return state
}

func (state *readState) Tool() api.ToolName {
	if state == nil {
		return ""
	}
	return api.ToolRead
}

func (state *readState) CWDID() uint64 {
	if state == nil {
		return 0
	}
	return state.cwdID
}

func (state *readState) SharedDigest() ([32]byte, bool) {
	if state == nil || state.shared == nil {
		return [32]byte{}, false
	}
	return state.shared.Digest(), true
}

func (state *readState) Footprint() uint64 {
	if state == nil {
		return 0
	}
	return uint64(unsafe.Sizeof(readState{}))
}

func (state *readState) Digest() [32]byte {
	if state == nil || state.shared == nil {
		return sha256.Sum256(nil)
	}
	digest := sha256.New()
	writeDynamicString(digest, "read-state-v1")
	sharedDigest := state.shared.Digest()
	digest.Write(sharedDigest[:])
	writeDynamicUint64(digest, state.cwdID)
	writeDynamicUint64(digest, state.page)
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func (state *readState) CloneForCompute() cursor.State {
	if !state.valid() {
		return nil
	}
	clone := *state
	return &clone
}

func (state *readState) valid() bool {
	return state != nil && state.shared != nil && state.cwdID != 0 && state.cwdID <= maxSafeCWDID &&
		state.shared.footprint != 0 && state.shared.plan.PageCount() >= 2 &&
		state.page > 0 && state.page < state.shared.plan.PageCount()
}

func addReadBytes(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}
