// Code Property Graph generator for Funds ETL
// Generates: AST summary, import dependency graph, function call graph, CFG stats, type hierarchy
// Output: DOT, Mermaid, JSON, Markdown summary
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ========== Data Models ==========

type PackageNode struct {
	Path       string    `json:"path"`
	Name       string    `json:"name"`
	Files      []string  `json:"files"`
	Imports    []string  `json:"imports"`
	Funcs      []FuncDef `json:"funcs"`
	Types      []TypeDef `json:"types"`
	TypeCount  int       `json:"type_count"`
	VarCount   int       `json:"var_count"`
	ConstCount int       `json:"const_count"`
}

type FuncDef struct {
	Name       string     `json:"name"`
	File       string     `json:"file"`
	Line       int        `json:"line"`
	Recv       string     `json:"recv,omitempty"`
	Params     []string   `json:"params"`
	Returns    []string   `json:"returns"`
	Calls      []CallSite `json:"calls"`
	IsExported bool       `json:"is_exported"`
	CFGSummary CFGStats   `json:"cfg_summary"`
}

type CFGStats struct {
	IfCount     int `json:"if_count"`
	ForCount    int `json:"for_count"`
	RangeCount  int `json:"range_count"`
	SwitchCount int `json:"switch_count"`
	SelectCount int `json:"select_count"`
	DeferCount  int `json:"defer_count"`
	GoCount     int `json:"go_count"`
	ReturnCount int `json:"return_count"`
	AssignCount int `json:"assign_count"`
}

type TypeDef struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Kind    string   `json:"kind"`
	Fields  []string `json:"fields,omitempty"`
	Methods []string `json:"methods,omitempty"`
	Embeds  []string `json:"embeds,omitempty"`
}

type CallSite struct {
	FuncName string `json:"func_name"`
	PkgName  string `json:"pkg_name,omitempty"`
	Line     int    `json:"line"`
}

type DepEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type CallEdge struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
	Line   int    `json:"line"`
}

type projectAnalysis struct {
	Packages  []PackageNode `json:"packages"`
	DepEdges  []DepEdge     `json:"dep_edges"`
	CallEdges []CallEdge    `json:"call_edges"`
}

// ========== Main ==========

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: cpg <project-root>\n")
		os.Exit(1)
	}
	root := os.Args[1]
	outDir := filepath.Join(root, "docs", "cpg")
	os.MkdirAll(outDir, 0755)

	data := analyzeProject(root)

	jb, _ := json.MarshalIndent(data, "", "  ")
	jsonPath := filepath.Join(outDir, "cpg.json")
	os.WriteFile(jsonPath, jb, 0644)
	fmt.Printf("  JSON: %s\n", jsonPath)

	dotPath := filepath.Join(outDir, "deps.dot")
	os.WriteFile(dotPath, []byte(generateDOT(data)), 0644)
	fmt.Printf("  DOT: %s\n", dotPath)

	mmdPath := filepath.Join(outDir, "deps.mmd")
	os.WriteFile(mmdPath, []byte(generateMermaid(data)), 0644)
	fmt.Printf("  Mermaid: %s\n", mmdPath)

	sumPath := filepath.Join(outDir, "summary.md")
	os.WriteFile(sumPath, []byte(generateSummary(data)), 0644)
	fmt.Printf("  Summary: %s\n", sumPath)

	totalFuncs, totalTypes := 0, 0
	for _, p := range data.Packages {
		totalFuncs += len(p.Funcs)
		totalTypes += len(p.Types)
	}
	fmt.Printf("\nDone: %d pkgs, %d funcs, %d types, %d dep edges, %d call edges\n",
		len(data.Packages), totalFuncs, totalTypes, len(data.DepEdges), len(data.CallEdges))
}

// ========== Analysis ==========

func analyzeProject(root string) *projectAnalysis {
	pa := &projectAnalysis{}

	var pkgDirs []string
	for _, base := range []string{"internal", "cmd"} {
		filepath.Walk(filepath.Join(root, base), func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.IsDir() {
				return nil
			}
			gos, _ := filepath.Glob(filepath.Join(path, "*.go"))
			if len(gos) > 0 {
				rel, _ := filepath.Rel(root, path)
				pkgDirs = append(pkgDirs, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(pkgDirs)

	pkgAlias := make(map[string]string)
	for _, d := range pkgDirs {
		importPath := "github.com/etl/backend/" + d
		pkgAlias[importPath] = filepath.Base(d)
	}

	for _, d := range pkgDirs {
		importPath := "github.com/etl/backend/" + d
		absPath := filepath.Join(root, d)
		pkg := parsePackage(absPath, root, importPath)
		pa.Packages = append(pa.Packages, pkg)
	}

	// Dep edges
	for _, pkg := range pa.Packages {
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, "github.com/etl/backend/") {
				pa.DepEdges = append(pa.DepEdges, DepEdge{From: pkg.Path, To: imp})
			}
		}
	}

	// Call edges
	for _, pkg := range pa.Packages {
		for _, fn := range pkg.Funcs {
			callerQ := pkg.Path + "." + fn.Name
			for _, call := range fn.Calls {
				calleeQ := resolveCall(pkg.Path, pa.Packages, call, pkgAlias)
				if calleeQ != "" && calleeQ != callerQ {
					pa.CallEdges = append(pa.CallEdges, CallEdge{Caller: callerQ, Callee: calleeQ, Line: call.Line})
				}
			}
		}
	}

	return pa
}

func parsePackage(absPath, root, importPath string) PackageNode {
	pkg := PackageNode{Path: importPath}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, absPath, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  WARN %s: %v\n", absPath, err)
		return pkg
	}

	seenImports := make(map[string]bool)

	for _, astPkg := range pkgs {
		pkg.Name = astPkg.Name
		for fname, file := range astPkg.Files {
			relFile, _ := filepath.Rel(root, fname)
			pkg.Files = append(pkg.Files, relFile)

			// Imports
			for _, imp := range file.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(impPath, "github.com/etl/backend/") {
					seenImports[impPath] = true
				}
			}

			// Declarations
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					switch d.Tok {
					case token.TYPE:
						pkg.TypeCount += len(d.Specs)
						for _, spec := range d.Specs {
							ts := spec.(*ast.TypeSpec)
							td := TypeDef{
								Name: ts.Name.Name,
								File: relFile,
								Line: fset.Position(ts.Pos()).Line,
							}
							switch t := ts.Type.(type) {
							case *ast.StructType:
								td.Kind = "struct"
								if t.Fields != nil {
									for _, f := range t.Fields.List {
										names := fieldNames(f)
										typ := exprStr(f.Type)
										for _, n := range names {
											if n == "" || n == "?" {
												td.Embeds = append(td.Embeds, typ)
											} else {
												td.Fields = append(td.Fields, n+": "+typ)
											}
										}
									}
								}
							case *ast.InterfaceType:
								td.Kind = "interface"
								if t.Methods != nil {
									for _, m := range t.Methods.List {
										for _, n := range m.Names {
											td.Methods = append(td.Methods, n.Name)
										}
									}
								}
							case *ast.Ident:
								td.Kind = "alias(" + t.Name + ")"
							default:
								td.Kind = fmt.Sprintf("type(%T)", t)
							}
							pkg.Types = append(pkg.Types, td)
						}
					case token.VAR:
						pkg.VarCount += len(d.Specs)
					case token.CONST:
						pkg.ConstCount += len(d.Specs)
					}
				case *ast.FuncDecl:
					fd := FuncDef{
						Name:       d.Name.Name,
						File:       relFile,
						Line:       fset.Position(d.Pos()).Line,
						IsExported: ast.IsExported(d.Name.Name),
					}
					if d.Recv != nil && len(d.Recv.List) > 0 {
						fd.Recv = exprStr(d.Recv.List[0].Type)
					}
					if d.Type.Params != nil {
						for _, p := range d.Type.Params.List {
							for range p.Names {
								fd.Params = append(fd.Params, exprStr(p.Type))
							}
						}
					}
					if d.Type.Results != nil {
						for _, r := range d.Type.Results.List {
							fd.Returns = append(fd.Returns, exprStr(r.Type))
						}
					}
					if d.Body != nil {
						cfg := &CFGStats{}
						ast.Inspect(d.Body, func(n ast.Node) bool {
							switch n.(type) {
							case *ast.IfStmt:
								cfg.IfCount++
							case *ast.ForStmt:
								cfg.ForCount++
							case *ast.RangeStmt:
								cfg.RangeCount++
							case *ast.SwitchStmt:
								cfg.SwitchCount++
							case *ast.SelectStmt:
								cfg.SelectCount++
							case *ast.DeferStmt:
								cfg.DeferCount++
							case *ast.GoStmt:
								cfg.GoCount++
							case *ast.ReturnStmt:
								cfg.ReturnCount++
							case *ast.AssignStmt:
								cfg.AssignCount++
							case *ast.CallExpr:
								cs := callSite(n.(*ast.CallExpr), fset)
								if cs.FuncName != "" {
									fd.Calls = append(fd.Calls, cs)
								}
							}
							return true
						})
						fd.CFGSummary = *cfg
					}
					pkg.Funcs = append(pkg.Funcs, fd)
				}
			}
		}
	}

	for imp := range seenImports {
		pkg.Imports = append(pkg.Imports, imp)
	}
	sort.Strings(pkg.Imports)
	sort.Slice(pkg.Funcs, func(i, j int) bool { return pkg.Funcs[i].Name < pkg.Funcs[j].Name })
	sort.Slice(pkg.Types, func(i, j int) bool { return pkg.Types[i].Name < pkg.Types[j].Name })
	return pkg
}

func callSite(call *ast.CallExpr, fset *token.FileSet) CallSite {
	cs := CallSite{Line: fset.Position(call.Pos()).Line}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		cs.FuncName = fun.Name
	case *ast.SelectorExpr:
		cs.FuncName = fun.Sel.Name
		if x, ok := fun.X.(*ast.Ident); ok {
			cs.PkgName = x.Name
		}
	}
	return cs
}

func resolveCall(callerPkg string, packages []PackageNode, call CallSite, pkgAlias map[string]string) string {
	if call.PkgName == "" {
		return callerPkg + "." + call.FuncName
	}
	for _, pkg := range packages {
		alias := filepath.Base(pkg.Path)
		if call.PkgName == alias || call.PkgName == pkg.Name {
			return pkg.Path + "." + call.FuncName
		}
	}
	return ""
}

// ========== Output ==========

func generateDOT(pa *projectAnalysis) string {
	var sb strings.Builder
	sb.WriteString("// Code Property Graph — Dependency Graph\n")
	sb.WriteString("// Auto-generated by tools/cpg\n\n")
	sb.WriteString("digraph CPG {\n\trankdir=LR;\n")
	sb.WriteString("\tnode [shape=box, style=rounded, fontname=\"Consolas\", fontsize=10];\n")
	sb.WriteString("\tedge [fontname=\"Consolas\", fontsize=8];\n\n")

	groups := make(map[string][]PackageNode)
	for _, pkg := range pa.Packages {
		parts := strings.Split(pkg.Path, "/")
		if len(parts) >= 4 {
			groups[parts[3]] = append(groups[parts[3]], pkg)
		}
	}
	done := make(map[string]bool)
	for groupName, pkgs := range groups {
		sb.WriteString(fmt.Sprintf("\tsubgraph cluster_%s {\n\t\tlabel=\"%s\";\n\t\tstyle=dashed;\n", groupName, groupName))
		for _, pkg := range pkgs {
			label := filepath.Base(pkg.Path)
			sb.WriteString(fmt.Sprintf("\t\t\"%s\" [label=\"%s (%d fn)\"];\n", pkg.Path, label, len(pkg.Funcs)))
			done[pkg.Path] = true
		}
		sb.WriteString("\t}\n")
	}
	for _, pkg := range pa.Packages {
		if !done[pkg.Path] {
			label := filepath.Base(pkg.Path)
			sb.WriteString(fmt.Sprintf("\t\"%s\" [label=\"%s (%d fn)\"];\n", pkg.Path, label, len(pkg.Funcs)))
		}
	}
	sb.WriteString("\n")
	for _, e := range pa.DepEdges {
		sb.WriteString(fmt.Sprintf("\t\"%s\" -> \"%s\";\n", e.From, e.To))
	}
	sb.WriteString("}\n")
	return sb.String()
}

func generateMermaid(pa *projectAnalysis) string {
	var sb strings.Builder
	sb.WriteString("```mermaid\ngraph LR\n")
	replacer := strings.NewReplacer("/", "_", ".", "_")
	for _, pkg := range pa.Packages {
		nodeID := replacer.Replace(pkg.Path)
		label := filepath.Base(pkg.Path)
		sb.WriteString(fmt.Sprintf("\t%s[\"%s\"]\n", nodeID, label))
	}
	for _, e := range pa.DepEdges {
		from := replacer.Replace(e.From)
		to := replacer.Replace(e.To)
		sb.WriteString(fmt.Sprintf("\t%s --> %s\n", from, to))
	}
	sb.WriteString("```\n")
	return sb.String()
}

func generateSummary(pa *projectAnalysis) string {
	var sb strings.Builder
	sb.WriteString("# Code Property Graph\n\n")
	sb.WriteString("**Module**: `github.com/etl/backend`\n\n")

	totalFuncs, totalTypes, totalFiles, totalStructs, totalIfaces := 0, 0, 0, 0, 0
	for _, pkg := range pa.Packages {
		totalFuncs += len(pkg.Funcs)
		totalTypes += len(pkg.Types)
		totalFiles += len(pkg.Files)
		for _, t := range pkg.Types {
			if t.Kind == "struct" {
				totalStructs++
			} else if t.Kind == "interface" {
				totalIfaces++
			}
		}
	}

	// CFG total
	totalIf, totalFor, totalSwitch, totalDefer, totalGo := 0, 0, 0, 0, 0
	for _, pkg := range pa.Packages {
		for _, fn := range pkg.Funcs {
			totalIf += fn.CFGSummary.IfCount
			totalFor += fn.CFGSummary.ForCount + fn.CFGSummary.RangeCount
			totalSwitch += fn.CFGSummary.SwitchCount + fn.CFGSummary.SelectCount
			totalDefer += fn.CFGSummary.DeferCount
			totalGo += fn.CFGSummary.GoCount
		}
	}

	sb.WriteString("## Statistics\n\n")
	sb.WriteString("| Metric | Count |\n|---|---|\n")
	sb.WriteString(fmt.Sprintf("| Packages | %d |\n", len(pa.Packages)))
	sb.WriteString(fmt.Sprintf("| Source files | %d |\n", totalFiles))
	sb.WriteString(fmt.Sprintf("| Functions | %d |\n", totalFuncs))
	sb.WriteString(fmt.Sprintf("| Types (%d structs, %d interfaces) | %d |\n", totalStructs, totalIfaces, totalTypes))
	sb.WriteString(fmt.Sprintf("| Internal dep edges | %d |\n", len(pa.DepEdges)))
	sb.WriteString(fmt.Sprintf("| Call edges | %d |\n", len(pa.CallEdges)))
	sb.WriteString("\n## CFG Summary\n\n")
	sb.WriteString("| Metric | Total |\n|---|---|\n")
	sb.WriteString(fmt.Sprintf("| if/else branches | %d |\n", totalIf))
	sb.WriteString(fmt.Sprintf("| for/range loops | %d |\n", totalFor))
	sb.WriteString(fmt.Sprintf("| switch/select | %d |\n", totalSwitch))
	sb.WriteString(fmt.Sprintf("| defer statements | %d |\n", totalDefer))
	sb.WriteString(fmt.Sprintf("| goroutines | %d |\n", totalGo))

	sb.WriteString("\n## Top Callers\n\n")
	sb.WriteString("| Caller | Calls Made |\n|---|---|\n")
	callCount := make(map[string]int)
	for _, e := range pa.CallEdges {
		callCount[e.Caller]++
	}
	type kv struct{ k string; v int }
	var sorted []kv
	for k, v := range callCount {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	for i, kv := range sorted {
		if i >= 20 {
			break
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d |\n", kv.k, kv.v))
	}

	sb.WriteString("\n## Package Detail\n\n")
	sb.WriteString("| Package | Files | Funcs | Types | Structs | Ifaces | Deps |\n|---|---|---|---|---|---|---|\n")
	for _, pkg := range pa.Packages {
		label := filepath.Base(pkg.Path)
		strs, ifs := 0, 0
		for _, t := range pkg.Types {
			if t.Kind == "struct" {
				strs++
			} else if t.Kind == "interface" {
				ifs++
			}
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d | %d | %d | %d | %d | %d |\n",
			label, len(pkg.Files), len(pkg.Funcs), len(pkg.Types), strs, ifs, len(pkg.Imports)))
	}

	sb.WriteString("\n## Top Structs (by field count)\n\n")
	type structInfo struct{ name, pkg string; fields int }
	var bigStructs []structInfo
	for _, pkg := range pa.Packages {
		for _, t := range pkg.Types {
			if t.Kind == "struct" && len(t.Fields) > 5 {
				bigStructs = append(bigStructs, structInfo{t.Name, filepath.Base(pkg.Path), len(t.Fields)})
			}
		}
	}
	sort.Slice(bigStructs, func(i, j int) bool { return bigStructs[i].fields > bigStructs[j].fields })
	sb.WriteString("| Struct | Package | Fields |\n|---|---|---|\n")
	for i, s := range bigStructs {
		if i >= 15 {
			break
		}
		sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %d |\n", s.name, s.pkg, s.fields))
	}

	sb.WriteString("\n## Diagrams\n\n")
	sb.WriteString("- **DOT**: `deps.dot` (Graphviz) — package dependency graph\n")
	sb.WriteString("- **Mermaid**: `deps.mmd` — package dependency graph\n")
	sb.WriteString("- **JSON**: `cpg.json` — full CPG data (AST types, CFG stats, call graph)\n")
	sb.WriteString("- **CodeQL DB**: `../codeql-db/` — deep analysis database\n")
	return sb.String()
}

// ========== Helpers ==========

func exprStr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprStr(t.X)
	case *ast.SelectorExpr:
		return exprStr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprStr(t.Elt)
	case *ast.MapType:
		return "map[" + exprStr(t.Key) + "]" + exprStr(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + exprStr(t.Elt)
	case *ast.FuncType:
		return "func(...)"
	default:
		return fmt.Sprintf("<%T>", t)
	}
}

func fieldNames(f *ast.Field) []string {
	if len(f.Names) == 0 {
		return []string{""} // embedded field
	}
	var names []string
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return names
}
