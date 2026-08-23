package navmodel

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

const (
	maxReadLineNumber = uint32(1<<31 - 1)
	maxReadItems      = 24
)

var errInvalidReadModel = errors.New("invalid read presentation model")

type ReadView uint8

const (
	ReadSource ReadView = iota + 1
	ReadOutline
)

func (view ReadView) Valid() bool {
	return view == ReadSource || view == ReadOutline
}

type ReadItemKind uint8

const (
	ReadItemSourceRows ReadItemKind = iota + 1
	ReadItemOutlineRecords
	ReadItemEmpty
	ReadItemFailure
)

type ReadLine struct {
	number uint32
	text   string
}

func NewReadLine(number uint32, text string) (ReadLine, error) {
	return NewOwnedReadLine(number, strings.Clone(text))
}

// NewOwnedReadLine accepts an exact, independently owned string without
// copying it again.
func NewOwnedReadLine(number uint32, text string) (ReadLine, error) {
	line := ReadLine{number: number, text: text}
	if err := line.Validate(); err != nil {
		return ReadLine{}, err
	}
	return line, nil
}

func (line ReadLine) Number() uint32 {
	return line.number
}

func (line ReadLine) Text() string {
	return line.text
}

func (line ReadLine) Validate() error {
	if line.number == 0 || line.number > maxReadLineNumber ||
		!utf8.ValidString(line.text) ||
		strings.IndexByte(line.text, '\r') >= 0 ||
		strings.IndexByte(line.text, '\n') >= 0 {
		return errInvalidReadModel
	}
	return nil
}

type ReadItem struct {
	kind      ReadItemKind
	view      ReadView
	index     uint32
	path      string
	language  api.Language
	lines     []ReadLine
	records   []Record
	code      api.ErrorCode
	warnings  []api.WarningCode
	footprint uint64
}

func NewReadSourceItem(index uint32, path string, lines []ReadLine, warnings []api.WarningCode) (ReadItem, error) {
	ownedLines, err := cloneReadLines(lines)
	if err != nil {
		return ReadItem{}, err
	}
	return NewOwnedReadSourceItem(index, path, ownedLines, warnings)
}

// NewOwnedReadSourceItem accepts a compact line slice whose ownership is
// transferred to the immutable item.
func NewOwnedReadSourceItem(index uint32, path string, lines []ReadLine, warnings []api.WarningCode) (ReadItem, error) {
	if index >= maxReadItems || !validReadPath(path) || len(lines) == 0 || validateReadLines(lines) != nil {
		return ReadItem{}, errInvalidReadModel
	}
	ownedWarnings, err := cloneReadWarnings(warnings)
	if err != nil {
		return ReadItem{}, err
	}
	item := ReadItem{
		kind:     ReadItemSourceRows,
		view:     ReadSource,
		index:    index,
		path:     strings.Clone(path),
		lines:    lines,
		warnings: ownedWarnings,
	}
	item.footprint = readItemFootprint(item)
	return item, nil
}

// ReadSourceItemFootprint preflights an exact compact source item without
// allocating its line slice or cloning its text.
func ReadSourceItemFootprint(path string, lineCount int, textBytes uint64) (uint64, bool) {
	if !validReadPath(path) || lineCount <= 0 {
		return 0, false
	}
	maximum := ^uint64(0)
	bytes := uint64(unsafe.Sizeof(ReadItem{})) + uint64(len(path))
	lineSize := uint64(unsafe.Sizeof(ReadLine{}))
	count := uint64(lineCount)
	if count > (maximum-bytes)/lineSize {
		return 0, false
	}
	bytes += count * lineSize
	if textBytes > maximum-bytes {
		return 0, false
	}
	return bytes + textBytes, true
}

func NewReadOutlineItem(index uint32, path string, language api.Language, records []Record, warnings []api.WarningCode) (ReadItem, error) {
	if index >= maxReadItems || !validReadPath(path) || !language.Valid() || len(records) == 0 {
		return ReadItem{}, errInvalidReadModel
	}
	ownedRecords, ok := CloneRecords(records)
	if !ok || !validOrderedReadRecords(ownedRecords) {
		return ReadItem{}, errInvalidReadModel
	}
	ownedWarnings, err := cloneReadWarnings(warnings)
	if err != nil {
		return ReadItem{}, err
	}
	item := ReadItem{
		kind:     ReadItemOutlineRecords,
		view:     ReadOutline,
		index:    index,
		path:     strings.Clone(path),
		language: api.Language(strings.Clone(string(language))),
		records:  ownedRecords,
		warnings: ownedWarnings,
	}
	item.footprint = readItemFootprint(item)
	return item, nil
}

func NewReadSourceEmptyItem(index uint32, path string, warnings []api.WarningCode) (ReadItem, error) {
	if index >= maxReadItems || !validReadPath(path) {
		return ReadItem{}, errInvalidReadModel
	}
	ownedWarnings, err := cloneReadWarnings(warnings)
	if err != nil {
		return ReadItem{}, err
	}
	item := ReadItem{
		kind:     ReadItemEmpty,
		view:     ReadSource,
		index:    index,
		path:     strings.Clone(path),
		warnings: ownedWarnings,
	}
	item.footprint = readItemFootprint(item)
	return item, nil
}

func NewReadOutlineEmptyItem(index uint32, path string, language api.Language, warnings []api.WarningCode) (ReadItem, error) {
	if index >= maxReadItems || !validReadPath(path) || !language.Valid() {
		return ReadItem{}, errInvalidReadModel
	}
	ownedWarnings, err := cloneReadWarnings(warnings)
	if err != nil {
		return ReadItem{}, err
	}
	item := ReadItem{
		kind:     ReadItemEmpty,
		view:     ReadOutline,
		index:    index,
		path:     strings.Clone(path),
		language: api.Language(strings.Clone(string(language))),
		warnings: ownedWarnings,
	}
	item.footprint = readItemFootprint(item)
	return item, nil
}

func NewReadErrorItem(view ReadView, index uint32, code api.ErrorCode, warnings []api.WarningCode) (ReadItem, error) {
	if !view.Valid() || index >= maxReadItems || !readItemErrorCode(code) {
		return ReadItem{}, errInvalidReadModel
	}
	ownedWarnings, err := cloneReadWarnings(warnings)
	if err != nil {
		return ReadItem{}, err
	}
	item := ReadItem{
		kind:     ReadItemFailure,
		view:     view,
		index:    index,
		code:     api.ErrorCode(strings.Clone(string(code))),
		warnings: ownedWarnings,
	}
	item.footprint = readItemFootprint(item)
	return item, nil
}

func (item ReadItem) Kind() ReadItemKind {
	return item.kind
}

func (item ReadItem) View() ReadView {
	return item.view
}

func (item ReadItem) Index() uint32 {
	return item.index
}

func (item ReadItem) Path() (string, bool) {
	if !item.Success() || item.path == "" {
		return "", false
	}
	return item.path, true
}

func (item ReadItem) Language() (api.Language, bool) {
	if !item.Success() || item.view != ReadOutline || !item.language.Valid() {
		return "", false
	}
	return item.language, true
}

func (item ReadItem) Lines() ([]ReadLine, bool) {
	if item.kind != ReadItemSourceRows {
		return nil, false
	}
	lines := make([]ReadLine, len(item.lines))
	copy(lines, item.lines)
	return lines, true
}

func (item ReadItem) Records() ([]Record, bool) {
	if item.kind != ReadItemOutlineRecords {
		return nil, false
	}
	records, ok := CloneRecords(item.records)
	return records, ok
}

func (item ReadItem) ErrorCode() (api.ErrorCode, bool) {
	if item.kind != ReadItemFailure || !readItemErrorCode(item.code) {
		return "", false
	}
	return item.code, true
}

func (item ReadItem) Warnings() []api.WarningCode {
	warnings := make([]api.WarningCode, len(item.warnings))
	copy(warnings, item.warnings)
	return warnings
}

func (item ReadItem) Success() bool {
	return item.kind == ReadItemSourceRows || item.kind == ReadItemOutlineRecords || item.kind == ReadItemEmpty
}

func (item ReadItem) Footprint() uint64 {
	return item.footprint
}

func (item ReadItem) Validate() error {
	if !item.view.Valid() || item.index >= maxReadItems || validateReadWarnings(item.warnings) != nil {
		return errInvalidReadModel
	}

	switch item.kind {
	case ReadItemSourceRows:
		if item.view != ReadSource || !validReadPath(item.path) || item.language != "" ||
			len(item.lines) == 0 || len(item.records) != 0 || item.code != "" ||
			validateReadLines(item.lines) != nil {
			return errInvalidReadModel
		}
	case ReadItemOutlineRecords:
		if item.view != ReadOutline || !validReadPath(item.path) || !item.language.Valid() ||
			len(item.lines) != 0 || len(item.records) == 0 || item.code != "" ||
			!validOrderedReadRecords(item.records) {
			return errInvalidReadModel
		}
	case ReadItemEmpty:
		if !validReadPath(item.path) || len(item.lines) != 0 || len(item.records) != 0 || item.code != "" {
			return errInvalidReadModel
		}
		if (item.view == ReadSource && item.language != "") || (item.view == ReadOutline && !item.language.Valid()) {
			return errInvalidReadModel
		}
	case ReadItemFailure:
		if item.path != "" || item.language != "" || len(item.lines) != 0 || len(item.records) != 0 || !readItemErrorCode(item.code) {
			return errInvalidReadModel
		}
	default:
		return errInvalidReadModel
	}

	if item.footprint == 0 || item.footprint != readItemFootprint(item) {
		return errInvalidReadModel
	}
	return nil
}

type readSnapshotData struct {
	View      ReadView
	items     []ReadItem
	success   uint32
	failed    uint32
	footprint uint64
}

type ReadSnapshot struct {
	data *readSnapshotData
}

func NewReadSnapshot(view ReadView, items []ReadItem) (ReadSnapshot, error) {
	if !view.Valid() || len(items) == 0 || len(items) > maxReadItems {
		return ReadSnapshot{}, errInvalidReadModel
	}
	ownedItems := make([]ReadItem, len(items))
	var success uint32
	var failed uint32
	for index, item := range items {
		if item.Validate() != nil || item.View() != view || item.Index() != uint32(index) {
			return ReadSnapshot{}, errInvalidReadModel
		}
		ownedItems[index] = item
		if item.Success() {
			success++
		} else {
			failed++
		}
	}
	data := &readSnapshotData{View: view, items: ownedItems, success: success, failed: failed}
	data.footprint = readSnapshotFootprint(data)
	return ReadSnapshot{data: data}, nil
}

func (snapshot ReadSnapshot) View() ReadView {
	if snapshot.data == nil {
		return 0
	}
	return snapshot.data.View
}

func (snapshot ReadSnapshot) Items() []ReadItem {
	if snapshot.data == nil {
		return nil
	}
	items := make([]ReadItem, len(snapshot.data.items))
	copy(items, snapshot.data.items)
	return items
}

func (snapshot ReadSnapshot) Success() uint32 {
	if snapshot.data == nil {
		return 0
	}
	return snapshot.data.success
}

func (snapshot ReadSnapshot) Failed() uint32 {
	if snapshot.data == nil {
		return 0
	}
	return snapshot.data.failed
}

func (snapshot ReadSnapshot) Footprint() uint64 {
	if snapshot.data == nil {
		return 0
	}
	return snapshot.data.footprint
}

func (snapshot ReadSnapshot) Validate() error {
	if snapshot.data == nil || !snapshot.data.View.Valid() || len(snapshot.data.items) == 0 || len(snapshot.data.items) > maxReadItems {
		return errInvalidReadModel
	}
	var success uint32
	var failed uint32
	for index, item := range snapshot.data.items {
		if item.Validate() != nil || item.View() != snapshot.data.View || item.Index() != uint32(index) {
			return errInvalidReadModel
		}
		if item.Success() {
			success++
		} else {
			failed++
		}
	}
	if success != snapshot.data.success || failed != snapshot.data.failed || success+failed != uint32(len(snapshot.data.items)) ||
		snapshot.data.footprint == 0 || snapshot.data.footprint != readSnapshotFootprint(snapshot.data) {
		return errInvalidReadModel
	}
	return nil
}

func cloneReadLines(lines []ReadLine) ([]ReadLine, error) {
	owned := make([]ReadLine, len(lines))
	for index, line := range lines {
		copy, err := NewReadLine(line.number, line.text)
		if err != nil {
			return nil, err
		}
		owned[index] = copy
	}
	if err := validateReadLines(owned); err != nil {
		return nil, err
	}
	return owned, nil
}

func validateReadLines(lines []ReadLine) error {
	if len(lines) == 0 || cap(lines) != len(lines) {
		return errInvalidReadModel
	}
	for index, line := range lines {
		if line.Validate() != nil || (index > 0 && line.number != lines[index-1].number+1) {
			return errInvalidReadModel
		}
	}
	return nil
}

func cloneReadWarnings(warnings []api.WarningCode) ([]api.WarningCode, error) {
	if len(warnings) == 0 {
		return nil, nil
	}
	owned := make([]api.WarningCode, len(warnings))
	for index, warning := range warnings {
		if !warning.Valid() {
			return nil, errInvalidReadModel
		}
		owned[index] = api.WarningCode(strings.Clone(string(warning)))
	}
	sort.Slice(owned, func(left, right int) bool { return string(owned[left]) < string(owned[right]) })
	if err := validateReadWarnings(owned); err != nil {
		return nil, err
	}
	return owned, nil
}

func validateReadWarnings(warnings []api.WarningCode) error {
	if cap(warnings) != len(warnings) {
		return errInvalidReadModel
	}
	for index, warning := range warnings {
		if !warning.Valid() || (index > 0 && string(warnings[index-1]) >= string(warning)) {
			return errInvalidReadModel
		}
	}
	return nil
}

func validOrderedReadRecords(records []Record) bool {
	if len(records) == 0 || cap(records) != len(records) {
		return false
	}
	for index, record := range records {
		if !record.Valid() || !utf8.ValidString(record.Name) {
			return false
		}
		if index > 0 && compareOutlineRecords(records[index-1], record) >= 0 {
			return false
		}
	}
	return true
}

func compareOutlineRecords(left, right Record) int {
	if left.Range.Start != right.Range.Start {
		if left.Range.Start < right.Range.Start {
			return -1
		}
		return 1
	}
	if left.Range.End != right.Range.End {
		if left.Range.End < right.Range.End {
			return -1
		}
		return 1
	}
	if left.Type != right.Type {
		if left.Type < right.Type {
			return -1
		}
		return 1
	}
	if left.Depth != right.Depth {
		if left.Depth < right.Depth {
			return -1
		}
		return 1
	}
	return strings.Compare(left.Name, right.Name)
}

func validReadPath(path string) bool {
	return path != "" && len(path) <= api.InputStringMaxBytes && utf8.ValidString(path)
}

func readItemErrorCode(code api.ErrorCode) bool {
	switch code {
	case api.ErrorInvalidInput,
		api.ErrorNotFound,
		api.ErrorBinary,
		api.ErrorUnsupportedEncoding,
		api.ErrorUnsupportedLanguage,
		api.ErrorBudgetExceeded,
		api.ErrorPermissionDenied,
		api.ErrorIOError,
		api.ErrorParserFailed:
		return true
	default:
		return false
	}
}

func readItemFootprint(item ReadItem) uint64 {
	bytes := uint64(unsafe.Sizeof(ReadItem{})) + uint64(len(item.path)) + uint64(len(item.language)) + uint64(len(item.code))
	bytes += uint64(cap(item.lines)) * uint64(unsafe.Sizeof(ReadLine{}))
	for _, line := range item.lines {
		bytes += uint64(len(line.text))
	}
	bytes += RecordsFootprint(item.records)
	bytes += uint64(cap(item.warnings)) * uint64(unsafe.Sizeof(api.WarningCode("")))
	for _, warning := range item.warnings {
		bytes += uint64(len(warning))
	}
	return bytes
}

func readSnapshotFootprint(data *readSnapshotData) uint64 {
	bytes := uint64(unsafe.Sizeof(readSnapshotData{})) + uint64(cap(data.items))*uint64(unsafe.Sizeof(ReadItem{}))
	itemSize := uint64(unsafe.Sizeof(ReadItem{}))
	for _, item := range data.items {
		bytes += item.footprint - itemSize
	}
	return bytes
}
