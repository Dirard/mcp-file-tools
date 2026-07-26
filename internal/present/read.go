package present

import (
	"errors"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/config"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

var errPresentationOverflow = errors.New("presentation exceeds byte budget")

const readCursorPlaceholder Cursor = "AAAAAAAAAAAAAAAAAAAAAA"

type readSegment struct {
	item  uint8
	start uint64
	end   uint64
}

type readPage struct {
	segments []readSegment
	rows     uint64
	items    uint64
	maxBytes uint64
	complete bool
}

type ReadPlan struct {
	snapshot  navmodel.ReadSnapshot
	pages     []readPage
	footprint uint64
}

type readMaterialItem struct {
	kind     navmodel.ReadItemKind
	index    uint32
	path     string
	language api.Language
	lines    []navmodel.ReadLine
	records  []navmodel.Record
	code     api.ErrorCode
	warnings []api.WarningCode
	units    uint64
}

type readMaterial struct {
	view    navmodel.ReadView
	items   []readMaterialItem
	success uint32
	failed  uint32
}

type readPosition struct {
	item   int
	offset uint64
}

func PlanRead(snapshot navmodel.ReadSnapshot, effectiveBytes uint64, maxPages uint64) (ReadPlan, api.ErrorCode) {
	if snapshot.Validate() != nil || effectiveBytes == 0 || maxPages == 0 {
		return ReadPlan{}, api.ErrorInvalidInput
	}
	if effectiveBytes > config.ReadOutputMaxBytes {
		effectiveBytes = config.ReadOutputMaxBytes
	}
	material, err := materializeReadSnapshot(snapshot)
	if err != nil {
		return ReadPlan{}, api.ErrorInvalidInput
	}

	position := readPosition{}
	pages := make([]readPage, 0, 1)
	for position.item < len(material.items) {
		remaining := remainingReadUnits(material, position)
		if remaining == 0 {
			return ReadPlan{}, api.ErrorInvalidInput
		}

		allSegments, end, ok := readSegments(material, position, remaining)
		if !ok || end.item != len(material.items) {
			return ReadPlan{}, api.ErrorInvalidInput
		}
		finalPage := newReadPage(material, allSegments, effectiveBytes, true)
		fits, renderErr := readPageFits(material, finalPage, "")
		if renderErr != nil {
			return ReadPlan{}, api.ErrorInvalidInput
		}
		if fits {
			pages = append(pages, finalPage)
			position = end
			break
		}

		oneSegment, _, ok := readSegments(material, position, 1)
		if !ok {
			return ReadPlan{}, api.ErrorInvalidInput
		}
		onePage := newReadPage(material, oneSegment, effectiveBytes, false)
		oneFits, renderErr := readPageFits(material, onePage, readCursorPlaceholder)
		if renderErr != nil {
			return ReadPlan{}, api.ErrorInvalidInput
		}
		if !oneFits || remaining == 1 {
			return ReadPlan{}, api.ErrorRecordExceedsBudget
		}
		if uint64(len(pages))+1 >= maxPages {
			return ReadPlan{}, api.ErrorBudgetExceeded
		}

		low, high := uint64(1), remaining-1
		for low < high {
			middle := low + (high-low+1)/2
			segments, _, ok := readSegments(material, position, middle)
			if !ok {
				return ReadPlan{}, api.ErrorInvalidInput
			}
			candidate := newReadPage(material, segments, effectiveBytes, false)
			candidateFits, renderErr := readPageFits(material, candidate, readCursorPlaceholder)
			if renderErr != nil {
				return ReadPlan{}, api.ErrorInvalidInput
			}
			if candidateFits {
				low = middle
			} else {
				high = middle - 1
			}
		}

		segments, next, ok := readSegments(material, position, low)
		if !ok || next == position {
			return ReadPlan{}, api.ErrorInvalidInput
		}
		pages = append(pages, newReadPage(material, segments, effectiveBytes, false))
		position = next
	}

	if len(pages) == 0 || uint64(len(pages)) > maxPages {
		return ReadPlan{}, api.ErrorBudgetExceeded
	}
	ownedPages := cloneReadPages(pages)
	plan := ReadPlan{snapshot: snapshot, pages: ownedPages}
	plan.footprint = readPlanFootprint(ownedPages)
	return plan, ""
}

// Render uses a zero-based page index. Partial pages require the caller's
// canonical cursor; the terminal page requires an empty cursor.
func (plan ReadPlan) Render(page uint64, cursor Cursor) (Page, error) {
	if plan.footprint == 0 || page >= uint64(len(plan.pages)) {
		return Page{}, errInvalidPresentation
	}
	selected := plan.pages[page]
	status := Partial
	if selected.complete {
		status = Complete
	}
	if !validStatusCursor(status, cursor) {
		return Page{}, errInvalidPresentation
	}
	material, err := materializeReadSnapshot(plan.snapshot)
	if err != nil {
		return Page{}, errInvalidPresentation
	}
	text, err := renderReadMaterial(material, selected, cursor)
	if err != nil {
		return Page{}, errInvalidPresentation
	}
	isError := selected.complete && material.success == 0 && material.failed != 0
	result := api.Navigation(string(text), isError)
	if result.Validate() != nil {
		return Page{}, errInvalidPresentation
	}
	return Page{
		Result:   result,
		Rows:     selected.rows,
		Items:    selected.items,
		Complete: selected.complete,
	}, nil
}

func (plan ReadPlan) Footprint() uint64 {
	return plan.footprint
}

func (plan ReadPlan) PageCount() uint64 {
	return uint64(len(plan.pages))
}

func materializeReadSnapshot(snapshot navmodel.ReadSnapshot) (readMaterial, error) {
	items := snapshot.Items()
	material := readMaterial{
		view:    snapshot.View(),
		items:   make([]readMaterialItem, len(items)),
		success: snapshot.Success(),
		failed:  snapshot.Failed(),
	}
	for index, item := range items {
		entry := readMaterialItem{
			kind:     item.Kind(),
			index:    item.Index(),
			warnings: item.Warnings(),
		}
		switch item.Kind() {
		case navmodel.ReadItemSourceRows:
			entry.path, _ = item.Path()
			entry.lines, _ = item.Lines()
			entry.units = uint64(len(entry.lines))
		case navmodel.ReadItemOutlineRecords:
			entry.path, _ = item.Path()
			entry.language, _ = item.Language()
			entry.records, _ = item.Records()
			entry.units = uint64(len(entry.records))
		case navmodel.ReadItemEmpty:
			entry.path, _ = item.Path()
			if item.View() == navmodel.ReadOutline {
				entry.language, _ = item.Language()
			}
			entry.units = 1
		case navmodel.ReadItemFailure:
			entry.code, _ = item.ErrorCode()
			entry.units = 1
		default:
			return readMaterial{}, errInvalidPresentation
		}
		if entry.units == 0 || entry.index != uint32(index) {
			return readMaterial{}, errInvalidPresentation
		}
		material.items[index] = entry
	}
	return material, nil
}

func remainingReadUnits(material readMaterial, position readPosition) uint64 {
	var remaining uint64
	for index := position.item; index < len(material.items); index++ {
		units := material.items[index].units
		if index == position.item {
			units -= position.offset
		}
		remaining += units
	}
	return remaining
}

func readSegments(material readMaterial, start readPosition, count uint64) ([]readSegment, readPosition, bool) {
	if count == 0 || start.item < 0 || start.item >= len(material.items) || start.offset >= material.items[start.item].units {
		return nil, readPosition{}, false
	}
	position := start
	segments := make([]readSegment, 0, len(material.items)-start.item)
	for count > 0 && position.item < len(material.items) {
		item := material.items[position.item]
		available := item.units - position.offset
		take := available
		if take > count {
			take = count
		}
		segments = append(segments, readSegment{item: uint8(position.item), start: position.offset, end: position.offset + take})
		position.offset += take
		count -= take
		if position.offset == item.units {
			position.item++
			position.offset = 0
		}
	}
	if count != 0 {
		return nil, readPosition{}, false
	}
	owned := make([]readSegment, len(segments))
	copy(owned, segments)
	return owned, position, true
}

func newReadPage(material readMaterial, segments []readSegment, maxBytes uint64, complete bool) readPage {
	page := readPage{
		segments: segments,
		items:    uint64(len(segments)),
		maxBytes: maxBytes,
		complete: complete,
	}
	for _, segment := range segments {
		kind := material.items[segment.item].kind
		if kind == navmodel.ReadItemSourceRows || kind == navmodel.ReadItemOutlineRecords {
			page.rows += segment.end - segment.start
		}
	}
	return page
}

func readPageFits(material readMaterial, page readPage, cursor Cursor) (bool, error) {
	_, err := renderReadMaterial(material, page, cursor)
	if errors.Is(err, errPresentationOverflow) {
		return false, nil
	}
	return err == nil, err
}

func renderReadMaterial(material readMaterial, page readPage, cursor Cursor) ([]byte, error) {
	status := Partial
	if page.complete {
		status = Complete
	}
	if len(page.segments) == 0 || page.items != uint64(len(page.segments)) || page.maxBytes == 0 ||
		!validStatusCursor(status, cursor) {
		return nil, errInvalidPresentation
	}

	buffer := newOutputBuffer(page.maxBytes)
	buffer.appendString("@@read\t")
	if material.view == navmodel.ReadSource {
		buffer.appendString("source")
	} else if material.view == navmodel.ReadOutline {
		buffer.appendString("outline")
	} else {
		return nil, errInvalidPresentation
	}
	buffer.appendByte('\t')
	buffer.appendString(statusName(status))
	buffer.appendString("\titems=")
	buffer.appendUint(page.items)
	buffer.appendString(cursorField(status, cursor))
	buffer.appendByte('\n')

	var rows uint64
	for _, segment := range page.segments {
		if int(segment.item) >= len(material.items) {
			return nil, errInvalidPresentation
		}
		item := material.items[segment.item]
		if segment.start >= segment.end || segment.end > item.units {
			return nil, errInvalidPresentation
		}
		if err := appendReadSegment(buffer, item, segment); err != nil {
			return nil, err
		}
		if item.kind == navmodel.ReadItemSourceRows || item.kind == navmodel.ReadItemOutlineRecords {
			rows += segment.end - segment.start
		}
	}
	if rows != page.rows {
		return nil, errInvalidPresentation
	}
	return buffer.finish()
}

func appendReadSegment(buffer *outputBuffer, item readMaterialItem, segment readSegment) error {
	blockComplete := segment.end == item.units
	buffer.appendString("@\t")
	switch item.kind {
	case navmodel.ReadItemSourceRows:
		if err := buffer.quote(item.path); err != nil {
			return err
		}
		appendItemField(buffer, item.index)
		first := item.lines[segment.start]
		last := item.lines[segment.end-1]
		buffer.appendByte('\t')
		buffer.appendUint(uint64(first.Number()))
		buffer.appendByte(':')
		buffer.appendUint(uint64(last.Number()))
		appendBlockStatus(buffer, blockComplete)
		buffer.appendByte('\n')
		for position := segment.start; position < segment.end; position++ {
			line := item.lines[position]
			buffer.appendUint(uint64(line.Number()))
			buffer.appendByte('|')
			buffer.appendString(line.Text())
			buffer.appendByte('\n')
			if position == 0 {
				appendItemWarnings(buffer, item.index, item.warnings)
			}
		}
	case navmodel.ReadItemOutlineRecords:
		if err := buffer.quote(item.path); err != nil {
			return err
		}
		appendItemField(buffer, item.index)
		buffer.appendByte('\t')
		buffer.appendString(string(item.language))
		appendBlockStatus(buffer, blockComplete)
		buffer.appendByte('\n')
		for position := segment.start; position < segment.end; position++ {
			if err := appendOutlineRecord(buffer, item.records[position]); err != nil {
				return err
			}
			if position == 0 {
				appendItemWarnings(buffer, item.index, item.warnings)
			}
		}
	case navmodel.ReadItemEmpty:
		if err := buffer.quote(item.path); err != nil {
			return err
		}
		appendItemField(buffer, item.index)
		if item.language == "" {
			buffer.appendString("\tempty\tcomplete\n")
		} else {
			buffer.appendByte('\t')
			buffer.appendString(string(item.language))
			buffer.appendString("\tcomplete\n")
		}
		appendItemWarnings(buffer, item.index, item.warnings)
	case navmodel.ReadItemFailure:
		buffer.appendString("\"<path-hidden>\"")
		appendItemField(buffer, item.index)
		buffer.appendString("\terror\t")
		buffer.appendString(string(item.code))
		buffer.appendByte('\n')
		appendItemWarnings(buffer, item.index, item.warnings)
	default:
		return errInvalidPresentation
	}
	return nil
}

func appendOutlineRecord(buffer *outputBuffer, record navmodel.Record) error {
	switch record.Type {
	case navmodel.Import:
		buffer.appendString("I\t")
	case navmodel.Heading:
		buffer.appendString("H\t")
		buffer.appendUint(uint64(record.Depth))
		buffer.appendByte('\t')
	case navmodel.Symbol:
		buffer.appendString("S\t")
		buffer.appendUint(uint64(record.Depth))
		buffer.appendByte('\t')
	default:
		return errInvalidPresentation
	}
	buffer.appendUint(uint64(record.Range.Start))
	buffer.appendByte(':')
	buffer.appendUint(uint64(record.Range.End))
	if record.Type == navmodel.Symbol {
		buffer.appendByte('\t')
		buffer.appendString(string(record.Kind))
	}
	buffer.appendByte('\t')
	if err := buffer.quote(record.Name); err != nil {
		return err
	}
	buffer.appendByte('\n')
	return nil
}

func appendItemField(buffer *outputBuffer, index uint32) {
	buffer.appendString("\titem=")
	buffer.appendUint(uint64(index))
}

func appendBlockStatus(buffer *outputBuffer, complete bool) {
	if complete {
		buffer.appendString("\tcomplete")
		return
	}
	buffer.appendString("\tpartial")
}

func appendItemWarnings(buffer *outputBuffer, index uint32, warnings []api.WarningCode) {
	for _, warning := range warnings {
		buffer.appendString("!\t")
		buffer.appendString(string(warning))
		buffer.appendString("\titem=")
		buffer.appendUint(uint64(index))
		buffer.appendByte('\n')
	}
}

func cloneReadPages(pages []readPage) []readPage {
	owned := make([]readPage, len(pages))
	for index, page := range pages {
		segments := make([]readSegment, len(page.segments))
		copy(segments, page.segments)
		page.segments = segments
		owned[index] = page
	}
	return owned
}

func readPlanFootprint(pages []readPage) uint64 {
	bytes := uint64(cap(pages)) * uint64(unsafe.Sizeof(readPage{}))
	for _, page := range pages {
		bytes += uint64(cap(page.segments)) * uint64(unsafe.Sizeof(readSegment{}))
	}
	return bytes
}
