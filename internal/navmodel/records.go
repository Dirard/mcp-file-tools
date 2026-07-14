package navmodel

import (
	"strings"
	"unsafe"

	"github.com/Dirard/mcp-file-tools/internal/api"
)

type Range struct {
	Start uint32
	End   uint32
}

func (lineRange Range) Valid() bool {
	return lineRange.Start != 0 && lineRange.End >= lineRange.Start
}

type RecordKind uint8

const (
	Import RecordKind = iota + 1
	Heading
	Symbol
)

type Record struct {
	Type  RecordKind
	Range Range
	Depth uint16
	Kind  api.Kind
	Name  string
}

func (record Record) Valid() bool {
	if !record.Range.Valid() {
		return false
	}
	switch record.Type {
	case Import:
		return record.Kind == ""
	case Heading:
		return record.Kind == api.KindSection
	case Symbol:
		return record.Kind.Valid()
	default:
		return false
	}
}

func NewRecord(record Record) (Record, bool) {
	if !record.Valid() {
		return Record{}, false
	}
	record.Name = strings.Clone(record.Name)
	return record, true
}

func CloneRecords(records []Record) ([]Record, bool) {
	if records == nil {
		return nil, true
	}
	cloned := make([]Record, len(records))
	for index, record := range records {
		copy, ok := NewRecord(record)
		if !ok {
			return nil, false
		}
		cloned[index] = copy
	}
	return cloned, true
}

type OutlineSeekKey struct {
	Start uint32
	End   uint32
	Type  RecordKind
	Depth uint16
	Name  string
}

func (record Record) OutlineSeekKey() OutlineSeekKey {
	return OutlineSeekKey{
		Start: record.Range.Start,
		End:   record.Range.End,
		Type:  record.Type,
		Depth: record.Depth,
		Name:  record.Name,
	}
}

type SymbolSeekKey struct {
	Path  string
	Start uint32
	End   uint32
	Kind  api.Kind
	Name  string
}

func (record Record) SymbolSeekKey(path string) SymbolSeekKey {
	return SymbolSeekKey{
		Path:  path,
		Start: record.Range.Start,
		End:   record.Range.End,
		Kind:  record.Kind,
		Name:  record.Name,
	}
}

func RecordsFootprint(records []Record) uint64 {
	bytes := uint64(cap(records)) * uint64(unsafe.Sizeof(Record{}))
	for _, record := range records {
		bytes += uint64(len(record.Name))
	}
	return bytes
}
