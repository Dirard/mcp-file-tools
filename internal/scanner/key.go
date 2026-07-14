package scanner

import (
	"encoding/binary"
	"strings"
)

func compareRows(left, right Row) int {
	if compared := strings.Compare(left.Path, right.Path); compared != 0 {
		return compared
	}
	leftCategory := rowCategory(left.Kind)
	rightCategory := rowCategory(right.Kind)
	if leftCategory != rightCategory {
		if leftCategory < rightCategory {
			return -1
		}
		return 1
	}
	switch leftCategory {
	case 1:
		if left.Line != right.Line {
			if left.Line < right.Line {
				return -1
			}
			return 1
		}
		if left.Kind != right.Kind {
			if left.Kind == RowTextMatch {
				return -1
			}
			return 1
		}
		return strings.Compare(left.Text, right.Text)
	case 2:
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
		if compared := strings.Compare(string(left.SymbolKind), string(right.SymbolKind)); compared != 0 {
			return compared
		}
		return strings.Compare(left.Name, right.Name)
	default:
		if left.Kind == right.Kind {
			return 0
		}
		if left.Kind == RowDirectory {
			return -1
		}
		return 1
	}
}

func rowCategory(kind RowKind) byte {
	switch kind {
	case RowTextMatch, RowTextContext:
		return 1
	case RowSymbol:
		return 2
	case RowDirectory, RowFile:
		return 3
	default:
		return 255
	}
}

func encodeRowKey(row Row) []byte {
	key := make([]byte, 0, len(row.Path)+len(row.Text)+len(row.Name)+24)
	key = append(key, row.Path...)
	key = append(key, 0, rowCategory(row.Kind))
	switch rowCategory(row.Kind) {
	case 1:
		var line [8]byte
		binary.BigEndian.PutUint64(line[:], row.Line)
		key = append(key, line[:]...)
		if row.Kind == RowTextMatch {
			key = append(key, 0)
		} else {
			key = append(key, 1)
		}
		key = append(key, row.Text...)
	case 2:
		var positions [8]byte
		binary.BigEndian.PutUint32(positions[0:4], row.Range.Start)
		binary.BigEndian.PutUint32(positions[4:8], row.Range.End)
		key = append(key, positions[:]...)
		key = append(key, row.SymbolKind...)
		key = append(key, 0)
		key = append(key, row.Name...)
	default:
		if row.Kind == RowDirectory {
			key = append(key, 0)
		} else {
			key = append(key, 1)
		}
	}
	return key
}
