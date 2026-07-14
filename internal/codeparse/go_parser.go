package codeparse

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strconv"
	"strings"

	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func parseGo(source []byte) parseOutput {
	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, "source.go", source, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return parseOutput{fatal: true}
	}

	output := parseOutput{records: make([]rawRecord, 0, len(file.Decls)+1)}
	if parseErr != nil {
		var exact bool
		output.errorRanges, exact = goErrorRanges(parseErr)
		if !exact || len(output.errorRanges) == 0 {
			return parseOutput{fatal: true}
		}
	}

	if file.Name != nil {
		output.records = append(output.records, rawRecord{
			kind:      "package",
			lineRange: goLineRange(fset, file.Package, file.Name.End()),
			name:      file.Name.Name,
		})
	}

	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			output.records = append(output.records, goGenRecords(fset, declaration)...)
		case *ast.FuncDecl:
			output.records = append(output.records, goFuncRecord(fset, declaration))
		}
	}
	return output
}

func goGenRecords(fset *token.FileSet, declaration *ast.GenDecl) []rawRecord {
	records := make([]rawRecord, 0, len(declaration.Specs))
	for _, specification := range declaration.Specs {
		switch specification := specification.(type) {
		case *ast.ImportSpec:
			name := strings.Trim(specification.Path.Value, `"`)
			if unquoted, err := strconv.Unquote(specification.Path.Value); err == nil {
				name = unquoted
			}
			records = append(records, rawRecord{
				kind:      "import",
				lineRange: goLineRange(fset, goSpecStart(specification), specification.End()),
				name:      name,
			})
		case *ast.ValueSpec:
			kind := strings.ToLower(declaration.Tok.String())
			lineRange := goLineRange(fset, goSpecStart(specification), specification.End())
			for _, name := range specification.Names {
				records = append(records, rawRecord{kind: kind, lineRange: lineRange, name: name.Name})
			}
		case *ast.TypeSpec:
			kind := "type"
			switch specification.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind = "interface"
			}
			lineRange := goLineRange(fset, goSpecStart(specification), specification.End())
			records = append(records, rawRecord{kind: kind, lineRange: lineRange, name: specification.Name.Name})
			records = append(records, goTypeMembers(fset, specification.Type)...)
		}
	}
	return records
}

func goTypeMembers(fset *token.FileSet, expression ast.Expr) []rawRecord {
	var fields *ast.FieldList
	kind := "field"
	switch typed := expression.(type) {
	case *ast.StructType:
		fields = typed.Fields
	case *ast.InterfaceType:
		fields = typed.Methods
		kind = "method"
	default:
		return nil
	}
	if fields == nil {
		return nil
	}
	records := make([]rawRecord, 0, len(fields.List))
	for _, field := range fields.List {
		lineRange := goLineRange(fset, goFieldStart(field), field.End())
		if len(field.Names) == 0 {
			name := goExprName(field.Type)
			if name != "" {
				records = append(records, rawRecord{kind: kind, lineRange: lineRange, depth: 1, name: name})
			}
			continue
		}
		for _, name := range field.Names {
			memberKind := kind
			if _, isMethod := field.Type.(*ast.FuncType); !isMethod && kind == "method" {
				memberKind = "type"
			}
			records = append(records, rawRecord{kind: memberKind, lineRange: lineRange, depth: 1, name: name.Name})
		}
	}
	return records
}

func goFuncRecord(fset *token.FileSet, declaration *ast.FuncDecl) rawRecord {
	start := declaration.Pos()
	if declaration.Doc != nil {
		start = declaration.Doc.Pos()
	}
	kind := "function"
	name := declaration.Name.Name
	var depth uint16
	if declaration.Recv != nil && len(declaration.Recv.List) != 0 {
		kind = "method"
		depth = 1
		if receiver := goExprName(declaration.Recv.List[0].Type); receiver != "" {
			name = receiver + "." + name
		}
	}
	return rawRecord{kind: kind, lineRange: goLineRange(fset, start, declaration.End()), depth: depth, name: name}
}

func goExprName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return goExprName(expression.X)
	case *ast.ParenExpr:
		return goExprName(expression.X)
	case *ast.IndexExpr:
		return goExprName(expression.X)
	case *ast.IndexListExpr:
		return goExprName(expression.X)
	case *ast.SelectorExpr:
		prefix := goExprName(expression.X)
		if prefix == "" {
			return expression.Sel.Name
		}
		return prefix + "." + expression.Sel.Name
	default:
		return ""
	}
}

func goSpecStart(specification ast.Spec) token.Pos {
	switch specification := specification.(type) {
	case *ast.ImportSpec:
		if specification.Doc != nil {
			return specification.Doc.Pos()
		}
	case *ast.ValueSpec:
		if specification.Doc != nil {
			return specification.Doc.Pos()
		}
	case *ast.TypeSpec:
		if specification.Doc != nil {
			return specification.Doc.Pos()
		}
	}
	return specification.Pos()
}

func goFieldStart(field *ast.Field) token.Pos {
	if field.Doc != nil {
		return field.Doc.Pos()
	}
	return field.Pos()
}

func goLineRange(fset *token.FileSet, start, end token.Pos) navmodel.Range {
	startLine := fset.PositionFor(start, false).Line
	endLine := fset.PositionFor(end, false).Line
	if startLine < 1 || endLine < startLine {
		return navmodel.Range{}
	}
	return navmodel.Range{Start: uint32(startLine), End: uint32(endLine)}
}

func goErrorRanges(parseErr error) ([]navmodel.Range, bool) {
	var errorsList scanner.ErrorList
	switch typed := parseErr.(type) {
	case scanner.ErrorList:
		errorsList = typed
	case *scanner.Error:
		errorsList = scanner.ErrorList{typed}
	case scanner.Error:
		copy := typed
		errorsList = scanner.ErrorList{&copy}
	default:
		return nil, false
	}
	ranges := make([]navmodel.Range, 0, len(errorsList))
	for _, parseError := range errorsList {
		if parseError == nil || parseError.Pos.Line < 1 {
			return nil, false
		}
		line := uint32(parseError.Pos.Line)
		ranges = append(ranges, navmodel.Range{Start: line, End: line})
	}
	return ranges, len(ranges) != 0
}

func filterUnsafeRecords(records []rawRecord, errorRanges []navmodel.Range) []rawRecord {
	if len(errorRanges) == 0 {
		return records
	}
	safe := make([]rawRecord, 0, len(records))
	for _, record := range records {
		if !record.lineRange.Valid() {
			continue
		}
		unsafe := false
		for _, errorRange := range errorRanges {
			if record.lineRange.Start <= errorRange.End && errorRange.Start <= record.lineRange.End {
				unsafe = true
				break
			}
		}
		if !unsafe {
			safe = append(safe, record)
		}
	}
	return safe
}
