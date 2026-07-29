// Command zz_rewrite performs the mechanical part of the PDFDict.Entries
// migration: map syntax -> Dict method calls. Throwaway; not committed.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// isEntries reports whether e is a `<something>.Entries` selector.
func isEntries(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Entries"
}

// isPDFValueMapType reports whether e is map[string]PDFValue or
// map[string]pdf.PDFValue.
func isPDFValueMapType(e ast.Expr) bool {
	m, ok := e.(*ast.MapType)
	if !ok {
		return false
	}
	k, ok := m.Key.(*ast.Ident)
	if !ok || k.Name != "string" {
		return false
	}
	switch v := m.Value.(type) {
	case *ast.Ident:
		return v.Name == "PDFValue"
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		return ok && x.Name == "pdf" && v.Sel.Name == "PDFValue"
	}
	return false
}

func call(recv ast.Expr, method string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(method)},
		Args: args,
	}
}

func rewriteFile(path string, qualifier string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}
	changed := false

	// Statement-level rewrites first (assignment, delete, range), because they
	// consume index expressions the expression pass would otherwise rewrite.
	var fixStmts func(n ast.Node) bool
	fixStmts = func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.BlockStmt:
			for i, st := range s.List {
				if repl, ok := rewriteStmt(st); ok {
					s.List[i] = repl
					changed = true
				}
			}
		case *ast.RangeStmt:
			if isEntries(s.X) {
				s.X = call(s.X, "All")
				changed = true
			}
		}
		return true
	}
	ast.Inspect(f, fixStmts)

	// Assignment/expression rewrites reachable anywhere.
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			// v, ok := d.Entries[k]
			if len(x.Lhs) == 2 && len(x.Rhs) == 1 {
				if ix, ok := x.Rhs[0].(*ast.IndexExpr); ok && isEntries(ix.X) {
					x.Rhs[0] = call(ix.X, "Lookup", ix.Index)
					changed = true
				}
			}
			// d.Entries[k] = v  (single, plain assign)
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 && x.Tok == token.ASSIGN {
				if ix, ok := x.Lhs[0].(*ast.IndexExpr); ok && isEntries(ix.X) {
					// Turn into an ExprStmt via a marker the caller replaces.
					x.Lhs[0] = ast.NewIdent("__SET__")
					x.Rhs[0] = call(ix.X, "Set", ix.Index, x.Rhs[0])
					changed = true
				}
			}
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok {
				switch id.Name {
				case "delete":
					if len(x.Args) == 2 && isEntries(x.Args[0]) {
						x.Fun = &ast.SelectorExpr{X: x.Args[0], Sel: ast.NewIdent("Del")}
						x.Args = x.Args[1:]
						changed = true
					}
				case "len":
					if len(x.Args) == 1 && isEntries(x.Args[0]) {
						x.Fun = &ast.SelectorExpr{X: x.Args[0], Sel: ast.NewIdent("Len")}
						x.Args = nil
						changed = true
					}
				}
			}
		}
		return true
	})

	// Remaining index reads: d.Entries[k] -> d.Entries.Get(k)
	replaceIndexReads(f, &changed)

	// Composite literal field:  Entries: map[string]PDFValue{...}
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok || k.Name != "Entries" {
			return true
		}
		if cl, ok := kv.Value.(*ast.CompositeLit); ok && isPDFValueMapType(cl.Type) {
			kv.Value = &ast.CallExpr{Fun: ast.NewIdent(qualifier + "DictOf"), Args: []ast.Expr{cl}}
			changed = true
		}
		return true
	})

	if !changed {
		return false, nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return false, err
	}
	out := strings.ReplaceAll(buf.String(), "__SET__ = ", "")
	return true, os.WriteFile(path, []byte(out), 0644)
}

// replaceIndexReads rewrites every remaining d.Entries[k] read.
func replaceIndexReads(f *ast.File, changed *bool) {
	var rec func(n ast.Node)
	rewrite := func(e *ast.Expr) {
		if ix, ok := (*e).(*ast.IndexExpr); ok && isEntries(ix.X) {
			*e = call(ix.X, "Get", ix.Index)
			*changed = true
		}
	}
	rec = func(n ast.Node) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.AssignStmt:
				for i := range x.Rhs {
					rewrite(&x.Rhs[i])
				}
				for i := range x.Lhs {
					if _, isIx := x.Lhs[i].(*ast.IndexExpr); !isIx {
						rewrite(&x.Lhs[i])
					}
				}
			case *ast.CallExpr:
				for i := range x.Args {
					rewrite(&x.Args[i])
				}
			case *ast.ReturnStmt:
				for i := range x.Results {
					rewrite(&x.Results[i])
				}
			case *ast.BinaryExpr:
				rewrite(&x.X)
				rewrite(&x.Y)
			case *ast.TypeAssertExpr:
				rewrite(&x.X)
			case *ast.ParenExpr:
				rewrite(&x.X)
			case *ast.KeyValueExpr:
				rewrite(&x.Value)
			case *ast.IfStmt:
				if x.Cond != nil {
					rewrite(&x.Cond)
				}
			case *ast.SwitchStmt:
				if x.Tag != nil {
					rewrite(&x.Tag)
				}
			case *ast.TypeSwitchStmt:
			case *ast.UnaryExpr:
				rewrite(&x.X)
			case *ast.CompositeLit:
				for i := range x.Elts {
					rewrite(&x.Elts[i])
				}
			case *ast.IndexExpr:
				rewrite(&x.Index)
			case *ast.SelectorExpr:
				rewrite(&x.X)
			}
			return true
		})
	}
	rec(f)
}

func main() {
	root := os.Args[1]
	filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(path, "zz_rewrite") {
			return nil
		}
		q := "pdf."
		if strings.Contains(path, "internal/pdf/") {
			q = ""
		}
		ok, err := rewriteFile(path, q)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		} else if ok {
			fmt.Println("rewrote", path)
		}
		return nil
	})
}
