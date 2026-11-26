// Command apicheck compares the current package with the reviewed source snapshot.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const packagePath = "github.com/angru/exness-go"
const baselineRevision = "73e62179739ce2e28318f5916ce51b5e5fbeb99f"

type listing struct {
	Dir			string
	GoFiles, CgoFiles	[]string
}

func load(dir string, files []string, imp types.Importer) (*types.Package, error) {
	fset := token.NewFileSet()
	syntax := make([]*ast.File, 0, len(files))
	for _, name := range files {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		syntax = append(syntax, f)
	}
	conf := types.Config{Importer: imp}
	return conf.Check(packagePath, fset, syntax, nil)
}

func qualifier(p *types.Package) string {
	if p.Path() == packagePath {
		return ""
	}
	return p.Path()
}

func typeText(t types.Type) string { return types.TypeString(t, qualifier) }

func signature(s *types.Signature) string {
	tuple := func(t *types.Tuple) *types.Tuple {
		vars := make([]*types.Var, t.Len())
		for i := range vars {
			vars[i] = types.NewVar(token.NoPos, nil, "", t.At(i).Type())
		}
		return types.NewTuple(vars...)
	}
	return typeText(types.NewSignatureType(nil, nil, nil, tuple(s.Params()), tuple(s.Results()), s.Variadic()))
}

func objectText(o types.Object) string {
	switch v := o.(type) {
	case *types.Func:
		return "func " + signature(v.Type().(*types.Signature))
	case *types.Const:
		return "const " + typeText(v.Type()) + " = " + v.Val().ExactString()
	case *types.Var:
		return "var " + typeText(v.Type())
	case *types.TypeName:
		if v.IsAlias() {
			return "alias " + typeText(types.Unalias(v.Type()))
		}
		return "type " + typeText(v.Type().Underlying())
	}
	return fmt.Sprintf("%T %s", o, typeText(o.Type()))
}

func allFieldsExported(s *types.Struct) bool {
	for i := 0; i < s.NumFields(); i++ {
		if !s.Field(i).Exported() {
			return false
		}
	}
	return true
}

func compareStruct(name string, old, cur *types.Struct, issues *[]string) {
	report := func(s string) { *issues = append(*issues, name+": "+s) }
	fields := make(map[string]int)
	for i := 0; i < cur.NumFields(); i++ {
		if cur.Field(i).Exported() {
			fields[cur.Field(i).Name()] = i
		}
	}
	for i := 0; i < old.NumFields(); i++ {
		f := old.Field(i)
		if !f.Exported() {
			continue
		}
		j, ok := fields[f.Name()]
		if !ok {
			report("field " + f.Name() + " removed")
			continue
		}
		g := cur.Field(j)
		if typeText(f.Type()) != typeText(g.Type()) || f.Embedded() != g.Embedded() || old.Tag(i) != cur.Tag(j) {
			report("field " + f.Name() + " changed")
		}
		if i != j && allFieldsExported(old) {
			report("field " + f.Name() + " moved (unkeyed literals)")
		}
	}
	if allFieldsExported(old) && old.NumFields() != cur.NumFields() {
		report("field count changed (unkeyed literals)")
	}
	if types.Comparable(old) != types.Comparable(cur) {
		report("comparability changed")
	}
}

func compare(old, cur *types.Package) []string {
	var issues []string
	for _, name := range old.Scope().Names() {
		if !ast.IsExported(name) {
			continue
		}
		a, b := old.Scope().Lookup(name), cur.Scope().Lookup(name)
		if b == nil {
			issues = append(issues, name+": removed")
			continue
		}
		at, aok := a.(*types.TypeName)
		bt, bok := b.(*types.TypeName)
		if aok && bok && !at.IsAlias() && !bt.IsAlias() {
			as, asok := at.Type().Underlying().(*types.Struct)
			bs, bsok := bt.Type().Underlying().(*types.Struct)
			if asok && bsok {
				compareStruct(name, as, bs, &issues)
			} else if typeText(at.Type().Underlying()) != typeText(bt.Type().Underlying()) {
				issues = append(issues, name+": underlying type changed: "+typeText(at.Type().Underlying())+" -> "+typeText(bt.Type().Underlying()))
			}
			for _, ptr := range []bool{false, true} {
				var x, y types.Type = at.Type(), bt.Type()
				label := name
				if ptr {
					x, y = types.NewPointer(x), types.NewPointer(y)
					label = "*" + name
				}
				am, bm := types.NewMethodSet(x), types.NewMethodSet(y)
				for i := 0; i < am.Len(); i++ {
					m := am.At(i).Obj().(*types.Func)
					if !m.Exported() {
						continue
					}
					n := bm.Lookup(cur, m.Name())
					symbol := label + "." + m.Name()
					if n == nil {
						issues = append(issues, symbol+": removed from method set")
					} else if signature(m.Type().(*types.Signature)) != signature(n.Obj().Type().(*types.Signature)) {
						issues = append(issues, symbol+": signature changed")
					}
				}
			}
		} else if objectText(a) != objectText(b) {
			issues = append(issues, name+": changed: "+objectText(a)+" -> "+objectText(b))
		}
	}
	sort.Strings(issues)
	return issues
}

func run() error {
	cmd := exec.Command("go", "list", "-json", ".")
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list: %w", err)
	}
	var info listing
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	if len(info.CgoFiles) > 0 {
		return fmt.Errorf("cgo files are not supported")
	}
	cmd = exec.Command("go", "list", "-export", "-deps", "-f", "{{if .Export}}{{.ImportPath}} {{.Export}}{{end}}", ".")
	data, err = cmd.Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("go list exports: %s", e.Stderr)
		}
		return err
	}
	exports := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			exports[parts[0]] = parts[1]
		}
	}
	imp := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		p, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(p)
	})
	dir := filepath.Join(info.Dir, "testdata", "api-baseline")
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("empty baseline")
	}
	files := make([]string, len(matches))
	for i, f := range matches {
		files[i] = filepath.Base(f)
	}
	old, err := load(dir, files, imp)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	cur, err := load(info.Dir, info.GoFiles, imp)
	if err != nil {
		return fmt.Errorf("current: %w", err)
	}
	issues := compare(old, cur)
	if len(issues) > 0 {
		return fmt.Errorf("public API incompatible with %s:\n%s", baselineRevision, strings.Join(issues, "\n"))
	}
	fmt.Println("Public API compatible with", baselineRevision)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
