package snmp

// Tests for the multi-column walk-and-join helper (table.go).

import (
	"context"
	"slices"
	"testing"

	"github.com/gosnmp/gosnmp"
)

// A synthetic ifTable-shaped table: two columns under one enterprise
// subtree, indexed by four-octet suffixes. Column B is sparse — it only
// exists for rows 1 and 3, proving absent values join as nil.
var (
	fixColA = "1.3.6.1.4.1.99.1.1" // e.g. an ifIndex-like column
	fixColB = "1.3.6.1.4.1.99.1.2" // e.g. a status column, sparse
)

func TestWalkTableColumnsJoinsBySuffix(t *testing.T) {
	a := newFakeAgent(t, []fixtureEntry{
		{oid: fixColA + ".10.0.0.1", typ: gosnmp.Integer, value: 101},
		{oid: fixColA + ".10.0.0.2", typ: gosnmp.Integer, value: 102},
		{oid: fixColA + ".10.0.0.3", typ: gosnmp.Integer, value: 103},
		{oid: fixColB + ".10.0.0.1", typ: gosnmp.Integer, value: 1},
		{oid: fixColB + ".10.0.0.3", typ: gosnmp.Integer, value: 1},
	})

	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	rows, err := c.WalkTableColumns(context.Background(), fixColA, fixColB)
	if err != nil {
		t.Fatalf("WalkTableColumns: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(rows), rows)
	}

	wantIndex := [][]int{{10, 0, 0, 1}, {10, 0, 0, 2}, {10, 0, 0, 3}}
	wantA := []any{101, 102, 103}
	wantB := []any{1, nil, 1} // row 2 has no column-B instance
	for i, r := range rows {
		if !slices.Equal(r.Index, wantIndex[i]) {
			t.Errorf("row %d index = %v, want %v", i, r.Index, wantIndex[i])
		}
		if len(r.Values) != 2 {
			t.Fatalf("row %d has %d values, want 2 (one per column)", i, len(r.Values))
		}
		if r.Values[0] != wantA[i] {
			t.Errorf("row %d col A = %v, want %v", i, r.Values[0], wantA[i])
		}
		if r.Values[1] != wantB[i] {
			t.Errorf("row %d col B = %v, want %v", i, r.Values[1], wantB[i])
		}
	}
}

func TestWalkTableColumnsRowOnlyInLaterColumn(t *testing.T) {
	// An index that exists ONLY in column B must still produce a row,
	// with nil for the column-A slot.
	a := newFakeAgent(t, []fixtureEntry{
		{oid: fixColA + ".10.0.0.1", typ: gosnmp.Integer, value: 101},
		{oid: fixColB + ".10.0.0.9", typ: gosnmp.Integer, value: 7},
	})

	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	rows, err := c.WalkTableColumns(context.Background(), fixColA, fixColB)
	if err != nil {
		t.Fatalf("WalkTableColumns: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if !slices.Equal(rows[1].Index, []int{10, 0, 0, 9}) || rows[1].Values[0] != nil || rows[1].Values[1] != 7 {
		t.Errorf("late-column-only row = %+v, want index [10 0 0 9], A=nil, B=7", rows[1])
	}
}

func TestWalkTableColumnsEmptyTable(t *testing.T) {
	a := newFakeAgent(t, nil)
	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	rows, err := c.WalkTableColumns(context.Background(), fixColA, fixColB)
	if err != nil {
		t.Fatalf("WalkTableColumns: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestWalkTableColumnsNoColsErrors(t *testing.T) {
	a := newFakeAgent(t, nil)
	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if _, err := c.WalkTableColumns(context.Background()); err == nil {
		t.Fatal("expected error for empty column list")
	}
}

func TestWalkTableColumnsPreCancelledContext(t *testing.T) {
	a := newFakeAgent(t, nil)
	c, err := NewClient(context.Background(), Config{
		Target: "127.0.0.1", Port: uint16(a.port()), Community: "public",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WalkTableColumns(ctx, fixColA); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
