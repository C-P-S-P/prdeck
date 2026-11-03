package mdrender

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	extast "github.com/yuin/goldmark/extension/ast"
)

type cell struct {
	runs  []run
	align extast.Alignment
}

// table draws a GitHub table that fits in w columns. When the natural widths don't fit,
// every column keeps room for its longest word (up to a limit) and the rest of the
// space goes to the columns that want it most; cell text then wraps.
func (r *renderer) table(t *extast.Table, w int) []string {
	var rows [][]cell
	header := 0
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		_, isHead := row.(*extast.TableHeader)
		if isHead {
			header = 1
		}
		var cells []cell
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			tc, ok := c.(*extast.TableCell)
			if !ok {
				continue
			}
			base := style{fg: cText}
			if isHead {
				base = style{fg: cBright, bold: true}
			}
			cells = append(cells, cell{runs: r.inline(tc, base), align: tc.Alignment})
		}
		rows = append(rows, cells)
	}
	ncol := 0
	for _, row := range rows {
		ncol = max(ncol, len(row))
	}
	if ncol == 0 {
		return nil
	}

	natural, minimum := make([]int, ncol), make([]int, ncol)
	for _, row := range rows {
		for i, c := range row {
			full, longest := 0, 0
			for _, p := range pieces(c.runs) {
				full += p.width
				if !p.space {
					longest = max(longest, p.width)
				}
			}
			natural[i] = max(natural[i], full)
			minimum[i] = max(minimum[i], min(longest, 18))
		}
	}
	for i := range minimum {
		minimum[i] = max(minimum[i], 3)
		natural[i] = max(natural[i], minimum[i])
	}
	widths := fitColumns(natural, minimum, w-(3*ncol+1))

	border := style{fg: cBorder}
	edge := func(left, mid, right string) string {
		parts := make([]string, ncol)
		for i, cw := range widths {
			parts[i] = strings.Repeat("─", cw+2)
		}
		return paint(left+strings.Join(parts, mid)+right, border, false)
	}
	bar := paint("│", border, false)

	// Lay every cell out first: when some body row wraps, rows get dividers so it is
	// clear which lines belong together.
	laid := make([][][]string, len(rows))
	wrapped := false
	for ri, row := range rows {
		laid[ri] = make([][]string, ncol)
		for i := 0; i < ncol; i++ {
			if i < len(row) {
				laid[ri][i] = r.wrap(row[i].runs, widths[i])
			} else {
				laid[ri][i] = []string{""}
			}
			if ri >= header && len(laid[ri][i]) > 1 {
				wrapped = true
			}
		}
	}

	out := []string{edge("┌", "┬", "┐")}
	for ri, row := range rows {
		cells := laid[ri]
		height := 1
		for i := 0; i < ncol; i++ {
			height = max(height, len(cells[i]))
		}
		for k := 0; k < height; k++ {
			var b strings.Builder
			b.WriteString(bar)
			for i := 0; i < ncol; i++ {
				line := ""
				if k < len(cells[i]) {
					line = cells[i][k]
				}
				align := extast.AlignNone
				if i < len(row) {
					align = row[i].align
				}
				b.WriteString(" " + pad(line, widths[i], align) + " " + bar)
			}
			out = append(out, b.String())
		}
		if ri < len(rows)-1 && (ri == header-1 || wrapped) {
			out = append(out, edge("├", "┼", "┤"))
		}
	}
	return append(out, edge("└", "┴", "┘"))
}

// fitColumns shrinks natural column widths to fit avail, never below minimum when that
// is possible.
func fitColumns(natural, minimum []int, avail int) []int {
	n := len(natural)
	minimum = append([]int(nil), minimum...)
	sum := func(xs []int) int {
		t := 0
		for _, x := range xs {
			t += x
		}
		return t
	}
	if sum(natural) <= avail {
		return natural
	}
	// Short columns ("Patch %", "50.74%") keep their full width as long as the long
	// columns can still get their minimum; only long text wraps.
	widths := append([]int(nil), minimum...)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return natural[order[a]] < natural[order[b]] })
	for _, i := range order {
		if natural[i] > 16 {
			break
		}
		if sum(widths)-widths[i]+natural[i] <= avail {
			widths[i] = natural[i]
			minimum[i] = natural[i]
		}
	}
	if sum(minimum) >= avail {
		// Not even the longest words fit: share the space evenly.
		each := max(avail/n, 3)
		for i := range widths {
			widths[i] = min(each, natural[i])
		}
		return widths
	}
	extra := avail - sum(minimum)
	want := 0
	for i := range natural {
		want += natural[i] - minimum[i]
	}
	for i := range widths {
		if want > 0 {
			widths[i] += (natural[i] - minimum[i]) * extra / want
		}
	}
	// Hand out what rounding left over, to the columns still short of their width.
	for left := avail - sum(widths); left > 0; {
		gave := false
		for i := 0; i < n && left > 0; i++ {
			if widths[i] < natural[i] {
				widths[i]++
				left--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return widths
}

func pad(s string, w int, align extast.Alignment) string {
	gap := max(w-ansi.StringWidth(s), 0)
	switch align {
	case extast.AlignRight:
		return strings.Repeat(" ", gap) + s
	case extast.AlignCenter:
		return strings.Repeat(" ", gap/2) + s + strings.Repeat(" ", gap-gap/2)
	}
	return s + strings.Repeat(" ", gap)
}
