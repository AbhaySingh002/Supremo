package repository

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/state"
)

// GoParser is Phase 2's sole structural adapter. Other languages still get a
// bounded text chunk for deterministic lexical discovery.
type GoParser struct{}

func (GoParser) Parse(path string, data []byte) (ParsedFile, error) {
	if filepath.Ext(path) != ".go" {
		return textFile(path, data), nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
	if err != nil {
		return ParsedFile{}, err
	}
	result := ParsedFile{Language: "go"}
	pkgKey := "go:package:" + file.Name.Name
	result.Symbols = append(result.Symbols, symbolFor(fset, file.Name, pkgKey, file.Name.Name, file.Name.Name, "package", true, "package "+file.Name.Name, ""))
	result.Summaries = append(result.Summaries, state.RepositorySummaryInput{Scope: "file", TargetStableKey: "file:" + path, Content: "Go package " + file.Name.Name + " in " + path, GenerationMethod: "deterministic", Confidence: 1})

	for _, decl := range file.Decls {
		switch declaration := decl.(type) {
		case *ast.FuncDecl:
			result.addFunction(fset, data, file.Name.Name, declaration)
		case *ast.GenDecl:
			result.addGenDecl(fset, data, file.Name.Name, declaration)
		}
	}
	return result, nil
}

func textFile(path string, data []byte) ParsedFile {
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ParsedFile{Language: "text"}
	}
	return ParsedFile{
		Language:  "text",
		Chunks:    []state.RepositoryChunkInput{{StableKey: "file:" + path, Kind: "file", StartLine: 1, StartColumn: 1, EndLine: lineCount(content), EndColumn: 1, Content: content}},
		Summaries: []state.RepositorySummaryInput{{Scope: "file", TargetStableKey: "file:" + path, Content: "Text file " + path, GenerationMethod: "deterministic", Confidence: 1}},
	}
}

func (result *ParsedFile) addFunction(fset *token.FileSet, data []byte, pkg string, decl *ast.FuncDecl) {
	name, kind := decl.Name.Name, "function"
	qualified := pkg + "." + name
	key := "go:" + pkg + ":function:" + name
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		receiver := receiverName(decl.Recv.List[0].Type)
		kind, qualified, key = "method", pkg+"."+receiver+"."+name, "go:"+pkg+":method:"+receiver+":"+name
	}
	signature := formatNode(fset, decl.Type)
	if strings.HasPrefix(signature, "func") {
		signature = "func " + name + strings.TrimPrefix(signature, "func")
	}
	doc := commentText(decl.Doc)
	symbol := symbolFor(fset, decl, key, name, qualified, kind, decl.Name.IsExported(), signature, doc)
	result.Symbols = append(result.Symbols, symbol)
	result.Chunks = append(result.Chunks, chunkFor(fset, data, decl, key, "declaration"))
	if doc != "" {
		result.Summaries = append(result.Summaries, state.RepositorySummaryInput{Scope: "symbol", TargetStableKey: key, Content: qualified + ": " + doc, GenerationMethod: "deterministic", Confidence: 1})
	}
	if decl.Body == nil {
		return
	}
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if !ok || identifier.Obj != nil && identifier.Obj.Kind != ast.Fun {
			return true
		}
		target := "go:" + pkg + ":function:" + identifier.Name
		result.Relations = append(result.Relations, state.RepositoryRelationInput{Type: "CALLS", SourceSymbolKey: key, TargetSymbolKey: target, TargetName: target, Confidence: 1, Provenance: state.Provenance{Authority: state.AuthorityDerived}})
		return true
	})
	if strings.HasSuffix(strings.ToLower(fset.Position(decl.Pos()).Filename), "_test.go") && strings.HasPrefix(name, "Test") {
		result.Relations = append(result.Relations, state.RepositoryRelationInput{Type: "TESTS", SourceSymbolKey: key, TargetName: "go:package:" + pkg, Confidence: 1, Provenance: state.Provenance{Authority: state.AuthorityDerived}})
	}
}

func (result *ParsedFile) addGenDecl(fset *token.FileSet, data []byte, pkg string, declaration *ast.GenDecl) {
	if declaration.Tok == token.IMPORT {
		for _, spec := range declaration.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok || importSpec.Path == nil {
				continue
			}
			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				continue
			}
			result.Relations = append(result.Relations, state.RepositoryRelationInput{Type: "IMPORTS", TargetName: "import:" + path, Confidence: 1, Provenance: state.Provenance{Authority: state.AuthorityDerived}})
		}
		return
	}
	for _, spec := range declaration.Specs {
		name, kind, exported := "", "", false
		switch value := spec.(type) {
		case *ast.TypeSpec:
			name, kind, exported = value.Name.Name, "type", value.Name.IsExported()
			if _, ok := value.Type.(*ast.InterfaceType); ok {
				kind = "interface"
			}
		case *ast.ValueSpec:
			if len(value.Names) == 0 {
				continue
			}
			name, exported = value.Names[0].Name, value.Names[0].IsExported()
			if declaration.Tok == token.CONST {
				kind = "constant"
			} else {
				kind = "variable"
			}
		default:
			continue
		}
		key := "go:" + pkg + ":" + kind + ":" + name
		doc := commentText(declaration.Doc) + "\n" + specDoc(spec)
		symbol := symbolFor(fset, spec, key, name, pkg+"."+name, kind, exported, formatNode(fset, spec), strings.TrimSpace(doc))
		result.Symbols = append(result.Symbols, symbol)
		result.Chunks = append(result.Chunks, chunkFor(fset, data, spec, key, "declaration"))
		if strings.TrimSpace(doc) != "" {
			result.Summaries = append(result.Summaries, state.RepositorySummaryInput{Scope: "symbol", TargetStableKey: key, Content: symbol.QualifiedName + ": " + strings.TrimSpace(doc), GenerationMethod: "deterministic", Confidence: 1})
		}
	}
}

func symbolFor(fset *token.FileSet, node ast.Node, key, name, qualified, kind string, exported bool, signature, doc string) state.RepositorySymbolInput {
	start, end := fset.Position(node.Pos()), fset.Position(node.End())
	return state.RepositorySymbolInput{StableKey: key, Name: name, QualifiedName: qualified, Kind: kind, Exported: exported, StartLine: start.Line, StartColumn: start.Column, EndLine: end.Line, EndColumn: end.Column, Signature: signature, DocComment: doc}
}

func chunkFor(fset *token.FileSet, data []byte, node ast.Node, key, kind string) state.RepositoryChunkInput {
	start, end := fset.Position(node.Pos()), fset.Position(node.End())
	return state.RepositoryChunkInput{StableKey: key, SymbolKey: key, Kind: kind, StartLine: start.Line, StartColumn: start.Column, EndLine: end.Line, EndColumn: end.Column, Content: sourceRange(data, start.Offset, end.Offset)}
}

func sourceRange(data []byte, start, end int) string {
	if start < 0 || end < start || start > len(data) {
		return ""
	}
	if end > len(data) {
		end = len(data)
	}
	return string(data[start:end])
}

func formatNode(fset *token.FileSet, node any) string {
	var output bytes.Buffer
	if err := format.Node(&output, fset, node); err != nil {
		return fmt.Sprint(node)
	}
	return strings.TrimSpace(output.String())
}

func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.TrimSpace(group.Text())
}

func specDoc(spec ast.Spec) string {
	switch value := spec.(type) {
	case *ast.TypeSpec:
		return commentText(value.Doc)
	case *ast.ValueSpec:
		return commentText(value.Doc)
	}
	return ""
}

func receiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverName(value.X)
	case *ast.IndexExpr:
		return receiverName(value.X)
	case *ast.IndexListExpr:
		return receiverName(value.X)
	}
	return "receiver"
}

func lineCount(content string) int { return strings.Count(content, "\n") + 1 }
