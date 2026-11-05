package hosts

import (
	"fmt"
	"strings"
)

const (
	diffContext = 3
	// lcsMaxCells bounds the LCS table; beyond it the changed region is shown
	// as all removed then all added.
	lcsMaxCells = 1 << 20
)

func diffSplit(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	if n == 0 || m == 0 || (n+1)*(m+1) > lcsMaxCells {
		ops := make([]diffOp, 0, n+m)
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// t[i][j] = LCS length of a[i:], b[j:]
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else if t[i+1][j] >= t[i][j+1] {
				t[i][j] = t[i+1][j]
			} else {
				t[i][j] = t[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// Diff returns a unified-style diff (one hunk around the changed region) of
// old and new, or "" when their lines are equal.
func Diff(name string, old, new []byte) string {
	a, b := diffSplit(old), diffSplit(new)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	if p == len(a) && p == len(b) {
		if string(old) != string(new) {
			return fmt.Sprintf("--- %s\n+++ %s\n(only line endings or the final newline differ)\n", name, name)
		}
		return ""
	}
	start := p - diffContext
	if start < 0 {
		start = 0
	}
	trail := s
	if trail > diffContext {
		trail = diffContext
	}
	aEnd, bEnd := len(a)-s, len(b)-s
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s (after)\n", name, name)
	oldCount := aEnd + trail - start
	newCount := bEnd + trail - start
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", hunkStart(start, oldCount), oldCount, hunkStart(start, newCount), newCount)
	for _, l := range a[start:p] {
		sb.WriteString(" " + l + "\n")
	}
	for _, op := range lcsOps(a[p:aEnd], b[p:bEnd]) {
		sb.WriteByte(op.kind)
		sb.WriteString(op.text + "\n")
	}
	for _, l := range a[aEnd : aEnd+trail] {
		sb.WriteString(" " + l + "\n")
	}
	return sb.String()
}

// hunkStart follows the unified format: an empty range starts at the line before.
func hunkStart(start, count int) int {
	if count == 0 {
		return start
	}
	return start + 1
}
