package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/cover"
)

// blockBetween returns the profile block spanning from the start of first to
// the end of last, both of which must occur in src.
func blockBetween(t *testing.T, src, first, last string) cover.ProfileBlock {
	t.Helper()
	start := strings.Index(src, first)
	end := strings.Index(src, last)
	require.NotEqual(t, -1, start, "first marker %q not found", first)
	require.NotEqual(t, -1, end, "last marker %q not found", last)
	end += len(last)

	position := func(offset int) (line, col int) {
		before := src[:offset]
		return strings.Count(before, "\n") + 1, offset - strings.LastIndex(before, "\n")
	}
	var b cover.ProfileBlock
	b.StartLine, b.StartCol = position(start)
	b.EndLine, b.EndCol = position(end)
	return b
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestSourceExempt(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		first string
		last  string
		want  bool
	}{
		{
			name: "annotation trailing a statement inside the block",
			body: `
	if err != nil {
		return err // untestable: cannot fail
	}
	return nil`,
			first: "return err",
			last:  "cannot fail\n",
			want:  true,
		},
		{
			name: "comment line between the opening brace and the first statement",
			body: `
	if err != nil {
		// untestable: cannot fail
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "annotation on the first of several comment lines",
			body: `
	if err != nil {
		// untestable: cannot fail
		// for reasons that take two lines
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "block comment after the opening brace",
			body: `
	if err != nil {
		/* untestable: cannot fail */
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "comment trailing the opening line",
			body: `
	if err != nil { // untestable: cannot fail
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "comment trailing the last line of a multi-line condition",
			body: `
	if errors.Is(
		err,
		os.ErrNotExist,
	) { // unreachable: checked by the caller
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "comment trailing a case clause",
			body: `
	switch {
	case err != nil: // unreachable: checked by the caller
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "comment trailing a select clause",
			body: `
	select {
	case <-make(chan int): // untestable: never fires
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "comment after the function's opening brace",
			body: `
	// untestable: process entry point
	return err`,
			first: "return err",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "block that starts at the opening brace",
			body: `
	if err != nil { // untestable: cannot fail
		return err
	}
	return nil`,
			first: "{ // untestable",
			last:  "return err\n",
			want:  true,
		},
		{
			name: "no annotation",
			body: `
	if err != nil {
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "annotation above the opening line",
			body: `
	// untestable: cannot fail
	if err != nil {
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "annotation below the block",
			body: `
	if err != nil {
		return err
	}
	// untestable: cannot fail
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "annotation text inside a string literal on the opening line",
			body: `
	if fmt.Sprint("prefix: // unreachable: no") != "" {
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "annotation text inside a string literal in the block",
			body: `
	if err != nil {
		return fmt.Errorf("// untestable: %w", err)
	}
	return nil`,
			first: "return fmt",
			last:  "err)\n",
			want:  false,
		},
		{
			name: "opening line annotation does not reach a later block",
			body: `
	if err != nil { // untestable: cannot fail
		if os.Getenv("X") != "" {
			fmt.Println()
		}
		return err
	}
	return nil`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "opening line annotation does not reach the condition before it",
			body: `
	if err != nil { // untestable: cannot fail
		return err
	}
	return nil`,
			first: "if err != nil",
			last:  "if err != nil ",
			want:  false,
		},
		{
			name: "opening line annotation does not reach the condition of an empty body",
			body: `
	if err != nil { // untestable: cannot fail
	}
	return nil`,
			first: "if err != nil",
			last:  "if err != nil ",
			want:  false,
		},
		{
			name: "else annotation does not reach the if body before it",
			body: `
	if err != nil {
		return err
	} else { // untestable: cannot fail
		return nil
	}`,
			first: "return err",
			last:  "return err\n",
			want:  false,
		},
		{
			name: "comment trailing an else opening line",
			body: `
	if err != nil {
		return err
	} else { // untestable: cannot fail
		return nil
	}`,
			first: "return nil",
			last:  "return nil\n",
			want:  true,
		},
		{
			name: "case annotation does not reach the clause before it",
			body: `
	switch {
	case err == nil:
		return nil
	case err != nil: // unreachable: checked by the caller
		return err
	}
	return nil`,
			first: "return nil",
			last:  "return nil\n",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package p\n\nfunc f(err error) error {" + tt.body + "\n}\n"
			path := filepath.Join(t.TempDir(), "p.go")
			writeFile(t, path, src)

			s, err := parseSource(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, s.exempt(blockBetween(t, src, tt.first, tt.last)))
		})
	}
}

func TestSourceExempt_BlockOutsideFile(t *testing.T) {
	tests := []struct {
		name  string
		block cover.ProfileBlock
	}{
		{"start before the first line", cover.ProfileBlock{StartLine: 0, EndLine: 1, EndCol: 1}},
		{
			"start past the last line",
			cover.ProfileBlock{StartLine: 40, StartCol: 2, EndLine: 41, EndCol: 3},
		},
		{
			"end past the last line",
			cover.ProfileBlock{StartLine: 1, StartCol: 1, EndLine: 41, EndCol: 3},
		},
	}

	path := filepath.Join(t.TempDir(), "p.go")
	writeFile(t, path, "package p\n\n// untestable: stale profile\n")
	s, err := parseSource(path)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, s.exempt(tt.block))
		})
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		excludes []string
		want     bool
	}{
		{"exact match", "cmd/kumolo/main.go", []string{"cmd/kumolo/main.go"}, true},
		{"path suffix", "cmd/kumolo/main.go", []string{"kumolo/main.go"}, true},
		{"partial file name", "cmd/kumolo/main.go", []string{"ain.go"}, false},
		{"no excludes", "cmd/kumolo/main.go", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isExcluded(tt.path, tt.excludes))
		})
	}
}

func TestModulePrefix(t *testing.T) {
	tests := []struct {
		name  string
		goMod string
		want  string
	}{
		{"module line", "// comment\nmodule example.com/m\n\ngo 1.27\n", "example.com/m"},
		{"no module line", "go 1.27\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), tt.goMod)
			t.Chdir(dir)

			assert.Equal(t, tt.want, modulePrefix())
		})
	}

	t.Run("no go.mod", func(t *testing.T) {
		t.Chdir(t.TempDir())

		assert.Empty(t, modulePrefix())
	})
}

const runSource = `package a

func f(err error) error {
	if err != nil {
		return err
	}
	if err == nil { // untestable: cannot fail
		return nil
	}
	if err != nil {
		println()
		return err
	}
	return nil
}
`

// runProfile builds a profile for runSource: the first condition is covered,
// the third condition and the three if bodies are not.
func runProfile(file string) string {
	return fmt.Sprintf(`mode: set
%[1]s:4.2,4.16 1 1
%[1]s:5.3,6.1 1 0
%[1]s:8.3,9.1 1 0
%[1]s:10.2,10.16 1 0
%[1]s:11.3,13.1 2 0
`, file)
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "reports blocks without an annotation",
			profile:    "coverage.out",
			wantStdout: "pkg/a.go:5-6\npkg/a.go:10\npkg/a.go:11-13\n",
		},
		{
			name:       "reads the profile named by the argument",
			profile:    "other.out",
			args:       []string{"other.out"},
			wantStdout: "pkg/a.go:5-6\npkg/a.go:10\npkg/a.go:11-13\n",
		},
		{
			name:    "skips excluded files",
			profile: "coverage.out",
			args:    []string{"-exclude", "pkg/a.go"},
		},
		{
			name:       "fails when the profile is missing",
			profile:    "other.out",
			wantCode:   1,
			wantStderr: "coverage.out",
		},
		{
			name:       "fails on an unknown flag",
			profile:    "coverage.out",
			args:       []string{"-nope"},
			wantCode:   2,
			wantStderr: "flag provided but not defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
			writeFile(t, filepath.Join(dir, "pkg", "a.go"), runSource)
			writeFile(t, filepath.Join(dir, tt.profile), runProfile("example.com/m/pkg/a.go"))
			t.Chdir(dir)

			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)

			assert.Equal(t, tt.wantCode, code)
			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestRun_SourceFileMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "coverage.out"), runProfile("example.com/m/pkg/gone.go"))
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "gone.go")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRun_StdoutWriteFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n")
	writeFile(t, filepath.Join(dir, "pkg", "a.go"), runSource)
	writeFile(t, filepath.Join(dir, "coverage.out"), runProfile("example.com/m/pkg/a.go"))
	t.Chdir(dir)

	var stderr bytes.Buffer
	code := run(nil, failingWriter{}, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "write failed")
}
