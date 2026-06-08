package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const (
	configPathMarkerPrefix = "\x00cfg:"
	configPathIndexPrefix  = configPathMarkerPrefix + "index:"
)

func (h *Handler) outlineTreeSitter(ctx context.Context, info fileTextInfo, language string, options outlineOptions) (OutlineFileOutput, error) {
	if err := contextError(ctx); err != nil {
		return OutlineFileOutput{}, err
	}
	lang := treeSitterLanguage(language)
	if lang == nil {
		output := outlineBaseOutput(info, "parser_dependency_unavailable", language)
		output.Warnings = append(output.Warnings, ToolWarning{
			Code:    "parser_dependency_unavailable",
			Message: fmt.Sprintf("No tree-sitter grammar is available for language %q.", language),
			File:    info.displayPath,
		})
		return output, nil
	}
	source, err := readDecodedFileBytes(ctx, info.resolvedPath, info.encoding)
	if err != nil {
		return OutlineFileOutput{}, err
	}
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		output := outlineBaseOutput(info, "parse_error", language)
		output.Error = err.Error()
		return output, err
	}
	root := tree.RootNode()
	if root == nil {
		output := outlineBaseOutput(info, "parse_error", language)
		output.Error = "parser returned nil root"
		return output, errors.New(output.Error)
	}

	parserStatus := "ok"
	warnings := []ToolWarning{}
	if root.HasError() {
		parserStatus = "partial"
		warning := ToolWarning{
			Code:    "parse_error",
			Message: "Tree-sitter parsed the file with syntax errors; exact read ranges may be returned, but write-safe selectors are disabled for affected syntax.",
			File:    info.displayPath,
		}
		if errorLine := firstTreeSitterErrorLine(root); errorLine > 0 {
			warning.Line = errorLine
		}
		warnings = append(warnings, warning)
	}
	if language == outlineLanguageSvelte {
		parserStatus = "partial"
		warnings = append(warnings, ToolWarning{
			Code:    "svelte_nested_symbols_unsupported",
			Message: "Svelte block ranges are exact, but nested script symbol extraction is not yet supported; symbol write recommendations are disabled for this partial outline.",
			File:    info.displayPath,
		})
	}

	extractor := treeSitterExtractor{
		info:     info,
		language: language,
		lang:     lang,
		source:   source,
	}
	items := []OutlineItem{}
	if rootSpec, ok := extractor.rootSymbolSpec(root); ok {
		rootItem := extractor.outlineItem(root, rootSpec, nil, 0, parserStatus == "ok")
		childParentKind := rootSpec.kind
		if rootSpec.kind == "stream" && treeSitterNamedChildCountByType(root, lang, "document") <= 1 {
			childParentKind = ""
		}
		items = extractor.extract(ctx, root, nil, childParentKind, 0, parserStatus == "ok")
		items = extractor.groupTopLevelImportBlocks(items, parserStatus == "ok")
		rootItem.Children = items
		items = []OutlineItem{rootItem}
	} else {
		items = extractor.extract(ctx, root, nil, "", 0, parserStatus == "ok")
		items = extractor.groupTopLevelImportBlocks(items, parserStatus == "ok")
	}
	fullItems := items
	enclosingItems := []OutlineItem{}
	if options.enclosingLine != nil {
		enclosingItems = enclosingOutlineItems(fullItems, *options.enclosingLine)
	}
	omittedLeafItems := 0
	if language == outlineLanguageJSON || language == outlineLanguageYAML {
		items, omittedLeafItems = filterConfigOutlineProfile(items, options)
	}
	if isJSLikeLanguage(language) {
		items = filterJSLikeOutlineProfile(items, options)
	}
	if language == outlineLanguageC || language == outlineLanguageCPP {
		items = filterCFamilyOutlineProfile(items, options)
	}
	if language == outlineLanguageBash {
		items = filterBashOutlineProfile(items, options)
	}
	imports, symbols, sections := splitTreeSitterOutlineItems(items, language)
	if configAgentProfileCompactActive(options, language) {
		symbols = compactConfigOutlinePresentation(symbols)
		sections = compactConfigOutlinePresentation(compactConfigOutlineSections(sections))
	}
	imports, symbols, sections, stats, truncated := finalizeOutlineCategories(imports, symbols, sections, options)

	output := outlineBaseOutput(info, parserStatus, language)
	output.ParserScope = "tree_sitter_symbols"
	output.Warnings = warnings
	output.OutlineStats = stats
	output.OutlineStats.OmittedLeafItems = omittedLeafItems
	output.Truncated = truncated
	output.EnclosingItems = enclosingItems
	output.Imports = imports
	output.Symbols = symbols
	output.Sections = sections
	return output, nil
}

func (e treeSitterExtractor) rootSymbolSpec(root *gotreesitter.Node) (treeSitterSymbolSpec, bool) {
	if root == nil {
		return treeSitterSymbolSpec{}, false
	}
	nodeType := root.Type(e.lang)
	switch e.language {
	case outlineLanguageJSON:
		return treeSitterSymbolSpec{kind: "document", name: configPathMarkerPrefix + "document", detail: nodeType}, true
	case outlineLanguageYAML:
		return treeSitterSymbolSpec{kind: "stream", name: configPathMarkerPrefix + "stream", detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func splitTreeSitterOutlineItems(items []OutlineItem, language string) ([]OutlineItem, []OutlineItem, []OutlineItem) {
	imports := []OutlineItem{}
	symbols := []OutlineItem{}
	sections := []OutlineItem{}
	for _, item := range items {
		collectTreeSitterOutlineCategories(item, true, language, &imports, &symbols, &sections)
	}
	return imports, symbols, sections
}

func collectTreeSitterOutlineCategories(item OutlineItem, collectSection bool, language string, imports, symbols, sections *[]OutlineItem) {
	switch {
	case isTreeSitterImportKind(item.Kind):
		if item.Kind == "import_block" {
			*imports = append(*imports, item)
		} else {
			*imports = append(*imports, itemWithoutChildren(item))
		}
		return
	case isTreeSitterSectionKind(item.Kind, language):
		if collectSection {
			*sections = append(*sections, item)
		}
	default:
		*symbols = append(*symbols, itemWithoutChildren(item))
	}
	for _, child := range item.Children {
		collectTreeSitterOutlineCategories(child, false, language, imports, symbols, sections)
	}
}

func itemWithoutChildren(item OutlineItem) OutlineItem {
	item.Children = nil
	return item
}

func isTreeSitterImportKind(kind string) bool {
	return kind == "package" || kind == "import" || kind == "re_export" || kind == "import_block"
}

func isTreeSitterSectionKind(kind, language string) bool {
	if (kind == "object" || kind == "array") && language != outlineLanguageJSON {
		return false
	}
	switch kind {
	case "document", "object", "array", "stream", "mapping", "sequence", "markup":
		return true
	default:
		return false
	}
}

func treeSitterLanguage(language string) *gotreesitter.Language {
	switch language {
	case outlineLanguageJavaScript:
		return grammars.JavascriptLanguage()
	case outlineLanguageTypeScript:
		return grammars.TypescriptLanguage()
	case outlineLanguageTSX:
		return grammars.TsxLanguage()
	case outlineLanguagePython:
		return grammars.PythonLanguage()
	case outlineLanguageJava:
		return grammars.JavaLanguage()
	case outlineLanguageRust:
		return grammars.RustLanguage()
	case outlineLanguageC:
		return grammars.CLanguage()
	case outlineLanguageCPP:
		return grammars.CppLanguage()
	case outlineLanguageCSharp:
		return grammars.CSharpLanguage()
	case outlineLanguageRuby:
		return grammars.RubyLanguage()
	case outlineLanguageKotlin:
		return grammars.KotlinLanguage()
	case outlineLanguageSwift:
		return grammars.SwiftLanguage()
	case outlineLanguageBash:
		return grammars.BashLanguage()
	case outlineLanguageJSON:
		return grammars.JsonLanguage()
	case outlineLanguageYAML:
		return grammars.YamlLanguage()
	case outlineLanguageSvelte:
		return grammars.SvelteLanguage()
	default:
		return nil
	}
}

func isJSLikeLanguage(language string) bool {
	return language == outlineLanguageJavaScript || language == outlineLanguageTypeScript || language == outlineLanguageTSX
}

type treeSitterExtractor struct {
	info     fileTextInfo
	language string
	lang     *gotreesitter.Language
	source   []byte
}

func (e treeSitterExtractor) extract(ctx context.Context, node *gotreesitter.Node, parentPath []string, parentKind string, depth int, parserClean bool) []OutlineItem {
	items := []OutlineItem{}
	indexedChild := 0
	for i := 0; i < node.ChildCount(); i++ {
		if err := contextError(ctx); err != nil {
			return items
		}
		child := node.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		nodeType := child.Type(e.lang)
		if treeSitterYAMLSequenceItemWrapper(e.language, parentKind, nodeType) {
			childPath := append(append([]string(nil), parentPath...), configIndexPathSegment(indexedChild))
			indexedChild++
			items = append(items, e.extract(ctx, child, childPath, "", depth, parserClean && !child.HasError())...)
			continue
		}
		if spec, ok := e.symbolSpec(child); ok {
			spec = e.refineSymbolSpec(child, spec, parentPath, parentKind)
			if spec.name == "" {
				spec.name = unnamedTreeSitterName(child, e.lang)
			}
			rawName := configRawPathName(e.language, spec.kind, spec.name)
			if treeSitterParentUsesIndexedPath(parentKind) {
				rawName = configIndexPathSegment(indexedChild)
				spec.name = rawName
				indexedChild++
			}
			item := e.outlineItem(child, spec, parentPath, depth, parserClean)
			childPath := append(append([]string(nil), parentPath...), rawName)
			if isJSLikeLanguage(e.language) && nodeType == "export_statement" {
				item.Children = nil
			} else if e.language == outlineLanguagePython && nodeType == "decorated_definition" {
				item.Children = e.extractDecoratedDefinitionChildren(ctx, child, childPath, item.Kind, depth+1, parserClean && !child.HasError())
			} else {
				item.Children = e.extract(ctx, child, childPath, item.Kind, depth+1, parserClean && !child.HasError())
			}
			items = append(items, item)
			if isJSLikeLanguage(e.language) && nodeType == "export_statement" {
				items = append(items, e.extractExportDeclarationChildren(ctx, child, parentPath, parentKind, depth, parserClean && !child.HasError())...)
			}
			continue
		}
		items = append(items, e.extract(ctx, child, parentPath, parentKind, depth, parserClean && !child.HasError())...)
	}
	return items
}

func treeSitterParentUsesIndexedPath(parentKind string) bool {
	return parentKind == "array" || parentKind == "sequence" || parentKind == "stream"
}

func treeSitterYAMLSequenceItemWrapper(language, parentKind, nodeType string) bool {
	return language == outlineLanguageYAML &&
		parentKind == "sequence" &&
		(nodeType == "block_sequence_item" || nodeType == "flow_sequence_item")
}

func treeSitterNamedChildCountByType(node *gotreesitter.Node, lang *gotreesitter.Language, nodeType string) int {
	if node == nil {
		return 0
	}
	count := 0
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && child.IsNamed() && child.Type(lang) == nodeType {
			count++
		}
	}
	return count
}

func (e treeSitterExtractor) extractExportDeclarationChildren(ctx context.Context, node *gotreesitter.Node, parentPath []string, parentKind string, depth int, parserClean bool) []OutlineItem {
	items := []OutlineItem{}
	for i := 0; i < node.ChildCount(); i++ {
		if err := contextError(ctx); err != nil {
			return items
		}
		child := node.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		if spec, ok := e.symbolSpec(child); ok && spec.kind != "import" && spec.kind != "export" {
			spec = e.refineSymbolSpec(child, spec, parentPath, parentKind)
			if spec.name == "" {
				spec.name = unnamedTreeSitterName(child, e.lang)
			}
			rawName := spec.name
			item := e.outlineItem(child, spec, parentPath, depth, parserClean)
			childPath := append(append([]string(nil), parentPath...), rawName)
			item.Children = e.extract(ctx, child, childPath, item.Kind, depth+1, parserClean && !child.HasError())
			items = append(items, item)
			continue
		}
		items = append(items, e.extract(ctx, child, parentPath, parentKind, depth, parserClean && !child.HasError())...)
	}
	return items
}

func (e treeSitterExtractor) extractDecoratedDefinitionChildren(ctx context.Context, node *gotreesitter.Node, parentPath []string, parentKind string, depth int, parserClean bool) []OutlineItem {
	items := []OutlineItem{}
	for i := 0; i < node.ChildCount(); i++ {
		if err := contextError(ctx); err != nil {
			return items
		}
		child := node.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		items = append(items, e.extract(ctx, child, parentPath, parentKind, depth, parserClean && !child.HasError())...)
	}
	return items
}

func (e treeSitterExtractor) groupTopLevelImportBlocks(items []OutlineItem, parserClean bool) []OutlineItem {
	if !isJSLikeLanguage(e.language) {
		return items
	}
	grouped := make([]OutlineItem, 0, len(items))
	for i := 0; i < len(items); {
		if !isTreeSitterTopLevelImportBlockMember(items[i]) {
			grouped = append(grouped, items[i])
			i++
			continue
		}
		start := i
		for i < len(items) && isTreeSitterTopLevelImportBlockMember(items[i]) {
			i++
		}
		grouped = append(grouped, e.importBlockItem(items[start:i], parserClean))
	}
	return grouped
}

func isTreeSitterTopLevelImportBlockMember(item OutlineItem) bool {
	return item.Depth == 0 && item.Kind == "import"
}

func (e treeSitterExtractor) importBlockItem(children []OutlineItem, parserClean bool) OutlineItem {
	first := children[0]
	last := children[len(children)-1]
	r := SourceLineRange{StartLine: first.Range.StartLine, EndLine: last.Range.EndLine}
	byteRange := lineByteRangeForSource(e.source, r)
	if first.ByteRange != nil && last.ByteRange != nil {
		byteRange = SourceByteRange{StartByte: first.ByteRange.StartByte, EndByteExclusive: last.ByteRange.EndByteExclusive}
	}
	wholeLineRange := true
	writeSafe := parserClean
	for _, child := range children {
		wholeLineRange = wholeLineRange && boolValue(child.WholeLineRange)
		writeSafe = writeSafe && boolValue(child.WriteSafe)
	}
	refusalReason := ""
	if !wholeLineRange {
		refusalReason = "symbol_range_not_write_safe"
	}
	if !parserClean {
		refusalReason = "symbol_parser_not_write_safe"
	}
	item := OutlineItem{
		ID:       fmt.Sprintf("%s:import_block:%d", e.language, r.StartLine),
		Kind:     "import_block",
		Name:     "import_block",
		Detail:   "adjacent_import_statements",
		Path:     []string{"import_block"},
		Range:    r,
		Depth:    0,
		Children: append([]OutlineItem(nil), children...),
		Metadata: map[string]string{"node_type": "import_block"},
	}
	return exactOutlineItemWithSelector(e.info, e.language, item, byteRange, wholeLineRange, writeSafe, refusalReason)
}

func (e treeSitterExtractor) refineSymbolSpec(node *gotreesitter.Node, spec treeSitterSymbolSpec, parentPath []string, parentKind string) treeSitterSymbolSpec {
	if e.language == outlineLanguagePython && spec.kind == "function" && parentKind == "class" {
		spec.kind = "method"
	}
	if e.language == outlineLanguageRust && spec.kind == "function" && (parentKind == "impl" || parentKind == "trait") {
		spec.kind = "method"
	}
	if (e.language == outlineLanguageKotlin || e.language == outlineLanguageSwift) && spec.kind == "function" && (parentKind == "class" || parentKind == "struct" || parentKind == "enum" || parentKind == "protocol" || parentKind == "interface" || parentKind == "object" || parentKind == "companion_object") {
		spec.kind = "method"
	}
	if (e.language == outlineLanguageJavaScript || e.language == outlineLanguageTypeScript || e.language == outlineLanguageTSX) && isPascalCaseName(spec.name) {
		if spec.kind == "function" || spec.kind == "variable" {
			if (e.language == outlineLanguageTSX || e.language == outlineLanguageJavaScript) && nodeContainsJSXLikeMarkup(node.Text(e.source)) {
				spec.kind = "component"
			}
		}
	}
	return spec
}

type treeSitterSymbolSpec struct {
	kind   string
	name   string
	detail string
}

func (e treeSitterExtractor) symbolSpec(node *gotreesitter.Node) (treeSitterSymbolSpec, bool) {
	nodeType := node.Type(e.lang)
	switch e.language {
	case outlineLanguageJavaScript, outlineLanguageTypeScript, outlineLanguageTSX:
		return e.jsLikeSymbolSpec(node, nodeType)
	case outlineLanguagePython:
		return e.pythonSymbolSpec(node, nodeType)
	case outlineLanguageJava:
		return e.javaSymbolSpec(node, nodeType)
	case outlineLanguageRust:
		return e.rustSymbolSpec(node, nodeType)
	case outlineLanguageC, outlineLanguageCPP:
		return e.cFamilySymbolSpec(node, nodeType)
	case outlineLanguageCSharp:
		return e.csharpSymbolSpec(node, nodeType)
	case outlineLanguageRuby:
		return e.rubySymbolSpec(node, nodeType)
	case outlineLanguageKotlin:
		return e.kotlinSymbolSpec(node, nodeType)
	case outlineLanguageSwift:
		return e.swiftSymbolSpec(node, nodeType)
	case outlineLanguageBash:
		return e.bashSymbolSpec(node, nodeType)
	case outlineLanguageJSON:
		return e.jsonSymbolSpec(node, nodeType)
	case outlineLanguageYAML:
		return e.yamlSymbolSpec(node, nodeType)
	case outlineLanguageSvelte:
		return e.svelteSymbolSpec(node, nodeType)
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) jsLikeSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "import_statement":
		name := e.nodeName(node)
		if name == "" {
			name = "import"
		}
		return treeSitterSymbolSpec{kind: "import", name: name, detail: nodeType}, true
	case "export_statement":
		if !e.jsLikeExportHasSource(node) {
			return treeSitterSymbolSpec{}, false
		}
		name := e.nodeName(node)
		if name == "" {
			name = firstTreeSitterLine(node.Text(e.source))
		}
		if name == "" {
			name = "re_export"
		}
		return treeSitterSymbolSpec{kind: "re_export", name: name, detail: nodeType}, true
	case "function_declaration", "generator_function_declaration":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "class_declaration":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "method_definition":
		return treeSitterSymbolSpec{kind: "method", name: e.nodeName(node), detail: nodeType}, true
	case "interface_declaration":
		return treeSitterSymbolSpec{kind: "interface", name: e.nodeName(node), detail: nodeType}, true
	case "type_alias_declaration":
		return treeSitterSymbolSpec{kind: "type", name: e.nodeName(node), detail: nodeType}, true
	case "variable_declarator":
		name := e.nodeName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "variable", name: name, detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) jsLikeExportHasSource(node *gotreesitter.Node) bool {
	return node.ChildByFieldName("source", e.lang) != nil
}

func (e treeSitterExtractor) pythonSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "import_statement", "import_from_statement":
		name := e.nodeName(node)
		if name == "" {
			name = firstTreeSitterLine(node.Text(e.source))
		}
		return treeSitterSymbolSpec{kind: "import", name: name, detail: nodeType}, true
	case "function_definition":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "class_definition":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "decorated_definition":
		return e.pythonDecoratedDefinitionSpec(node)
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) pythonDecoratedDefinitionSpec(node *gotreesitter.Node) (treeSitterSymbolSpec, bool) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Type(e.lang) {
		case "function_definition":
			name := e.nodeName(child)
			if name == "" {
				return treeSitterSymbolSpec{}, false
			}
			return treeSitterSymbolSpec{kind: "function", name: name, detail: "decorated_definition"}, true
		case "class_definition":
			name := e.nodeName(child)
			if name == "" {
				return treeSitterSymbolSpec{}, false
			}
			return treeSitterSymbolSpec{kind: "class", name: name, detail: "decorated_definition"}, true
		}
	}
	return treeSitterSymbolSpec{}, false
}

func (e treeSitterExtractor) javaSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "package_declaration":
		name := e.nodeName(node)
		if name == "" {
			name = firstTreeSitterLine(node.Text(e.source))
		}
		return treeSitterSymbolSpec{kind: "package", name: name, detail: nodeType}, true
	case "import_declaration":
		name := e.nodeName(node)
		if name == "" {
			name = firstTreeSitterLine(node.Text(e.source))
		}
		return treeSitterSymbolSpec{kind: "import", name: name, detail: nodeType}, true
	case "class_declaration":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "interface_declaration":
		return treeSitterSymbolSpec{kind: "interface", name: e.nodeName(node), detail: nodeType}, true
	case "enum_declaration":
		return treeSitterSymbolSpec{kind: "enum", name: e.nodeName(node), detail: nodeType}, true
	case "record_declaration":
		return treeSitterSymbolSpec{kind: "record", name: e.nodeName(node), detail: nodeType}, true
	case "annotation_type_declaration":
		return treeSitterSymbolSpec{kind: "annotation", name: e.nodeName(node), detail: nodeType}, true
	case "method_declaration":
		return treeSitterSymbolSpec{kind: "method", name: e.nodeName(node), detail: nodeType}, true
	case "constructor_declaration":
		return treeSitterSymbolSpec{kind: "constructor", name: e.nodeName(node), detail: nodeType}, true
	case "field_declaration":
		name := e.javaFieldName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "field", name: name, detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) rustSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "use_declaration":
		return treeSitterSymbolSpec{kind: "import", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "mod_item":
		return treeSitterSymbolSpec{kind: "module", name: e.nodeName(node), detail: nodeType}, true
	case "struct_item":
		return treeSitterSymbolSpec{kind: "struct", name: e.nodeName(node), detail: nodeType}, true
	case "enum_item":
		return treeSitterSymbolSpec{kind: "enum", name: e.nodeName(node), detail: nodeType}, true
	case "trait_item":
		return treeSitterSymbolSpec{kind: "trait", name: e.nodeName(node), detail: nodeType}, true
	case "impl_item":
		return treeSitterSymbolSpec{kind: "impl", name: e.rustImplName(node), detail: nodeType}, true
	case "function_item":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "function_signature_item":
		return treeSitterSymbolSpec{kind: "method", name: e.nodeName(node), detail: nodeType}, true
	case "type_item":
		return treeSitterSymbolSpec{kind: "type", name: e.nodeName(node), detail: nodeType}, true
	case "const_item":
		return treeSitterSymbolSpec{kind: "constant", name: e.nodeName(node), detail: nodeType}, true
	case "static_item":
		return treeSitterSymbolSpec{kind: "static", name: e.nodeName(node), detail: nodeType}, true
	case "macro_definition":
		return treeSitterSymbolSpec{kind: "macro", name: e.nodeName(node), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) cFamilySymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "preproc_include":
		return treeSitterSymbolSpec{kind: "import", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "namespace_definition":
		return treeSitterSymbolSpec{kind: "namespace", name: e.nodeName(node), detail: nodeType}, true
	case "class_specifier":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "struct_specifier":
		return treeSitterSymbolSpec{kind: "struct", name: e.nodeName(node), detail: nodeType}, true
	case "union_specifier":
		return treeSitterSymbolSpec{kind: "union", name: e.nodeName(node), detail: nodeType}, true
	case "enum_specifier":
		return treeSitterSymbolSpec{kind: "enum", name: e.nodeName(node), detail: nodeType}, true
	case "type_definition":
		return treeSitterSymbolSpec{kind: "type", name: e.cDeclaratorName(node), detail: nodeType}, true
	case "function_definition":
		name := e.cDeclaratorName(node)
		kind := "function"
		if strings.Contains(name, "::") {
			kind = "method"
		}
		return treeSitterSymbolSpec{kind: kind, name: name, detail: nodeType}, true
	case "declaration":
		name := e.cDeclaratorName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		if treeSitterNodeHasDescendantType(node, e.lang, "function_declarator") {
			kind := "function"
			if strings.Contains(name, "::") {
				kind = "method"
			}
			return treeSitterSymbolSpec{kind: kind, name: name, detail: nodeType}, true
		}
		if cFamilyHasMultipleDeclarators(node.Text(e.source)) {
			return treeSitterSymbolSpec{}, false
		}
		if treeSitterNamedChildCountByType(node, e.lang, "init_declarator") > 1 {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "variable", name: name, detail: nodeType}, true
	case "field_declaration":
		name := e.cDeclaratorName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		if treeSitterNodeHasDescendantType(node, e.lang, "function_declarator") {
			return treeSitterSymbolSpec{kind: "method", name: name, detail: nodeType}, true
		}
		if cFamilyHasMultipleDeclarators(node.Text(e.source)) {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "field", name: name, detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) csharpSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "using_directive":
		return treeSitterSymbolSpec{kind: "import", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "namespace_declaration":
		return treeSitterSymbolSpec{kind: "namespace", name: e.nodeName(node), detail: nodeType}, true
	case "class_declaration":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "interface_declaration":
		return treeSitterSymbolSpec{kind: "interface", name: e.nodeName(node), detail: nodeType}, true
	case "struct_declaration":
		return treeSitterSymbolSpec{kind: "struct", name: e.nodeName(node), detail: nodeType}, true
	case "record_declaration":
		return treeSitterSymbolSpec{kind: "record", name: e.nodeName(node), detail: nodeType}, true
	case "enum_declaration":
		return treeSitterSymbolSpec{kind: "enum", name: e.nodeName(node), detail: nodeType}, true
	case "method_declaration":
		return treeSitterSymbolSpec{kind: "method", name: e.nodeName(node), detail: nodeType}, true
	case "constructor_declaration":
		return treeSitterSymbolSpec{kind: "constructor", name: e.nodeName(node), detail: nodeType}, true
	case "property_declaration":
		return treeSitterSymbolSpec{kind: "property", name: e.nodeName(node), detail: nodeType}, true
	case "field_declaration":
		name := e.csharpFieldName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "field", name: name, detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) rubySymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "call":
		line := firstTreeSitterLine(node.Text(e.source))
		if name := rubyImportName(line); name != "" {
			return treeSitterSymbolSpec{kind: "import", name: name, detail: nodeType}, true
		}
		return treeSitterSymbolSpec{}, false
	case "module":
		return treeSitterSymbolSpec{kind: "module", name: e.nodeName(node), detail: nodeType}, true
	case "class":
		return treeSitterSymbolSpec{kind: "class", name: e.nodeName(node), detail: nodeType}, true
	case "method", "singleton_method":
		return treeSitterSymbolSpec{kind: "method", name: e.nodeName(node), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) kotlinSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "package_header":
		return treeSitterSymbolSpec{kind: "package", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "import_header":
		return treeSitterSymbolSpec{kind: "import", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "class_declaration":
		return treeSitterSymbolSpec{kind: e.kotlinClassLikeKind(node), name: e.nodeName(node), detail: nodeType}, true
	case "object_declaration":
		return treeSitterSymbolSpec{kind: e.kotlinClassLikeKind(node), name: e.nodeName(node), detail: nodeType}, true
	case "companion_object":
		return treeSitterSymbolSpec{kind: "companion_object", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "type_alias", "typealias", "type_alias_declaration", "typealias_declaration":
		return treeSitterSymbolSpec{kind: "typealias", name: e.nodeName(node), detail: nodeType}, true
	case "function_declaration":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "property_declaration":
		return treeSitterSymbolSpec{kind: "property", name: e.nodeName(node), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) swiftSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "import_declaration":
		return treeSitterSymbolSpec{kind: "import", name: e.nodeNameOrLine(node), detail: nodeType}, true
	case "protocol_declaration":
		return treeSitterSymbolSpec{kind: "protocol", name: e.nodeName(node), detail: nodeType}, true
	case "class_declaration":
		return treeSitterSymbolSpec{kind: e.swiftClassLikeKind(node), name: e.nodeName(node), detail: nodeType}, true
	case "function_declaration", "protocol_function_declaration":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "property_declaration":
		return treeSitterSymbolSpec{kind: "property", name: e.nodeName(node), detail: nodeType}, true
	case "enum_entry":
		return treeSitterSymbolSpec{kind: "enum_case", name: e.nodeName(node), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) bashSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "command":
		line := strings.TrimSpace(firstTreeSitterLine(node.Text(e.source)))
		if name := bashSourceImportName(line); name != "" {
			return treeSitterSymbolSpec{kind: "import", name: name, detail: nodeType}, true
		}
		return treeSitterSymbolSpec{}, false
	case "function_definition":
		return treeSitterSymbolSpec{kind: "function", name: e.nodeName(node), detail: nodeType}, true
	case "variable_assignment":
		return treeSitterSymbolSpec{kind: "variable", name: e.nodeName(node), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) jsonSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "document":
		return treeSitterSymbolSpec{kind: "document", name: "$", detail: nodeType}, true
	case "object":
		return treeSitterSymbolSpec{kind: "object", name: "object", detail: nodeType}, true
	case "array":
		return treeSitterSymbolSpec{kind: "array", name: "[]", detail: nodeType}, true
	case "pair":
		name := e.nodeName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "property", name: name, detail: nodeType}, true
	case "string", "number", "true", "false", "null":
		return treeSitterSymbolSpec{kind: "value", name: e.containerName(node, nodeType), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) yamlSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "stream":
		return treeSitterSymbolSpec{kind: "stream", name: "stream", detail: nodeType}, true
	case "document":
		return treeSitterSymbolSpec{kind: "document", name: "document", detail: nodeType}, true
	case "block_mapping", "flow_mapping":
		return treeSitterSymbolSpec{kind: "mapping", name: "mapping", detail: nodeType}, true
	case "block_sequence", "flow_sequence":
		return treeSitterSymbolSpec{kind: "sequence", name: "[]", detail: nodeType}, true
	case "block_mapping_pair", "flow_pair":
		name := e.nodeName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "key", name: name, detail: nodeType}, true
	case "plain_scalar", "string_scalar", "double_quote_scalar", "single_quote_scalar", "integer_scalar", "float_scalar", "boolean_scalar", "null_scalar":
		if e.yamlScalarShouldBeSkipped(node) {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "value", name: e.containerName(node, nodeType), detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) yamlScalarShouldBeSkipped(node *gotreesitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	if isYAMLScalarNodeType(parent.Type(e.lang)) {
		return true
	}
	parentType := parent.Type(e.lang)
	if parentType != "block_mapping_pair" && parentType != "flow_pair" {
		return false
	}
	key := parent.ChildByFieldName("key", e.lang)
	return key != nil && key.StartByte() == node.StartByte() && key.EndByte() == node.EndByte()
}

func isYAMLScalarNodeType(nodeType string) bool {
	switch nodeType {
	case "plain_scalar", "string_scalar", "double_quote_scalar", "single_quote_scalar", "integer_scalar", "float_scalar", "boolean_scalar", "null_scalar":
		return true
	default:
		return false
	}
}

func (e treeSitterExtractor) svelteSymbolSpec(node *gotreesitter.Node, nodeType string) (treeSitterSymbolSpec, bool) {
	switch nodeType {
	case "script_element":
		text := node.Text(e.source)
		if strings.Contains(text, "context=\"module\"") || strings.Contains(text, "context='module'") || strings.Contains(text, "<script module") {
			return treeSitterSymbolSpec{kind: "module_script", name: "module_script", detail: nodeType}, true
		}
		return treeSitterSymbolSpec{kind: "script", name: "script", detail: nodeType}, true
	case "style_element":
		return treeSitterSymbolSpec{kind: "style", name: "style", detail: nodeType}, true
	case "fragment":
		return treeSitterSymbolSpec{kind: "markup", name: "markup", detail: nodeType}, true
	case "element", "component":
		name := e.nodeName(node)
		if name == "" {
			return treeSitterSymbolSpec{}, false
		}
		return treeSitterSymbolSpec{kind: "element", name: name, detail: nodeType}, true
	default:
		return treeSitterSymbolSpec{}, false
	}
}

func (e treeSitterExtractor) outlineItem(node *gotreesitter.Node, spec treeSitterSymbolSpec, parentPath []string, depth int, parserClean bool) OutlineItem {
	if spec.name == "" {
		spec.name = unnamedTreeSitterName(node, e.lang)
	}
	rangeNode := e.outlineRangeNode(node, spec)
	r := lineRangeForTreeSitterNode(e.source, rangeNode)
	byteRange := SourceByteRange{StartByte: int(rangeNode.StartByte()), EndByteExclusive: int(rangeNode.EndByte())}
	symbolPath := append(append([]string(nil), parentPath...), spec.name)
	displayName := e.displaySymbolName(spec.kind, spec.name, symbolPath)
	if displayName != "" {
		spec.name = displayName
		if e.language != outlineLanguageJSON && e.language != outlineLanguageYAML {
			symbolPath[len(symbolPath)-1] = displayName
		}
	}
	publicSymbolPath := append([]string(nil), symbolPath...)
	publicParentPath := append([]string(nil), parentPath...)
	if e.language == outlineLanguageJSON || e.language == outlineLanguageYAML {
		publicSymbolPath = publicConfigSymbolPath(symbolPath)
		publicParentPath = publicConfigSymbolPath(parentPath)
	}
	wholeLineRange := treeSitterNodeWholeLineSafe(e.source, rangeNode)
	delimiterSafe := e.treeSitterSymbolDelimiterSafeForNode(rangeNode, spec.kind)
	writeSafe := parserClean && wholeLineRange && delimiterSafe
	refusalReason := ""
	if !wholeLineRange {
		refusalReason = "symbol_range_not_write_safe"
	}
	if wholeLineRange && !delimiterSafe {
		refusalReason = "symbol_range_not_write_safe"
	}
	if !parserClean {
		refusalReason = "symbol_parser_not_write_safe"
	}
	metadata := map[string]string{"node_type": spec.detail}
	if e.language == outlineLanguagePython && spec.detail == "decorated_definition" {
		metadata["decorated"] = "true"
	}
	if e.language == outlineLanguagePython && depth > 0 {
		metadata["nested"] = "true"
	}
	return exactOutlineItemWithSelector(e.info, e.language, OutlineItem{
		ID:            fmt.Sprintf("%s:%s:%s:%d", e.language, spec.kind, sanitizeOutlineIDPart(spec.name), r.StartLine),
		Kind:          spec.kind,
		Name:          spec.name,
		Detail:        spec.detail,
		Path:          publicSymbolPath,
		EnclosingPath: publicParentPath,
		Range:         r,
		Depth:         depth,
		Metadata:      metadata,
	}, byteRange, wholeLineRange, writeSafe, refusalReason)
}

func (e treeSitterExtractor) outlineRangeNode(node *gotreesitter.Node, spec treeSitterSymbolSpec) *gotreesitter.Node {
	if node == nil || !isJSLikeLanguage(e.language) {
		return node
	}
	nodeType := node.Type(e.lang)
	switch nodeType {
	case "function_declaration", "generator_function_declaration", "class_declaration", "interface_declaration", "type_alias_declaration":
		if parent := node.Parent(); parent != nil && parent.Type(e.lang) == "export_statement" && !e.jsLikeExportHasSource(parent) {
			return parent
		}
	case "variable_declarator":
		parent := node.Parent()
		if parent == nil {
			return node
		}
		parentType := parent.Type(e.lang)
		if parentType != "lexical_declaration" && parentType != "variable_declaration" {
			return node
		}
		if treeSitterNamedChildCountByType(parent, e.lang, "variable_declarator") != 1 {
			return node
		}
		if exportParent := parent.Parent(); exportParent != nil && exportParent.Type(e.lang) == "export_statement" && !e.jsLikeExportHasSource(exportParent) {
			return exportParent
		}
		return parent
	}
	return node
}

func (e treeSitterExtractor) displaySymbolName(kind, name string, symbolPath []string) string {
	switch e.language {
	case outlineLanguageJSON:
		return jsonDisplayPath(kind, name, symbolPath)
	case outlineLanguageYAML:
		return yamlDisplayPath(kind, name, symbolPath)
	default:
		return name
	}
}

func jsonDisplayPath(kind, name string, symbolPath []string) string {
	segments := configPathSegments(symbolPath)
	if len(segments) == 0 {
		return "document"
	}
	if kind == "value" && len(segments) > 1 && !configPathSegmentIsIndex(segments[len(segments)-1]) {
		segments = segments[:len(segments)-1]
	}
	return configDisplayPath("document", segments)
}

func yamlDisplayPath(kind, name string, symbolPath []string) string {
	segments := configPathSegments(symbolPath)
	if len(segments) == 0 {
		return "document"
	}
	if kind == "value" && len(segments) > 1 && !configPathSegmentIsIndex(segments[len(segments)-1]) {
		segments = segments[:len(segments)-1]
	}
	return configDisplayPath("document", segments)
}

func configDisplayPath(root string, segments []string) string {
	path := root
	for _, segment := range segments {
		if index, ok := configPathIndex(segment); ok {
			path += fmt.Sprintf("[%d]", index)
		} else {
			path += configPathSegmentSuffix(segment)
		}
	}
	return path
}

func configPathSegmentSuffix(segment string) string {
	if isSimpleConfigPathKey(segment) {
		return "." + segment
	}
	data, err := json.Marshal(segment)
	if err != nil {
		return "." + segment
	}
	return "[" + string(data) + "]"
}

func isSimpleConfigPathKey(segment string) bool {
	if segment == "" {
		return false
	}
	for i, r := range segment {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func configPathSegments(symbolPath []string) []string {
	out := []string{}
	for _, segment := range symbolPath {
		switch {
		case segment == "", segment == "$", segment == "{", segment == "}":
			continue
		case strings.HasPrefix(segment, configPathMarkerPrefix) && !strings.HasPrefix(segment, configPathIndexPrefix):
			continue
		case strings.HasPrefix(segment, configPathIndexPrefix):
			out = append(out, segment)
		default:
			out = append(out, segment)
		}
	}
	return out
}

func configRawPathName(language, kind, name string) string {
	if language != outlineLanguageJSON && language != outlineLanguageYAML {
		return name
	}
	switch kind {
	case "document", "object", "array", "stream", "mapping", "sequence":
		return configPathMarkerPrefix + kind
	default:
		return name
	}
}

func configIndexPathSegment(index int) string {
	return fmt.Sprintf("%s%d", configPathIndexPrefix, index)
}

func configPathIndex(segment string) (int, bool) {
	if !strings.HasPrefix(segment, configPathIndexPrefix) {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(segment, configPathIndexPrefix))
	if err != nil || index < 0 {
		return 0, false
	}
	return index, true
}

func configPathSegmentIsIndex(segment string) bool {
	_, ok := configPathIndex(segment)
	return ok
}

func publicConfigSymbolPath(symbolPath []string) []string {
	out := []string{}
	for _, segment := range symbolPath {
		switch {
		case segment == "", segment == "$", segment == "{", segment == "}":
			continue
		case strings.HasPrefix(segment, configPathMarkerPrefix) && !strings.HasPrefix(segment, configPathIndexPrefix):
			continue
		case strings.HasPrefix(segment, configPathIndexPrefix):
			if index, ok := configPathIndex(segment); ok {
				out = append(out, fmt.Sprintf("[%d]", index))
			}
		default:
			out = append(out, segment)
		}
	}
	return out
}

func treeSitterSymbolDelimiterSafe(language, kind string) bool {
	switch language {
	case outlineLanguageJSON, outlineLanguageYAML:
		return false
	case outlineLanguageJavaScript, outlineLanguageTypeScript, outlineLanguageTSX:
		switch kind {
		case "variable":
			return false
		}
	case outlineLanguageSvelte:
		return false
	}
	return true
}

func (e treeSitterExtractor) treeSitterSymbolDelimiterSafeForNode(node *gotreesitter.Node, kind string) bool {
	if isJSLikeLanguage(e.language) && node != nil {
		switch node.Type(e.lang) {
		case "variable_declarator":
			return false
		case "lexical_declaration", "variable_declaration", "export_statement":
			if e.jsLikeSingleVariableDeclarationRange(node) {
				return true
			}
		}
	}
	if kind == "field" {
		if e.language == outlineLanguageJava && treeSitterNamedChildCountByType(node, e.lang, "variable_declarator") > 1 {
			return false
		}
		if e.language == outlineLanguageCSharp && strings.Contains(firstTreeSitterLine(node.Text(e.source)), ",") {
			return false
		}
	}
	return treeSitterSymbolDelimiterSafe(e.language, kind)
}

func (e treeSitterExtractor) jsLikeSingleVariableDeclarationRange(node *gotreesitter.Node) bool {
	if node == nil {
		return false
	}
	switch node.Type(e.lang) {
	case "lexical_declaration", "variable_declaration":
		return treeSitterNamedChildCountByType(node, e.lang, "variable_declarator") == 1
	case "export_statement":
		for i := 0; i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child == nil {
				continue
			}
			switch child.Type(e.lang) {
			case "lexical_declaration", "variable_declaration":
				return treeSitterNamedChildCountByType(child, e.lang, "variable_declarator") == 1
			}
		}
		return false
	default:
		return false
	}
}

func treeSitterNamedDescendantCountByType(node *gotreesitter.Node, lang *gotreesitter.Language, nodeType string) int {
	if node == nil {
		return 0
	}
	count := 0
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		if child.Type(lang) == nodeType {
			count++
		}
		count += treeSitterNamedDescendantCountByType(child, lang, nodeType)
	}
	return count
}

func treeSitterNodeHasDescendantType(node *gotreesitter.Node, lang *gotreesitter.Language, nodeType string) bool {
	return treeSitterNamedDescendantCountByType(node, lang, nodeType) > 0
}

func outlineBoolPtr(value bool) *bool {
	return &value
}

func (e treeSitterExtractor) nodeNameOrLine(node *gotreesitter.Node) string {
	if name := e.nodeName(node); name != "" {
		return name
	}
	return firstTreeSitterLine(node.Text(e.source))
}

func (e treeSitterExtractor) nodeName(node *gotreesitter.Node) string {
	for _, field := range []string{"name", "key"} {
		if child := node.ChildByFieldName(field, e.lang); child != nil {
			name := normalizeTreeSitterName(child.Text(e.source))
			if field == "key" && (e.language == outlineLanguageJSON || e.language == outlineLanguageYAML) {
				name = normalizeConfigKeyName(child.Text(e.source))
			}
			if name != "" {
				return name
			}
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		childType := child.Type(e.lang)
		switch childType {
		case "identifier", "property_identifier", "type_identifier", "shorthand_property_identifier", "string", "string_scalar", "plain_scalar", "scoped_identifier", "qualified_identifier", "qualified_name", "namespace_identifier", "field_identifier", "simple_identifier", "constant", "variable_name", "word", "system_lib_string":
			if name := normalizeTreeSitterName(child.Text(e.source)); name != "" {
				return name
			}
		case "function_declarator", "parenthesized_declarator", "pointer_declarator", "reference_declarator", "init_declarator":
			if name := e.nodeName(child); name != "" {
				return name
			}
		}
	}
	return normalizeTreeSitterName(firstTreeSitterLine(node.Text(e.source)))
}

func (e treeSitterExtractor) rustImplName(node *gotreesitter.Node) string {
	typ := ""
	if typeNode := node.ChildByFieldName("type", e.lang); typeNode != nil {
		typ = e.nodeName(typeNode)
	}
	if traitNode := node.ChildByFieldName("trait", e.lang); traitNode != nil {
		trait := e.nodeName(traitNode)
		if trait != "" && typ != "" {
			return trait + " for " + typ
		}
		if trait != "" {
			return trait
		}
	}
	if typ != "" {
		return typ
	}
	return e.nodeNameOrLine(node)
}

func (e treeSitterExtractor) cDeclaratorName(node *gotreesitter.Node) string {
	for _, field := range []string{"declarator", "name"} {
		if child := node.ChildByFieldName(field, e.lang); child != nil {
			if name := e.nodeName(child); name != "" {
				return name
			}
		}
	}
	return e.nodeName(node)
}

func (e treeSitterExtractor) csharpFieldName(node *gotreesitter.Node) string {
	if node == nil {
		return ""
	}
	if name := e.csharpFieldDeclaratorName(node); name != "" {
		return name
	}
	return csharpFieldNameFromLine(firstTreeSitterLine(node.Text(e.source)))
}

func (e treeSitterExtractor) csharpFieldDeclaratorName(node *gotreesitter.Node) string {
	if node == nil {
		return ""
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Type(e.lang) == "variable_declarator" {
			if name := e.nodeName(child); name != "" {
				return name
			}
		}
		if name := e.csharpFieldDeclaratorName(child); name != "" {
			return name
		}
	}
	return ""
}

func csharpFieldNameFromLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(line, ";")
	if idx := strings.Index(line, "="); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.LastIndex(line, ","); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return ""
	}
	return normalizeTreeSitterName(parts[len(parts)-1])
}

func (e treeSitterExtractor) kotlinClassLikeKind(node *gotreesitter.Node) string {
	return classLikeKindFromText(node.Text(e.source), "class", map[string]string{
		"companion object": "companion_object",
		"enum":             "enum",
		"interface":        "interface",
		"object":           "object",
		"class":            "class",
	})
}

func (e treeSitterExtractor) swiftClassLikeKind(node *gotreesitter.Node) string {
	return classLikeKindFromText(node.Text(e.source), "class", map[string]string{
		"struct": "struct",
		"enum":   "enum",
		"actor":  "actor",
		"class":  "class",
	})
}

func classLikeKindFromText(text string, fallback string, keywords map[string]string) string {
	tokens := declarationTokens(text)
	for i, token := range tokens {
		if i+1 < len(tokens) {
			if kind, ok := keywords[token+" "+tokens[i+1]]; ok {
				return kind
			}
		}
		if kind, ok := keywords[token]; ok {
			return kind
		}
	}
	return fallback
}

func rubyImportName(line string) string {
	line = strings.TrimSpace(line)
	for _, keyword := range []string{"require", "require_relative", "load"} {
		if line == keyword {
			return keyword
		}
		if strings.HasPrefix(line, keyword+" ") || strings.HasPrefix(line, keyword+"(") {
			arg := strings.TrimSpace(strings.TrimPrefix(line, keyword))
			return importArgumentName(arg)
		}
	}
	return ""
}

func bashSourceImportName(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "source "):
		return importArgumentName(strings.TrimPrefix(line, "source"))
	case strings.HasPrefix(line, ". "):
		return importArgumentName(strings.TrimPrefix(line, "."))
	default:
		return ""
	}
}

func importArgumentName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "(")
	value = strings.TrimSuffix(value, ")")
	value = strings.TrimSpace(value)
	if idx := strings.IndexAny(value, " \t"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.Trim(value, "\"'")
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return value
}

func declarationTokens(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func cFamilyHasMultipleDeclarators(value string) bool {
	angleDepth := 0
	parenDepth := 0
	bracketDepth := 0
	braceDepth := 0
	for _, r := range value {
		switch r {
		case '<':
			angleDepth++
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ',':
			if angleDepth == 0 && parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return true
			}
		}
	}
	return false
}

func (e treeSitterExtractor) javaFieldName(node *gotreesitter.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil || child.Type(e.lang) != "variable_declarator" {
			continue
		}
		if name := e.nodeName(child); name != "" {
			return name
		}
	}
	return e.nodeName(node)
}

func (e treeSitterExtractor) containerName(node *gotreesitter.Node, fallback string) string {
	if name := e.nodeName(node); name != "" && name != fallback {
		return name
	}
	text := normalizeTreeSitterName(firstTreeSitterLine(node.Text(e.source)))
	if text != "" {
		clipped, _ := truncateDisplayPrefix(text, 40, "")
		return clipped
	}
	return fmt.Sprintf("%s@%d", fallback, node.StartPoint().Row+1)
}

func isPascalCaseName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	first := rune(name[0])
	return first >= 'A' && first <= 'Z'
}

func nodeContainsJSXLikeMarkup(text string) bool {
	return strings.Contains(text, "return <") ||
		strings.Contains(text, "return (<") ||
		strings.Contains(text, "(<") ||
		strings.Contains(text, "= <") ||
		strings.Contains(text, "=> <")
}

func normalizeTreeSitterName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'`")
	value = strings.TrimSuffix(value, ":")
	if strings.Contains(value, "\n") {
		value = firstTreeSitterLine(value)
	}
	return strings.TrimSpace(value)
}

func normalizeConfigKeyName(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "\n") {
		value = strings.TrimSpace(firstTreeSitterLine(value))
	}
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'' || first == '`') && last == first {
			return value[1 : len(value)-1]
		}
	}
	value = strings.TrimSuffix(value, ":")
	return strings.TrimSpace(value)
}

func filterConfigOutlineProfile(items []OutlineItem, options outlineOptions) ([]OutlineItem, int) {
	if options.outputProfile == outlineProfileFull {
		return items, 0
	}
	filtered, omitted := filterConfigOutlineItems(items, options)
	return filtered, omitted
}

func filterConfigOutlineItems(items []OutlineItem, options outlineOptions) ([]OutlineItem, int) {
	out := make([]OutlineItem, 0, len(items))
	omitted := 0
	for _, item := range items {
		if isConfigLeafOutlineItem(item) && !keepConfigLeafOutlineItem(item, options) {
			omitted++
			continue
		}
		children, childOmitted := filterConfigOutlineItems(item.Children, options)
		item.Children = children
		omitted += childOmitted
		if configAgentProfileCompactActive(options, "") && isSyntheticConfigWrapperItem(item) {
			out = append(out, item.Children...)
			continue
		}
		out = append(out, item)
	}
	return out, omitted
}

func isConfigLeafOutlineItem(item OutlineItem) bool {
	return item.Kind == "value"
}

func keepConfigLeafOutlineItem(item OutlineItem, options outlineOptions) bool {
	if outlineKindsInclude(options.kinds, item.Kind) {
		return true
	}
	if options.lineWindow != nil && rangesOverlap(item.Range, *options.lineWindow) {
		return true
	}
	if options.nameContains != "" {
		needle := strings.ToLower(options.nameContains)
		if strings.Contains(strings.ToLower(item.Name), needle) || strings.Contains(strings.ToLower(strings.Join(item.Path, ".")), needle) {
			return true
		}
	}
	return false
}

func outlineKindsInclude(kinds []string, kind string) bool {
	for _, candidate := range kinds {
		if strings.EqualFold(strings.TrimSpace(candidate), kind) {
			return true
		}
	}
	return false
}

func configAgentProfileCompactActive(options outlineOptions, language string) bool {
	if language != "" && language != outlineLanguageJSON && language != outlineLanguageYAML {
		return false
	}
	return options.outputProfile != outlineProfileFull && !outlineHasExplicitDetailRequest(options)
}

func outlineHasExplicitDetailRequest(options outlineOptions) bool {
	return options.lineWindow != nil ||
		options.enclosingLine != nil ||
		strings.TrimSpace(options.nameContains) != "" ||
		len(options.kinds) > 0
}

func isSyntheticConfigWrapperItem(item OutlineItem) bool {
	switch item.Kind {
	case "object", "array", "mapping", "sequence":
	default:
		return false
	}
	switch {
	case strings.HasSuffix(item.Name, ".object"):
		return true
	case strings.HasSuffix(item.Name, ".mapping"):
		return true
	case strings.HasSuffix(item.Name, ".sequence"):
		return true
	case strings.HasSuffix(item.Name, "[\"[]\"]"):
		return true
	default:
		return false
	}
}

func compactConfigOutlineSections(items []OutlineItem) []OutlineItem {
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		if item.Kind != "document" && item.Kind != "stream" {
			continue
		}
		item.Children = nil
		out = append(out, item)
	}
	return out
}

func compactConfigOutlinePresentation(items []OutlineItem) []OutlineItem {
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		item.Children = compactConfigOutlinePresentation(item.Children)
		item.WholeLineRange = nil
		item.WriteSafe = nil
		item.RefusalReason = ""
		out = append(out, item)
	}
	return out
}

func filterJSLikeOutlineProfile(items []OutlineItem, options outlineOptions) []OutlineItem {
	if options.outputProfile == outlineProfileFull || outlineHasExplicitDetailRequest(options) {
		return items
	}
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		item.Children = filterJSLikeOutlineProfile(item.Children, options)
		if item.Depth > 0 && item.Metadata["node_type"] == "variable_declarator" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterCFamilyOutlineProfile(items []OutlineItem, options outlineOptions) []OutlineItem {
	if options.outputProfile == outlineProfileFull || outlineHasExplicitDetailRequest(options) {
		return items
	}
	return filterCFamilyOutlineItems(items, false, options)
}

func filterCFamilyOutlineItems(items []OutlineItem, insideCallable bool, options outlineOptions) []OutlineItem {
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		itemInsideCallable := insideCallable || item.Kind == "function" || item.Kind == "method" || item.Kind == "constructor"
		item.Children = filterCFamilyOutlineItems(item.Children, itemInsideCallable, options)
		if item.Kind == "variable" && insideCallable {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterBashOutlineProfile(items []OutlineItem, options outlineOptions) []OutlineItem {
	if options.outputProfile == outlineProfileFull || outlineHasExplicitDetailRequest(options) {
		return items
	}
	seenTopLevelVariables := map[string]bool{}
	return filterBashOutlineItems(items, options, seenTopLevelVariables)
}

func filterBashOutlineItems(items []OutlineItem, options outlineOptions, seenTopLevelVariables map[string]bool) []OutlineItem {
	out := make([]OutlineItem, 0, len(items))
	for _, item := range items {
		item.Children = filterBashOutlineItems(item.Children, options, seenTopLevelVariables)
		if item.Kind == "variable" {
			if item.Depth > 0 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(item.Name))
			if key != "" && seenTopLevelVariables[key] {
				continue
			}
			seenTopLevelVariables[key] = true
		}
		out = append(out, item)
	}
	return out
}

func firstTreeSitterLine(value string) string {
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return value[:idx]
	}
	return value
}

func unnamedTreeSitterName(node *gotreesitter.Node, lang *gotreesitter.Language) string {
	return fmt.Sprintf("%s@%d", node.Type(lang), node.StartPoint().Row+1)
}

func lineRangeForTreeSitterNode(source []byte, node *gotreesitter.Node) SourceLineRange {
	start := oneBasedLineForByte(source, node.StartByte())
	endByte := node.EndByte()
	if endByte > 0 {
		endByte--
	}
	end := oneBasedLineForByte(source, endByte)
	if end < start {
		end = start
	}
	return SourceLineRange{StartLine: int(start), EndLine: int(end)}
}

func firstTreeSitterErrorLine(node *gotreesitter.Node) int {
	if node == nil || !node.HasError() {
		return 0
	}
	if node.IsError() {
		return int(node.StartPoint().Row) + 1
	}
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil || !child.HasError() {
			continue
		}
		if line := firstTreeSitterErrorLine(child); line > 0 {
			return line
		}
	}
	return int(node.StartPoint().Row) + 1
}

func oneBasedLineForByte(source []byte, offset uint32) uint32 {
	if offset > uint32(len(source)) {
		offset = uint32(len(source))
	}
	var line uint32 = 1
	for i := uint32(0); i < offset; i++ {
		if source[i] == '\n' {
			line++
		}
	}
	return line
}

func treeSitterNodeWholeLineSafe(source []byte, node *gotreesitter.Node) bool {
	if node.StartByte() >= node.EndByte() || int(node.EndByte()) > len(source) {
		return false
	}
	start := node.StartByte()
	for start > 0 && source[start-1] != '\n' {
		if source[start-1] != ' ' && source[start-1] != '\t' && source[start-1] != '\r' {
			return false
		}
		start--
	}
	end := node.EndByte()
	for end < uint32(len(source)) && source[end] != '\n' {
		if source[end] != ' ' && source[end] != '\t' && source[end] != '\r' {
			return false
		}
		end++
	}
	return true
}
