//go:build ignore

// uncovered prints uncovered blocks that lack an unreachable: or untestable: annotation.
// Usage: go run tools/uncovered/main.go [-exclude path]... [coverage.out]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var exempt = regexp.MustCompile(`\b(unreachable|untestable):`)

type excludeList []string

func (e *excludeList) String() string     { return strings.Join(*e, ",") }
func (e *excludeList) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	profile, excludes := parseFlags()
	module := modulePrefix()
	if err := processCoverage(profile, module, excludes); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() (string, excludeList) {
	var excludes excludeList
	flag.Var(&excludes, "exclude", "file path to exclude (repeatable)")
	flag.Parse()
	profile := "coverage.out"
	if flag.NArg() > 0 {
		profile = flag.Arg(0)
	}
	return profile, excludes
}

func processCoverage(profile, module string, excludes excludeList) error {
	f, err := os.Open(profile)
	if err != nil {
		return err
	}
	defer f.Close()

	seen := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		// format: pkg/file.go:startLine.startCol,endLine.endCol numStmts count
		parts := strings.Fields(line)
		if len(parts) != 3 || parts[2] != "0" {
			continue
		}

		loc := parts[0]
		colon := strings.LastIndex(loc, ":")
		if colon < 0 {
			continue
		}
		filePath := loc[:colon]
		lineRange := loc[colon+1:]

		comma := strings.Index(lineRange, ",")
		if comma < 0 {
			continue
		}
		startDot := strings.Index(lineRange[:comma], ".")
		endPart := lineRange[comma+1:]
		endDot := strings.Index(endPart, ".")
		if startDot < 0 || endDot < 0 {
			continue
		}
		startLine, err := strconv.Atoi(lineRange[:startDot])
		if err != nil {
			continue
		}
		endLine, err := strconv.Atoi(endPart[:endDot])
		if err != nil {
			continue
		}

		localPath := strings.TrimPrefix(filePath, module+"/")
		if isExcluded(localPath, excludes) {
			continue
		}

		src, err := os.ReadFile(localPath)
		if err != nil {
			continue
		}
		srcLines := strings.Split(string(src), "\n")
		if startLine < 1 || endLine > len(srcLines) {
			continue
		}
		if hasExempt(srcLines[startLine-1 : endLine]) {
			continue
		}

		var key string
		if startLine == endLine {
			key = fmt.Sprintf("%s:%d", localPath, startLine)
		} else {
			key = fmt.Sprintf("%s:%d-%d", localPath, startLine, endLine)
		}
		if !seen[key] {
			seen[key] = true
			fmt.Println(key)
		}
	}
	return s.Err()
}

func isExcluded(path string, excludes []string) bool {
	for _, e := range excludes {
		if path == e || strings.HasSuffix(path, "/"+e) {
			return true
		}
	}
	return false
}

func hasExempt(block []string) bool {
	for _, l := range block {
		if exempt.MatchString(l) {
			return true
		}
	}
	return false
}

func modulePrefix() string {
	f, err := os.Open("go.mod")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if line := s.Text(); strings.HasPrefix(line, "module ") {
			return strings.Fields(line)[1]
		}
	}
	if err := s.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return ""
}
