package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func checked(t *testing.T, source string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", "package example\n"+source, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check(packagePath, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestComparison(t *testing.T) {
	baseline := `
 type Request struct { Name string; Count int }
 type Opaque struct { Name string; private int }
 type Handler interface { Handle(string) error }
 type Client struct{}
 func (Client) Fetch(Request) (string, error) { return "", nil }
 `
	for _, tc := range []struct {
		name, source, want string
	}{
		{"addition", baseline + "\nfunc Added() {}", ""},
		{"keyed-only addition", strings.Replace(baseline, "private int", "private int; Extra bool", 1), ""},
		{"unkeyed addition", strings.Replace(baseline, "Count int", "Count int; Extra bool", 1), "Request: field count changed"},
		{"field type", strings.Replace(baseline, "Count int", "Count string", 1), "Request: field Count changed"},
		{"interface", strings.Replace(baseline, "Handle(string) error", "Handle(string) error; Reset()", 1), "Handler: underlying type changed"},
		{"receiver", strings.Replace(baseline, "(Client) Fetch", "(*Client) Fetch", 1), "Client.Fetch: removed"},
		{"result", strings.Replace(baseline, "(string, error) { return", "(int, error) { return 0, nil }; func other() (string, error) { return", 1), "Fetch: signature changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(compare(checked(t, baseline), checked(t, tc.source)), "\n")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want substring %q", got, tc.want)
			}
		})
	}
}
