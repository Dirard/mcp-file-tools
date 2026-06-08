package handler

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

func (h *Handler) outlineGo(ctx context.Context, info fileTextInfo, options outlineOptions) (OutlineFileOutput, error) {
	if err := contextError(ctx); err != nil {
		return OutlineFileOutput{}, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, info.resolvedPath, nil, parser.ParseComments)
	if err != nil {
		output := outlineBaseOutput(info, "parse_error", outlineLanguageGo)
		output.Error = err.Error()
		return output, err
	}

	imports := make([]OutlineItem, 0)
	symbols := make([]OutlineItem, 0)
	for _, decl := range file.Decls {
		if err := contextError(ctx); err != nil {
			return OutlineFileOutput{}, err
		}
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.IMPORT:
				imports = append(imports, outlineGoImportDecl(info, fset, d))
			case token.CONST, token.VAR, token.TYPE:
				symbols = append(symbols, outlineGoGenDecl(info, fset, d))
			}
		case *ast.FuncDecl:
			symbols = append(symbols, outlineGoFuncDecl(info, fset, d))
		}
	}

	output := outlineBaseOutput(info, "ok", outlineLanguageGo)
	output.ParserScope = "go_ast_declarations"
	if options.enclosingLine != nil {
		output.EnclosingItems = enclosingOutlineItems(append(append([]OutlineItem{}, imports...), symbols...), *options.enclosingLine)
	}
	output.Imports, output.Symbols, output.Sections, output.OutlineStats, output.Truncated = finalizeOutlineCategories(imports, symbols, nil, options)
	return output, nil
}

func outlineGoImportDecl(info fileTextInfo, fset *token.FileSet, decl *ast.GenDecl) OutlineItem {
	start := goGenDeclStart(decl)
	r := goNodeLineRange(fset, start, decl.End())
	byteRange := goNodeByteRange(fset, start, decl.End())
	item := OutlineItem{
		ID:     fmt.Sprintf("go:import_block:%d", r.StartLine),
		Kind:   "import_block",
		Name:   "import",
		Detail: "import block",
		Path:   []string{"imports"},
		Range:  r,
		Depth:  1,
	}
	for _, spec := range decl.Specs {
		importSpec, ok := spec.(*ast.ImportSpec)
		if !ok {
			continue
		}
		name := strings.Trim(importSpec.Path.Value, `"`)
		if unquoted, err := strconv.Unquote(importSpec.Path.Value); err == nil {
			name = unquoted
		}
		detail := name
		if importSpec.Name != nil {
			detail = importSpec.Name.Name + " " + name
		}
		start := importSpec.Pos()
		if importSpec.Doc != nil {
			start = importSpec.Doc.Pos()
		}
		r := goNodeLineRange(fset, start, importSpec.End())
		childByteRange := goNodeByteRange(fset, start, importSpec.End())
		item.Children = append(item.Children, exactOutlineItemWithSelector(info, outlineLanguageGo, OutlineItem{
			ID:     fmt.Sprintf("go:import:%d:%s", r.StartLine, sanitizeOutlineIDPart(name)),
			Kind:   "import",
			Name:   name,
			Detail: detail,
			Path:   []string{"imports", name},
			Range:  r,
			Depth:  2,
		}, childByteRange, true, true, ""))
	}
	return exactOutlineItemWithSelector(info, outlineLanguageGo, item, byteRange, true, true, "")
}

func outlineGoGenDecl(info fileTextInfo, fset *token.FileSet, decl *ast.GenDecl) OutlineItem {
	kind := strings.ToLower(decl.Tok.String())
	start := goGenDeclStart(decl)
	r := goNodeLineRange(fset, start, decl.End())
	byteRange := goNodeByteRange(fset, start, decl.End())
	item := OutlineItem{
		ID:     fmt.Sprintf("go:%s_block:%d", kind, r.StartLine),
		Kind:   kind + "_block",
		Name:   kind,
		Detail: decl.Tok.String() + " block",
		Path:   []string{kind},
		Range:  r,
		Depth:  1,
	}
	for _, spec := range decl.Specs {
		start := goSpecStart(spec)
		r := goNodeLineRange(fset, start, spec.End())
		childByteRange := goNodeByteRange(fset, start, spec.End())
		name := kind
		switch s := spec.(type) {
		case *ast.TypeSpec:
			name = s.Name.Name
		case *ast.ValueSpec:
			names := make([]string, 0, len(s.Names))
			for _, n := range s.Names {
				names = append(names, n.Name)
			}
			name = strings.Join(names, ", ")
		}
		item.Children = append(item.Children, exactOutlineItemWithSelector(info, outlineLanguageGo, OutlineItem{
			ID:     fmt.Sprintf("go:%s:%d:%s", kind, r.StartLine, sanitizeOutlineIDPart(name)),
			Kind:   kind,
			Name:   name,
			Detail: decl.Tok.String(),
			Path:   []string{kind, name},
			Range:  r,
			Depth:  2,
		}, childByteRange, true, true, ""))
	}
	return exactOutlineItemWithSelector(info, outlineLanguageGo, item, byteRange, true, true, "")
}

func outlineGoFuncDecl(info fileTextInfo, fset *token.FileSet, decl *ast.FuncDecl) OutlineItem {
	start := decl.Pos()
	if decl.Doc != nil {
		start = decl.Doc.Pos()
	}
	r := goNodeLineRange(fset, start, decl.End())
	kind := "function"
	name := decl.Name.Name
	path := []string{"functions", name}
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		kind = "method"
		receiver := goReceiverName(decl.Recv.List[0].Type)
		if receiver != "" {
			path = []string{"methods", receiver, name}
			name = receiver + "." + name
		}
	}
	byteRange := goNodeByteRange(fset, start, decl.End())
	return exactOutlineItemWithSelector(info, outlineLanguageGo, OutlineItem{
		ID:     fmt.Sprintf("go:%s:%d:%s", kind, r.StartLine, sanitizeOutlineIDPart(name)),
		Kind:   kind,
		Name:   name,
		Detail: kind,
		Path:   path,
		Range:  r,
		Depth:  len(path),
	}, byteRange, true, true, "")
}

func goGenDeclStart(decl *ast.GenDecl) token.Pos {
	if decl.Doc != nil {
		return decl.Doc.Pos()
	}
	return decl.Pos()
}

func goSpecStart(spec ast.Spec) token.Pos {
	switch s := spec.(type) {
	case *ast.ImportSpec:
		if s.Doc != nil {
			return s.Doc.Pos()
		}
	case *ast.TypeSpec:
		if s.Doc != nil {
			return s.Doc.Pos()
		}
	case *ast.ValueSpec:
		if s.Doc != nil {
			return s.Doc.Pos()
		}
	}
	return spec.Pos()
}

func goReceiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return goReceiverName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return goReceiverName(t.X)
	case *ast.IndexListExpr:
		return goReceiverName(t.X)
	default:
		return ""
	}
}

func goNodeLineRange(fset *token.FileSet, start, end token.Pos) SourceLineRange {
	return SourceLineRange{
		StartLine: fset.Position(start).Line,
		EndLine:   fset.Position(end).Line,
	}
}

func goNodeByteRange(fset *token.FileSet, start, end token.Pos) SourceByteRange {
	return SourceByteRange{
		StartByte:        fset.Position(start).Offset,
		EndByteExclusive: fset.Position(end).Offset,
	}
}
