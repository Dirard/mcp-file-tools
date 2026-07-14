package codeparse

import (
	"fmt"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/Dirard/mcp-file-tools/internal/api"
	"github.com/Dirard/mcp-file-tools/internal/navmodel"
)

func parseTreeSitter(language api.Language, source []byte) parseOutput {
	grammar := treeSitterGrammar(language)
	if grammar == nil {
		return parseOutput{fatal: true}
	}
	parser := gotreesitter.NewParser(grammar)
	tree, err := parser.Parse(source)
	if err != nil || tree == nil {
		return parseOutput{fatal: true}
	}
	defer tree.Release()
	root := tree.RootNode()
	if root == nil {
		return parseOutput{fatal: true}
	}

	extractor := treeExtractor{language: language, grammar: grammar, source: source}
	output := parseOutput{records: make([]rawRecord, 0, 32)}
	extractor.walk(root, "", 0, nil, &output.records)
	if root.HasError() {
		output.errorRanges = treeErrorRanges(root)
		if len(output.errorRanges) == 0 {
			return parseOutput{fatal: true}
		}
	}
	return output
}

func treeSitterGrammar(language api.Language) *gotreesitter.Language {
	switch language {
	case api.LanguageJavaScript, api.LanguageJSX:
		return grammars.JavascriptLanguage()
	case api.LanguageTypeScript:
		return grammars.TypescriptLanguage()
	case api.LanguageTSX:
		return grammars.TsxLanguage()
	case api.LanguagePython:
		return grammars.PythonLanguage()
	case api.LanguageJava:
		return grammars.JavaLanguage()
	case api.LanguageRust:
		return grammars.RustLanguage()
	case api.LanguageC:
		return grammars.CLanguage()
	case api.LanguageCPP:
		return grammars.CppLanguage()
	case api.LanguageCSharp:
		return grammars.CSharpLanguage()
	case api.LanguageRuby:
		return grammars.RubyLanguage()
	case api.LanguageKotlin:
		return grammars.KotlinLanguage()
	case api.LanguageSwift:
		return grammars.SwiftLanguage()
	case api.LanguageBash:
		return grammars.BashLanguage()
	case api.LanguageJSON:
		return grammars.JsonLanguage()
	case api.LanguageYAML:
		return grammars.YamlLanguage()
	case api.LanguageSvelte:
		return grammars.SvelteLanguage()
	default:
		return nil
	}
}

type treeExtractor struct {
	language api.Language
	grammar  *gotreesitter.Language
	source   []byte
}

type treeSymbolSpec struct {
	kind string
	name string
}

func (extractor treeExtractor) walk(node *gotreesitter.Node, parentKind string, depth uint16, configPath []string, records *[]rawRecord) {
	if node == nil {
		return
	}
	nodeType := node.Type(extractor.grammar)
	nextParent := parentKind
	nextDepth := depth
	nextConfigPath := configPath
	mappedKind := ""
	if spec, ok := extractor.symbolSpec(node, nodeType); ok {
		spec = extractor.refineSpec(node, spec, parentKind)
		if extractor.language == api.LanguageJSON || extractor.language == api.LanguageYAML {
			spec, nextConfigPath = configureTreeSpec(spec, configPath)
		}
		if spec.name == "" {
			spec.name = fmt.Sprintf("%s@%d", nodeType, node.StartPoint().Row+1)
		}
		*records = append(*records, rawRecord{
			kind:      spec.kind,
			lineRange: treeLineRange(node),
			depth:     depth,
			name:      spec.name,
		})
		nextParent = spec.kind
		mappedKind = spec.kind
		if spec.kind != "document" && spec.kind != "stream" {
			nextDepth++
		}
	}

	indexedChild := 0
	for index := 0; index < node.ChildCount(); index++ {
		child := node.Child(index)
		if child == nil || !child.IsNamed() {
			continue
		}
		if nodeType == "decorated_definition" {
			childType := child.Type(extractor.grammar)
			if childType == "function_definition" || childType == "class_definition" {
				extractor.walkChildren(child, nextParent, nextDepth, nextConfigPath, records)
				continue
			}
		}
		childConfigPath := nextConfigPath
		if mappedKind == "array" || mappedKind == "sequence" {
			childConfigPath = appendConfigPath(nextConfigPath, fmt.Sprintf("[%d]", indexedChild))
			indexedChild++
		}
		extractor.walk(child, nextParent, nextDepth, childConfigPath, records)
	}
}

func (extractor treeExtractor) walkChildren(node *gotreesitter.Node, parentKind string, depth uint16, configPath []string, records *[]rawRecord) {
	for index := 0; index < node.ChildCount(); index++ {
		child := node.Child(index)
		if child != nil && child.IsNamed() {
			extractor.walk(child, parentKind, depth, configPath, records)
		}
	}
}

func (extractor treeExtractor) symbolSpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	switch extractor.language {
	case api.LanguageJavaScript, api.LanguageJSX, api.LanguageTypeScript, api.LanguageTSX:
		return extractor.jsLikeSpec(node, nodeType)
	case api.LanguagePython:
		if nodeType == "decorated_definition" {
			return extractor.decoratedPythonSpec(node)
		}
	case api.LanguageC, api.LanguageCPP:
		return extractor.cFamilySpec(node, nodeType)
	case api.LanguageRuby:
		if nodeType == "call" {
			if name := rubyImportName(firstLine(node.Text(extractor.source))); name != "" {
				return treeSymbolSpec{kind: "import", name: name}, true
			}
			return treeSymbolSpec{}, false
		}
	case api.LanguageBash:
		if nodeType == "command" {
			if name := bashImportName(firstLine(node.Text(extractor.source))); name != "" {
				return treeSymbolSpec{kind: "import", name: name}, true
			}
			return treeSymbolSpec{}, false
		}
	case api.LanguageJSON:
		return extractor.jsonSpec(node, nodeType)
	case api.LanguageYAML:
		return extractor.yamlSpec(node, nodeType)
	case api.LanguageSvelte:
		return extractor.svelteSpec(node, nodeType)
	}

	kind, ok := simpleTreeKind(extractor.language, nodeType)
	if !ok {
		return treeSymbolSpec{}, false
	}
	name := extractor.nodeName(node)
	if kind == "import" || kind == "package" {
		name = extractor.statementName(node, kind)
	}
	if extractor.language == api.LanguageJava && nodeType == "field_declaration" {
		name = extractor.descendantName(node, "variable_declarator")
	}
	if extractor.language == api.LanguageCSharp && nodeType == "field_declaration" {
		name = extractor.descendantName(node, "variable_declarator")
	}
	if extractor.language == api.LanguageKotlin && (nodeType == "class_declaration" || nodeType == "object_declaration") {
		kind = kotlinClassKind(node.Text(extractor.source))
	}
	if extractor.language == api.LanguageSwift && nodeType == "class_declaration" {
		kind = swiftClassKind(node.Text(extractor.source))
	}
	return treeSymbolSpec{kind: kind, name: name}, name != ""
}

func (extractor treeExtractor) jsLikeSpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	switch nodeType {
	case "import_statement":
		return treeSymbolSpec{kind: "import", name: extractor.sourceNameOrLine(node)}, true
	case "export_statement":
		if source := node.ChildByFieldName("source", extractor.grammar); source != nil {
			return treeSymbolSpec{kind: "re_export", name: normalizeTreeName(source.Text(extractor.source))}, true
		}
		return treeSymbolSpec{}, false
	}
	kind, ok := simpleTreeKind(extractor.language, nodeType)
	if !ok {
		return treeSymbolSpec{}, false
	}
	name := extractor.nodeName(node)
	return treeSymbolSpec{kind: kind, name: name}, name != ""
}

func (extractor treeExtractor) decoratedPythonSpec(node *gotreesitter.Node) (treeSymbolSpec, bool) {
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child == nil {
			continue
		}
		switch child.Type(extractor.grammar) {
		case "function_definition":
			return treeSymbolSpec{kind: "function", name: extractor.nodeName(child)}, true
		case "class_definition":
			return treeSymbolSpec{kind: "class", name: extractor.nodeName(child)}, true
		}
	}
	return treeSymbolSpec{}, false
}

func (extractor treeExtractor) cFamilySpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	if kind, ok := simpleTreeKind(extractor.language, nodeType); ok && nodeType != "declaration" && nodeType != "field_declaration" && nodeType != "function_definition" && nodeType != "type_definition" {
		name := extractor.nodeName(node)
		if kind == "import" {
			name = extractor.statementName(node, kind)
		}
		return treeSymbolSpec{kind: kind, name: name}, name != ""
	}
	name := extractor.declaratorName(node)
	switch nodeType {
	case "type_definition":
		return treeSymbolSpec{kind: "type", name: name}, name != ""
	case "function_definition":
		kind := "function"
		if strings.Contains(name, "::") {
			kind = "method"
		}
		return treeSymbolSpec{kind: kind, name: name}, name != ""
	case "declaration", "field_declaration":
		if name == "" || hasTopLevelComma(node.Text(extractor.source)) {
			return treeSymbolSpec{}, false
		}
		if extractor.hasDescendantType(node, "function_declarator") {
			kind := "function"
			if nodeType == "field_declaration" || strings.Contains(name, "::") {
				kind = "method"
			}
			return treeSymbolSpec{kind: kind, name: name}, true
		}
		kind := "variable"
		if nodeType == "field_declaration" {
			kind = "field"
		}
		return treeSymbolSpec{kind: kind, name: name}, true
	default:
		return treeSymbolSpec{}, false
	}
}

func (extractor treeExtractor) jsonSpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	switch nodeType {
	case "document":
		return treeSymbolSpec{kind: "document", name: "$"}, true
	case "object":
		return treeSymbolSpec{kind: "object", name: "object"}, true
	case "array":
		return treeSymbolSpec{kind: "array", name: "[]"}, true
	case "pair":
		name := extractor.nodeName(node)
		return treeSymbolSpec{kind: "property", name: name}, name != ""
	case "string", "number", "true", "false", "null":
		return treeSymbolSpec{kind: "value", name: normalizeTreeName(node.Text(extractor.source))}, true
	default:
		return treeSymbolSpec{}, false
	}
}

func (extractor treeExtractor) yamlSpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	switch nodeType {
	case "stream":
		return treeSymbolSpec{kind: "stream", name: "stream"}, true
	case "document":
		return treeSymbolSpec{kind: "document", name: "document"}, true
	case "block_mapping", "flow_mapping":
		return treeSymbolSpec{kind: "mapping", name: "mapping"}, true
	case "block_sequence", "flow_sequence":
		return treeSymbolSpec{kind: "sequence", name: "[]"}, true
	case "block_mapping_pair", "flow_pair":
		name := extractor.nodeName(node)
		return treeSymbolSpec{kind: "key", name: name}, name != ""
	case "plain_scalar", "string_scalar", "double_quote_scalar", "single_quote_scalar", "integer_scalar", "float_scalar", "boolean_scalar", "null_scalar":
		return treeSymbolSpec{kind: "value", name: normalizeTreeName(node.Text(extractor.source))}, true
	default:
		return treeSymbolSpec{}, false
	}
}

func (extractor treeExtractor) svelteSpec(node *gotreesitter.Node, nodeType string) (treeSymbolSpec, bool) {
	switch nodeType {
	case "script_element":
		text := node.Text(extractor.source)
		if strings.Contains(text, `context="module"`) || strings.Contains(text, "context='module'") || strings.Contains(text, "<script module") {
			return treeSymbolSpec{kind: "module_script", name: "module_script"}, true
		}
		return treeSymbolSpec{kind: "script", name: "script"}, true
	case "style_element":
		return treeSymbolSpec{kind: "style", name: "style"}, true
	case "fragment":
		return treeSymbolSpec{kind: "markup", name: "markup"}, true
	case "element", "component":
		name := extractor.nodeName(node)
		return treeSymbolSpec{kind: "element", name: name}, name != ""
	default:
		return treeSymbolSpec{}, false
	}
}

func configureTreeSpec(spec treeSymbolSpec, path []string) (treeSymbolSpec, []string) {
	nextPath := path
	switch spec.kind {
	case "property", "key":
		nextPath = appendConfigPath(path, spec.name)
		spec.name = configPathName(nextPath)
	case "object", "mapping", "array", "sequence", "value":
		spec.name = configPathName(path)
		if spec.name == "" {
			spec.name = "$"
		}
	}
	return spec, nextPath
}

func appendConfigPath(path []string, segment string) []string {
	result := make([]string, len(path), len(path)+1)
	copy(result, path)
	return append(result, segment)
}

func configPathName(path []string) string {
	var builder strings.Builder
	for _, segment := range path {
		if segment == "" {
			continue
		}
		if strings.HasPrefix(segment, "[") {
			builder.WriteString(segment)
			continue
		}
		if builder.Len() != 0 {
			builder.WriteByte('.')
		}
		builder.WriteString(segment)
	}
	return builder.String()
}

func simpleTreeKind(language api.Language, nodeType string) (string, bool) {
	switch language {
	case api.LanguageJavaScript, api.LanguageJSX, api.LanguageTypeScript, api.LanguageTSX:
		switch nodeType {
		case "function_declaration", "generator_function_declaration":
			return "function", true
		case "class_declaration":
			return "class", true
		case "method_definition":
			return "method", true
		case "interface_declaration":
			return "interface", true
		case "type_alias_declaration":
			return "type", true
		case "variable_declarator":
			return "variable", true
		}
	case api.LanguagePython:
		switch nodeType {
		case "import_statement", "import_from_statement":
			return "import", true
		case "function_definition":
			return "function", true
		case "class_definition":
			return "class", true
		}
	case api.LanguageJava:
		switch nodeType {
		case "package_declaration":
			return "package", true
		case "import_declaration":
			return "import", true
		case "class_declaration":
			return "class", true
		case "interface_declaration":
			return "interface", true
		case "enum_declaration":
			return "enum", true
		case "record_declaration":
			return "record", true
		case "annotation_type_declaration":
			return "annotation", true
		case "method_declaration":
			return "method", true
		case "constructor_declaration":
			return "constructor", true
		case "field_declaration":
			return "field", true
		}
	case api.LanguageRust:
		switch nodeType {
		case "use_declaration":
			return "import", true
		case "mod_item":
			return "module", true
		case "struct_item":
			return "struct", true
		case "enum_item":
			return "enum", true
		case "trait_item":
			return "trait", true
		case "impl_item":
			return "impl", true
		case "function_item":
			return "function", true
		case "function_signature_item":
			return "method", true
		case "type_item":
			return "type", true
		case "const_item":
			return "constant", true
		case "static_item":
			return "static", true
		case "macro_definition":
			return "macro", true
		}
	case api.LanguageC, api.LanguageCPP:
		switch nodeType {
		case "preproc_include":
			return "import", true
		case "namespace_definition":
			return "namespace", true
		case "class_specifier":
			return "class", true
		case "struct_specifier":
			return "struct", true
		case "union_specifier":
			return "union", true
		case "enum_specifier":
			return "enum", true
		case "type_definition":
			return "type", true
		case "function_definition", "declaration", "field_declaration":
			return "", true
		}
	case api.LanguageCSharp:
		switch nodeType {
		case "using_directive":
			return "import", true
		case "namespace_declaration":
			return "namespace", true
		case "class_declaration":
			return "class", true
		case "interface_declaration":
			return "interface", true
		case "struct_declaration":
			return "struct", true
		case "record_declaration":
			return "record", true
		case "enum_declaration":
			return "enum", true
		case "method_declaration":
			return "method", true
		case "constructor_declaration":
			return "constructor", true
		case "property_declaration":
			return "property", true
		case "field_declaration":
			return "field", true
		}
	case api.LanguageRuby:
		switch nodeType {
		case "module":
			return "module", true
		case "class":
			return "class", true
		case "method", "singleton_method":
			return "method", true
		}
	case api.LanguageKotlin:
		switch nodeType {
		case "package_header":
			return "package", true
		case "import_header":
			return "import", true
		case "class_declaration":
			return "class", true
		case "object_declaration":
			return "object", true
		case "companion_object":
			return "companion_object", true
		case "type_alias", "typealias", "type_alias_declaration", "typealias_declaration":
			return "typealias", true
		case "function_declaration":
			return "function", true
		case "property_declaration":
			return "property", true
		}
	case api.LanguageSwift:
		switch nodeType {
		case "import_declaration":
			return "import", true
		case "protocol_declaration":
			return "protocol", true
		case "class_declaration":
			return "class", true
		case "function_declaration", "protocol_function_declaration":
			return "function", true
		case "property_declaration":
			return "property", true
		case "enum_entry":
			return "enum_case", true
		}
	case api.LanguageBash:
		switch nodeType {
		case "function_definition":
			return "function", true
		case "variable_assignment":
			return "variable", true
		}
	}
	return "", false
}

func (extractor treeExtractor) refineSpec(node *gotreesitter.Node, spec treeSymbolSpec, parentKind string) treeSymbolSpec {
	switch extractor.language {
	case api.LanguagePython:
		if spec.kind == "function" && parentKind == "class" {
			spec.kind = "method"
		}
	case api.LanguageRust:
		if spec.kind == "function" && (parentKind == "impl" || parentKind == "trait") {
			spec.kind = "method"
		}
	case api.LanguageKotlin, api.LanguageSwift:
		if spec.kind == "function" && isContainerKind(parentKind) {
			spec.kind = "method"
		}
	case api.LanguageC, api.LanguageCPP:
		if spec.kind == "function" && (parentKind == "class" || parentKind == "struct") {
			spec.kind = "method"
		}
	}
	if isJSLike(extractor.language) && (spec.kind == "function" || spec.kind == "variable") && isPascalCase(spec.name) && nodeContainsJSX(node.Text(extractor.source)) {
		spec.kind = "component"
	}
	return spec
}

func isContainerKind(kind string) bool {
	switch kind {
	case "class", "struct", "enum", "protocol", "interface", "object", "companion_object":
		return true
	default:
		return false
	}
}

func isJSLike(language api.Language) bool {
	return language == api.LanguageJavaScript || language == api.LanguageJSX || language == api.LanguageTypeScript || language == api.LanguageTSX
}

func (extractor treeExtractor) nodeName(node *gotreesitter.Node) string {
	for _, field := range []string{"name", "key", "declarator"} {
		if child := node.ChildByFieldName(field, extractor.grammar); child != nil {
			name := normalizeTreeName(child.Text(extractor.source))
			if field == "key" && (extractor.language == api.LanguageJSON || extractor.language == api.LanguageYAML) {
				name = normalizeConfigKey(name)
			}
			if name != "" {
				if field == "declarator" {
					if nested := extractor.nodeName(child); nested != "" {
						return nested
					}
				}
				return name
			}
		}
	}
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child == nil {
			continue
		}
		switch child.Type(extractor.grammar) {
		case "identifier", "property_identifier", "type_identifier", "shorthand_property_identifier", "string", "string_scalar", "plain_scalar", "scoped_identifier", "qualified_identifier", "qualified_name", "namespace_identifier", "field_identifier", "simple_identifier", "constant", "variable_name", "word", "system_lib_string", "dotted_name", "tag_name":
			if name := normalizeTreeName(child.Text(extractor.source)); name != "" {
				return name
			}
		case "function_declarator", "parenthesized_declarator", "pointer_declarator", "reference_declarator", "init_declarator", "variable_declarator":
			if name := extractor.nodeName(child); name != "" {
				return name
			}
		}
	}
	return ""
}

func (extractor treeExtractor) descendantName(node *gotreesitter.Node, wantedType string) string {
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child == nil {
			continue
		}
		if child.Type(extractor.grammar) == wantedType {
			return extractor.nodeName(child)
		}
		if name := extractor.descendantName(child, wantedType); name != "" {
			return name
		}
	}
	return ""
}

func (extractor treeExtractor) declaratorName(node *gotreesitter.Node) string {
	for _, field := range []string{"declarator", "name"} {
		if child := node.ChildByFieldName(field, extractor.grammar); child != nil {
			if name := extractor.nodeName(child); name != "" {
				return name
			}
		}
	}
	return extractor.nodeName(node)
}

func (extractor treeExtractor) hasDescendantType(node *gotreesitter.Node, wanted string) bool {
	for index := 0; index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child == nil {
			continue
		}
		if child.Type(extractor.grammar) == wanted || extractor.hasDescendantType(child, wanted) {
			return true
		}
	}
	return false
}

func (extractor treeExtractor) sourceNameOrLine(node *gotreesitter.Node) string {
	if source := node.ChildByFieldName("source", extractor.grammar); source != nil {
		if name := normalizeTreeName(source.Text(extractor.source)); name != "" {
			return name
		}
	}
	return extractor.statementName(node, "import")
}

func (extractor treeExtractor) statementName(node *gotreesitter.Node, kind string) string {
	line := strings.TrimSpace(firstLine(node.Text(extractor.source)))
	line = strings.TrimSuffix(line, ";")
	for _, prefix := range []string{"import ", "from ", "package ", "use ", "using ", "#include ", "#include", "require ", "load "} {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
			break
		}
	}
	if kind == "package" {
		return normalizeTreeName(line)
	}
	return normalizeTreeName(line)
}

func treeErrorRanges(root *gotreesitter.Node) []navmodel.Range {
	ranges := make([]navmodel.Range, 0, 4)
	var visit func(*gotreesitter.Node) bool
	visit = func(node *gotreesitter.Node) bool {
		if node == nil {
			return false
		}
		hasNestedError := false
		for index := 0; index < node.ChildCount(); index++ {
			hasNestedError = visit(node.Child(index)) || hasNestedError
		}
		selfError := node.IsError() || node.IsMissing()
		if selfError && !hasNestedError {
			ranges = append(ranges, treeLineRange(node))
		}
		return selfError || hasNestedError
	}
	visit(root)
	return ranges
}

func treeLineRange(node *gotreesitter.Node) navmodel.Range {
	start := uint32(node.StartPoint().Row) + 1
	end := uint32(node.EndPoint().Row) + 1
	if node.EndByte() > node.StartByte() && node.EndPoint().Column == 0 && end > start {
		end--
	}
	if end < start {
		end = start
	}
	return navmodel.Range{Start: start, End: end}
}

func normalizeTreeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'`")
	value = strings.TrimSuffix(value, ":")
	if strings.Contains(value, "\n") {
		value = firstLine(value)
	}
	return strings.TrimSpace(value)
}

func normalizeConfigKey(value string) string {
	return strings.TrimSpace(strings.Trim(value, "\"'`"))
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func rubyImportName(line string) string {
	line = strings.TrimSpace(line)
	for _, keyword := range []string{"require", "require_relative", "load"} {
		if line == keyword {
			return keyword
		}
		if strings.HasPrefix(line, keyword+" ") || strings.HasPrefix(line, keyword+"(") {
			return importArgument(strings.TrimSpace(strings.TrimPrefix(line, keyword)))
		}
	}
	return ""
}

func bashImportName(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "source "):
		return importArgument(strings.TrimPrefix(line, "source"))
	case strings.HasPrefix(line, ". "):
		return importArgument(strings.TrimPrefix(line, "."))
	default:
		return ""
	}
}

func importArgument(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "("), ")"))
	if index := strings.IndexAny(value, " \t"); index >= 0 {
		value = value[:index]
	}
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func kotlinClassKind(source string) string {
	text := strings.ToLower(firstLine(source))
	switch {
	case strings.Contains(text, "companion object"):
		return "companion_object"
	case strings.Contains(text, "interface"):
		return "interface"
	case strings.Contains(text, "enum"):
		return "enum"
	case strings.Contains(text, "object"):
		return "object"
	default:
		return "class"
	}
}

func swiftClassKind(source string) string {
	text := strings.ToLower(firstLine(source))
	switch {
	case strings.Contains(text, "struct"):
		return "struct"
	case strings.Contains(text, "enum"):
		return "enum"
	case strings.Contains(text, "actor"):
		return "actor"
	default:
		return "class"
	}
}

func hasTopLevelComma(value string) bool {
	angle, paren, bracket, brace := 0, 0, 0, 0
	for _, character := range value {
		switch character {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case '[':
			bracket++
		case ']':
			if bracket > 0 {
				bracket--
			}
		case '{':
			brace++
		case '}':
			if brace > 0 {
				brace--
			}
		case ',':
			if angle == 0 && paren == 0 && bracket == 0 && brace == 0 {
				return true
			}
		}
	}
	return false
}

func isPascalCase(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

func nodeContainsJSX(source string) bool {
	return strings.Contains(source, "return <") || strings.Contains(source, "return (<") || strings.Contains(source, "(<") || strings.Contains(source, "= <") || strings.Contains(source, "=> <")
}
