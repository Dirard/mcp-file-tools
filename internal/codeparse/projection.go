package codeparse

import (
	"sort"
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func projectRecords(raw []rawRecord) ([]navmodel.Record, bool) {
	projected := make([]navmodel.Record, 0, len(raw))
	seenExact := make(map[navmodel.Record]struct{}, len(raw))

	for index, candidate := range raw {
		record, keep := mapRawRecord(candidate, hasMappedDescendant(raw, index))
		if !keep {
			continue
		}
		owned, ok := navmodel.NewRecord(record)
		if !ok {
			return nil, false
		}
		if _, duplicate := seenExact[owned]; duplicate {
			continue
		}
		seenExact[owned] = struct{}{}
		projected = append(projected, owned)
	}

	sort.Slice(projected, func(left, right int) bool {
		a, b := projected[left], projected[right]
		if a.Range.Start != b.Range.Start {
			return a.Range.Start < b.Range.Start
		}
		if a.Range.End != b.Range.End {
			return a.Range.End < b.Range.End
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Kind < b.Kind
	})

	outlineKeys := make(map[navmodel.OutlineSeekKey]struct{}, len(projected))
	symbolKeys := make(map[navmodel.SymbolSeekKey]struct{}, len(projected))
	for _, record := range projected {
		outlineKey := record.OutlineSeekKey()
		if _, exists := outlineKeys[outlineKey]; exists {
			return nil, false
		}
		outlineKeys[outlineKey] = struct{}{}
		if record.Type == navmodel.Symbol {
			symbolKey := record.SymbolSeekKey("")
			if _, exists := symbolKeys[symbolKey]; exists {
				return nil, false
			}
			symbolKeys[symbolKey] = struct{}{}
		}
	}

	if projected == nil {
		return []navmodel.Record{}, true
	}
	return projected, true
}

func hasMappedDescendant(records []rawRecord, parentIndex int) bool {
	parent := records[parentIndex]
	for index, candidate := range records {
		if index == parentIndex || candidate.lineRange.Start < parent.lineRange.Start || candidate.lineRange.End > parent.lineRange.End {
			continue
		}
		if _, keep := mapRawRecord(candidate, false); keep {
			return true
		}
	}
	return false
}

func mapRawRecord(raw rawRecord, hasMappedChild bool) (navmodel.Record, bool) {
	record := navmodel.Record{Range: raw.lineRange, Depth: raw.depth, Name: raw.name}
	switch raw.kind {
	case "import", "re_export":
		record.Type = navmodel.Import
		return record, true
	case "import_block", "value":
		return navmodel.Record{}, false
	case "section", "frontmatter":
		record.Type = navmodel.Heading
		record.Kind = api.KindSection
		return record, true
	case "package":
		record.Kind = api.KindPackage
	case "module":
		record.Kind = api.KindModule
	case "namespace":
		record.Kind = api.KindNamespace
	case "class":
		record.Kind = api.KindClass
	case "interface", "annotation", "protocol":
		record.Kind = api.KindInterface
	case "struct", "record":
		record.Kind = api.KindStruct
	case "enum":
		record.Kind = api.KindEnum
	case "trait":
		record.Kind = api.KindTrait
	case "type", "union", "typealias":
		record.Kind = api.KindType
	case "constant", "const", "enum_case":
		record.Kind = api.KindConstant
	case "variable", "var", "static":
		record.Kind = api.KindVariable
	case "field":
		record.Kind = api.KindField
	case "property", "key":
		record.Kind = api.KindProperty
	case "function":
		record.Kind = api.KindFunction
	case "method":
		record.Kind = api.KindMethod
	case "constructor":
		record.Kind = api.KindConstructor
	case "object", "companion_object", "mapping", "array", "sequence", "element":
		record.Kind = api.KindObject
	case "component":
		record.Kind = api.KindComponent
	case "module_script", "script", "style", "markup":
		record.Kind = api.KindSection
	case "macro", "impl":
		record.Kind = api.KindOther
	case "document", "stream":
		if hasMappedChild {
			return navmodel.Record{}, false
		}
		record.Kind = api.KindSection
	default:
		if (strings.HasSuffix(raw.kind, "_block") || raw.kind == "block") && hasMappedChild {
			return navmodel.Record{}, false
		}
		record.Kind = api.KindOther
	}
	record.Type = navmodel.Symbol
	return record, true
}
