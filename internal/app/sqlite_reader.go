package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// A minimal, read-only reader for SQLite database files, enough to scan whole
// tables of a 3x-ui backup (x-ui.db). It follows the file format at
// https://www.sqlite.org/fileformat.html: table b-trees, record format and
// overflow pages. Indexes, WITHOUT ROWID tables and UTF-16 databases are not
// needed and are rejected. Pulling in a full SQLite driver would have added
// several megabytes and a cgo or transpiled-C dependency for this one importer.

var sqliteMagic = []byte("SQLite format 3\x00")

type sqliteFile struct {
	data     []byte
	pageSize int
	usable   int
	tables   map[string]sqliteTable
}

type sqliteTable struct {
	root    int
	columns []string
	// rowidColumn is the INTEGER PRIMARY KEY column whose value is the rowid; the
	// record stores NULL in its place.
	rowidColumn int
}

func isSQLiteFile(data []byte) bool {
	return len(data) >= 100 && bytes.Equal(data[:16], sqliteMagic)
}

func openSQLite(data []byte) (*sqliteFile, error) {
	if !isSQLiteFile(data) {
		return nil, errors.New("not a SQLite database")
	}
	pageSize := int(binary.BigEndian.Uint16(data[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize < 512 || pageSize&(pageSize-1) != 0 {
		return nil, fmt.Errorf("invalid SQLite page size %d", pageSize)
	}
	if encoding := binary.BigEndian.Uint32(data[56:60]); encoding > 1 {
		return nil, errors.New("only UTF-8 SQLite databases are supported")
	}
	file := &sqliteFile{data: data, pageSize: pageSize, usable: pageSize - int(data[20]), tables: map[string]sqliteTable{}}
	if file.usable < 480 {
		return nil, errors.New("invalid SQLite reserved space")
	}
	// sqlite_schema: type, name, tbl_name, rootpage, sql
	rows, err := file.scan(1)
	if err != nil {
		return nil, fmt.Errorf("read SQLite schema: %w", err)
	}
	for _, row := range rows {
		if len(row.values) < 5 {
			continue
		}
		kind, _ := row.values[0].(string)
		name, _ := row.values[1].(string)
		root, _ := row.values[3].(int64)
		sql, _ := row.values[4].(string)
		if kind != "table" || root <= 0 || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		columns, rowid := sqliteColumns(sql)
		if len(columns) == 0 || strings.Contains(strings.ToUpper(sql), "WITHOUT ROWID") {
			continue
		}
		file.tables[strings.ToLower(name)] = sqliteTable{root: int(root), columns: columns, rowidColumn: rowid}
	}
	return file, nil
}

func (f *sqliteFile) hasTable(name string) bool {
	_, ok := f.tables[strings.ToLower(name)]
	return ok
}

// rows returns every row of a table as column name -> value (int64, float64,
// string, []byte or nil). Columns added by ALTER TABLE after a row was written
// are simply absent from that row.
func (f *sqliteFile) rows(name string) ([]map[string]any, error) {
	table, ok := f.tables[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("table %s not found", name)
	}
	raw, err := f.scan(table.root)
	if err != nil {
		return nil, fmt.Errorf("read table %s: %w", name, err)
	}
	result := make([]map[string]any, 0, len(raw))
	for _, row := range raw {
		item := make(map[string]any, len(table.columns))
		for index, column := range table.columns {
			if index < len(row.values) {
				item[column] = row.values[index]
			}
		}
		if table.rowidColumn >= 0 {
			item[table.columns[table.rowidColumn]] = row.rowid
		}
		result = append(result, item)
	}
	return result, nil
}

type sqliteRow struct {
	rowid  int64
	values []any
}

func (f *sqliteFile) page(number int) ([]byte, error) {
	start := (number - 1) * f.pageSize
	if number < 1 || start+f.pageSize > len(f.data) {
		return nil, fmt.Errorf("page %d is outside the file", number)
	}
	return f.data[start : start+f.pageSize], nil
}

// scan walks a table b-tree in rowid order.
func (f *sqliteFile) scan(root int) ([]sqliteRow, error) {
	var rows []sqliteRow
	visited := map[int]bool{}
	var walk func(number int, depth int) error
	walk = func(number int, depth int) error {
		if depth > 64 || visited[number] {
			return errors.New("corrupt b-tree (cycle or too deep)")
		}
		visited[number] = true
		page, err := f.page(number)
		if err != nil {
			return err
		}
		offset := 0
		if number == 1 {
			offset = 100
		}
		if offset+8 > len(page) {
			return errors.New("truncated page header")
		}
		kind := page[offset]
		cells := int(binary.BigEndian.Uint16(page[offset+3 : offset+5]))
		header := 8
		if kind == 0x05 {
			header = 12
		} else if kind != 0x0d {
			return fmt.Errorf("unexpected page type %#x in a table b-tree", kind)
		}
		pointers := offset + header
		if pointers+2*cells > len(page) {
			return errors.New("truncated cell pointer array")
		}
		for index := 0; index < cells; index++ {
			cell := int(binary.BigEndian.Uint16(page[pointers+2*index:]))
			if cell >= len(page) {
				return errors.New("cell pointer outside the page")
			}
			if kind == 0x05 {
				if cell+4 > len(page) {
					return errors.New("truncated interior cell")
				}
				if err := walk(int(binary.BigEndian.Uint32(page[cell:])), depth+1); err != nil {
					return err
				}
				continue
			}
			row, err := f.leafCell(page, cell)
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		if kind == 0x05 {
			return walk(int(binary.BigEndian.Uint32(page[offset+8:])), depth+1)
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return rows, nil
}

func (f *sqliteFile) leafCell(page []byte, cell int) (sqliteRow, error) {
	size, n := sqliteVarint(page[cell:])
	if n == 0 {
		return sqliteRow{}, errors.New("bad payload size")
	}
	cell += n
	rowid, n := sqliteVarint(page[cell:])
	if n == 0 {
		return sqliteRow{}, errors.New("bad rowid")
	}
	cell += n
	payloadSize := int(size)
	if payloadSize < 0 || payloadSize > len(f.data) {
		return sqliteRow{}, errors.New("bad payload size")
	}
	local := f.localPayload(payloadSize)
	if cell+local > len(page) {
		return sqliteRow{}, errors.New("payload outside the page")
	}
	payload := make([]byte, 0, payloadSize)
	payload = append(payload, page[cell:cell+local]...)
	if local < payloadSize {
		if cell+local+4 > len(page) {
			return sqliteRow{}, errors.New("missing overflow pointer")
		}
		next := int(binary.BigEndian.Uint32(page[cell+local:]))
		for guard := 0; len(payload) < payloadSize; guard++ {
			if next == 0 || guard > len(f.data)/f.pageSize {
				return sqliteRow{}, errors.New("broken overflow chain")
			}
			overflow, err := f.page(next)
			if err != nil {
				return sqliteRow{}, err
			}
			next = int(binary.BigEndian.Uint32(overflow))
			chunk := overflow[4:f.usable]
			if remaining := payloadSize - len(payload); len(chunk) > remaining {
				chunk = chunk[:remaining]
			}
			payload = append(payload, chunk...)
		}
	}
	values, err := sqliteRecord(payload)
	if err != nil {
		return sqliteRow{}, err
	}
	return sqliteRow{rowid: rowid, values: values}, nil
}

// localPayload is how much of a table-leaf payload is stored on the page itself.
func (f *sqliteFile) localPayload(size int) int {
	maxLocal := f.usable - 35
	if size <= maxLocal {
		return size
	}
	minLocal := (f.usable-12)*32/255 - 23
	local := minLocal + (size-minLocal)%(f.usable-4)
	if local > maxLocal {
		return minLocal
	}
	return local
}

func sqliteVarint(data []byte) (int64, int) {
	var value uint64
	for index := 0; index < 9 && index < len(data); index++ {
		if index == 8 {
			return int64(value<<8 | uint64(data[index])), 9
		}
		value = value<<7 | uint64(data[index]&0x7f)
		if data[index]&0x80 == 0 {
			return int64(value), index + 1
		}
	}
	return 0, 0
}

func sqliteRecord(payload []byte) ([]any, error) {
	headerSize, n := sqliteVarint(payload)
	if n == 0 || int(headerSize) > len(payload) || headerSize < int64(n) {
		return nil, errors.New("bad record header")
	}
	var types []int64
	for position := n; position < int(headerSize); {
		serial, used := sqliteVarint(payload[position:int(headerSize)])
		if used == 0 {
			return nil, errors.New("bad serial type")
		}
		types = append(types, serial)
		position += used
	}
	values := make([]any, 0, len(types))
	body := payload[headerSize:]
	for _, serial := range types {
		var size int
		switch {
		case serial == 0, serial == 8, serial == 9:
			size = 0
		case serial >= 1 && serial <= 4:
			size = int(serial)
		case serial == 5:
			size = 6
		case serial == 6, serial == 7:
			size = 8
		case serial >= 12:
			size = int((serial - 12) / 2)
		default:
			return nil, fmt.Errorf("reserved serial type %d", serial)
		}
		if size > len(body) {
			return nil, errors.New("record body is truncated")
		}
		field := body[:size]
		body = body[size:]
		switch {
		case serial == 0:
			values = append(values, nil)
		case serial == 8:
			values = append(values, int64(0))
		case serial == 9:
			values = append(values, int64(1))
		case serial == 7:
			values = append(values, math.Float64frombits(binary.BigEndian.Uint64(field)))
		case serial >= 1 && serial <= 6:
			var value int64
			for _, b := range field {
				value = value<<8 | int64(b)
			}
			// sign-extend from the field width
			shift := uint(64 - 8*len(field))
			values = append(values, value<<shift>>shift)
		case serial%2 == 0:
			values = append(values, append([]byte(nil), field...))
		default:
			values = append(values, string(field))
		}
	}
	return values, nil
}

// sqliteColumns extracts the column names from a CREATE TABLE statement and
// finds an INTEGER PRIMARY KEY column (stored as the rowid).
func sqliteColumns(sql string) ([]string, int) {
	open := strings.IndexByte(sql, '(')
	end := strings.LastIndexByte(sql, ')')
	if open < 0 || end <= open {
		return nil, -1
	}
	var parts []string
	depth, start := 0, open+1
	var quote byte
	for index := open + 1; index < end; index++ {
		c := sql[index]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '`' || c == '"' || c == '\'' || c == '[':
			quote = c
			if c == '[' {
				quote = ']'
			}
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, sql[start:index])
			start = index + 1
		}
	}
	parts = append(parts, sql[start:end])
	var columns []string
	rowid := -1
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		upper := strings.ToUpper(part)
		if strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "CHECK") || strings.HasPrefix(upper, "FOREIGN KEY") {
			continue
		}
		name, rest := part, ""
		if strings.ContainsAny(part[:1], "`\"[") {
			closer := part[0]
			if closer == '[' {
				closer = ']'
			}
			if stop := strings.IndexByte(part[1:], closer); stop >= 0 {
				name, rest = part[1:stop+1], part[stop+2:]
			}
		} else if fields := strings.Fields(part); len(fields) > 0 {
			name, rest = fields[0], strings.TrimPrefix(part, fields[0])
		}
		restUpper := strings.ToUpper(strings.TrimSpace(rest))
		if strings.HasPrefix(restUpper, "INTEGER") && strings.Contains(restUpper, "PRIMARY KEY") {
			rowid = len(columns)
		}
		columns = append(columns, name)
	}
	return columns, rowid
}
