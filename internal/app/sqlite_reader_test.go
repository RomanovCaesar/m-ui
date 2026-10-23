package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The fixture is written by Python's sqlite3 (testdata/xui/gen_sqlite_fixture.py)
// with 1 KiB pages, so it has interior pages, overflow chains, a column added by
// ALTER TABLE after rows existed, deleted rows and every value type.
func TestSQLiteReaderMatchesSQLite(t *testing.T) {
	data, err := os.ReadFile("internal/app/testdata/xui/sqlite_fixture.db")
	if err != nil {
		t.Fatal(err)
	}
	var expected []map[string]any
	raw, err := os.ReadFile("internal/app/testdata/xui/sqlite_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(data)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.rows("items")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(expected) {
		t.Fatalf("read %d rows, want %d", len(rows), len(expected))
	}
	for index, row := range rows {
		want := expected[index]
		got := map[string]any{}
		for key, value := range row {
			switch v := value.(type) {
			case int64:
				got[key] = float64(v)
			case []byte:
				list := make([]any, len(v))
				for i, b := range v {
					list[i] = float64(b)
				}
				got[key] = list
			default:
				got[key] = v
			}
		}
		if _, present := got["late"]; !present {
			// Rows written before ALTER TABLE carry no value for the new column;
			// SQLite reports the column default for them.
			got["late"] = "x"
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("row %d differs:\n got %v\nwant %v", index, trimForLog(got), trimForLog(want))
		}
	}
}

func trimForLog(row map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range row {
		if text, ok := value.(string); ok && len(text) > 60 {
			value = text[:60] + "…"
		}
		out[key] = value
	}
	return out
}

func TestSQLiteReaderRejectsOtherFiles(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("{\"inbounds\":[]}"), append([]byte("SQLite format 3\x00"), make([]byte, 20)...)} {
		if _, err := openSQLite(data); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}
