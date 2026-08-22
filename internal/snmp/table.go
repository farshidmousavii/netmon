package snmp

// Multi-column table walks: walk several columns of one SNMP table and
// join the results by shared index suffix, so callers get rows instead of
// per-column varbind lists. Additive only — the legacy SnmpWalk in
// snmp.go is untouched.

import (
	"context"
	"fmt"
	"strings"
)

// TableRow is one joined row of a multi-column table walk: the index
// suffix shared by every column instance of that row, plus one value per
// requested column (nil when the agent did not serve that column for the
// row — common for sparse tables).
type TableRow struct {
	Index []int
	// Values is parallel to the cols argument passed to WalkTableColumns.
	Values []any
}

// WalkTableColumns walks each column OID of one table and joins the
// results by identical OID suffix. Rows are returned in first-seen order
// (the first column's walk order, with indexes seen only in later columns
// appended in their own order) — deterministic for a given agent.
//
// A column whose walk fails aborts the whole call: partial tables would
// silently look like sparse data.
func (c *Client) WalkTableColumns(ctx context.Context, cols ...string) ([]TableRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("walk table columns cancelled: %w", err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("walk table columns: no columns requested")
	}

	rows := make(map[string]*TableRow, 64)
	var order []string // first-seen suffix keys

	for ci, col := range cols {
		vbs, err := c.WalkTable(ctx, col)
		if err != nil {
			return nil, fmt.Errorf("walk column %s: %w", col, err)
		}
		for _, vb := range vbs {
			key := strings.Join(toStrings(vb.Suffix), ".")
			tr, ok := rows[key]
			if !ok {
				tr = &TableRow{
					Index:  append([]int(nil), vb.Suffix...),
					Values: make([]any, len(cols)),
				}
				rows[key] = tr
				order = append(order, key)
			}
			tr.Values[ci] = vb.Value
		}
	}

	out := make([]TableRow, 0, len(order))
	for _, k := range order {
		out = append(out, *rows[k])
	}
	return out, nil
}
