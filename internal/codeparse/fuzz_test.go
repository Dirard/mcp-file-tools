package codeparse

import (
	"reflect"
	"testing"

	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func FuzzProjection(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte("function\x00method\xff"))
	kinds := [...]string{"import", "section", "function", "method", "class", "record", "value", "document", "unknown_leaf", "type_block"}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			data = data[:256]
		}
		raw := make([]rawRecord, 0, len(data)/4)
		for index := 0; index+3 < len(data); index += 4 {
			start := uint32(data[index]%32) + 1
			end := start + uint32(data[index+1]%8)
			raw = append(raw, rawRecord{
				kind:      kinds[int(data[index+2])%len(kinds)],
				lineRange: navmodel.Range{Start: start, End: end},
				depth:     uint16(data[index+3] % 8),
				name:      string(data[index : index+4]),
			})
		}
		first, firstOK := projectRecords(raw)
		second, secondOK := projectRecords(raw)
		if firstOK != secondOK || !reflect.DeepEqual(first, second) {
			t.Fatalf("projection is nondeterministic: %#v,%t then %#v,%t", first, firstOK, second, secondOK)
		}
		if firstOK {
			for _, record := range first {
				if !record.Valid() {
					t.Fatalf("projection emitted invalid record: %#v", record)
				}
			}
		}
	})
}
