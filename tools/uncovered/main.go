// uncovered prints uncovered blocks that lack an unreachable: or untestable: annotation.
// Usage: go run ./tools/uncovered [-exclude path]... [coverage.out]
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/cover"
)

var exempt = regexp.MustCompile(`\b(unreachable|untestable):`)

type excludeList []string

func (e *excludeList) String() string     { return strings.Join(*e, ",") }
func (e *excludeList) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	// untestable: process entry point
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var excludes excludeList
	fs := flag.NewFlagSet("uncovered", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&excludes, "exclude", "file path to exclude (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	profile := "coverage.out"
	if fs.NArg() > 0 {
		profile = fs.Arg(0)
	}
	if err := report(stdout, profile, modulePrefix(), excludes); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func report(w io.Writer, profile, module string, excludes excludeList) error {
	profiles, err := cover.ParseProfiles(profile)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		localPath := strings.TrimPrefix(p.FileName, module+"/")
		if isExcluded(localPath, excludes) {
			continue
		}
		src, err := parseSource(localPath)
		if err != nil {
			return err
		}
		for _, b := range p.Blocks {
			if b.Count != 0 || src.exempt(b) {
				continue
			}
			lines := strconv.Itoa(b.StartLine)
			if b.EndLine != b.StartLine {
				lines += "-" + strconv.Itoa(b.EndLine)
			}
			if _, err := fmt.Fprintf(w, "%s:%s\n", localPath, lines); err != nil {
				return err
			}
		}
	}
	return nil
}

func isExcluded(path string, excludes []string) bool {
	for _, e := range excludes {
		if path == e || strings.HasSuffix(path, "/"+e) {
			return true
		}
	}
	return false
}

// opener is the brace or case colon that opens a statement list, paired with
// the list's first statement (the opener itself when the list is empty).
type opener struct {
	open, first token.Pos
}

type source struct {
	file        *token.File
	annotations []token.Pos
	openers     []opener
}

func parseSource(path string) (*source, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	s := &source{file: fset.File(f.FileStart)}
	for _, group := range f.Comments {
		for _, c := range group.List {
			if exempt.MatchString(c.Text) {
				s.annotations = append(s.annotations, c.Pos())
			}
		}
	}
	for n := range ast.Preorder(f) {
		switch n := n.(type) {
		case *ast.BlockStmt:
			s.addOpener(n.Lbrace, n.List)
		case *ast.CaseClause:
			s.addOpener(n.Colon, n.Body)
		case *ast.CommClause:
			s.addOpener(n.Colon, n.Body)
		}
	}
	return s, nil
}

func (s *source) addOpener(open token.Pos, list []ast.Stmt) {
	first := open
	if len(list) > 0 {
		first = list[0].Pos()
	}
	s.openers = append(s.openers, opener{open: open, first: first})
}

// exempt reports whether an annotation comment belongs to the block. A profile
// block starts at its first statement, so a block that begins a statement list
// is widened to the list's opener: that covers a comment trailing the opening
// line and comment lines between the opener and the statement. A comment after
// the block's end counts only up to the next opener on that line, where it
// belongs to the list being opened.
func (s *source) exempt(b cover.ProfileBlock) bool {
	start, ok := s.pos(b.StartLine, b.StartCol)
	if !ok {
		return false
	}
	end, ok := s.pos(b.EndLine, b.EndCol)
	if !ok {
		return false
	}
	for _, o := range s.openers {
		if o.open <= start && start <= o.first {
			start = o.open
			break
		}
	}
	return slices.ContainsFunc(s.annotations, func(a token.Pos) bool {
		if a < start || s.file.Line(a) > b.EndLine {
			return false
		}
		return !slices.ContainsFunc(s.openers, func(o opener) bool {
			return end <= o.open && o.open < a
		})
	})
}

func (s *source) pos(line, col int) (token.Pos, bool) {
	if line < 1 || line > s.file.LineCount() {
		return token.NoPos, false
	}
	return s.file.LineStart(line) + token.Pos(col-1), true
}

func modulePrefix() string {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(goMod)) {
		if strings.HasPrefix(line, "module ") {
			return strings.Fields(line)[1]
		}
	}
	return ""
}
