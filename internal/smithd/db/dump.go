package db

import (
	"fmt"
	"strings"
)

// dumpTables lists the tables that participate in dump/restore, in FK-safe
// order (parents before children). schema_version is intentionally excluded
// since golang-migrate owns it.
var dumpTables = []string{"applications", "environments", "versions", "policies", "deployments"}

// TableDump holds a generic snapshot of a single table's rows.
type TableDump struct {
	Table string           `json:"table"`
	Rows  []map[string]any `json:"rows"`
}

// Dump exports all rows from every table in dumpTables, in order.
func (db *DB) Dump() ([]TableDump, error) {
	dumps := make([]TableDump, 0, len(dumpTables))

	for _, table := range dumpTables {
		rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s", table))
		if err != nil {
			return nil, fmt.Errorf("failed to query table %s: %w", table, err)
		}

		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to get columns for table %s: %w", table, err)
		}

		var tableRows []map[string]any
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}

			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan row for table %s: %w", table, err)
			}

			row := make(map[string]any, len(cols))
			for i, col := range cols {
				v := values[i]
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				row[col] = v
			}
			tableRows = append(tableRows, row)
		}

		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to read rows for table %s: %w", table, err)
		}
		rows.Close()

		dumps = append(dumps, TableDump{Table: table, Rows: tableRows})
	}

	return dumps, nil
}

// EnsureEmpty returns an error if any table in dumpTables already has rows.
// Load has no upsert/conflict-resolution logic, so it must only ever be
// applied to a freshly-migrated, schema-only database.
func (db *DB) EnsureEmpty() error {
	for _, table := range dumpTables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil {
			return fmt.Errorf("failed to count rows in table %s: %w", table, err)
		}
		if count > 0 {
			return fmt.Errorf("table %s is not empty (%d rows)", table, count)
		}
	}
	return nil
}

// Load inserts the given table dumps, in the order provided, into the
// database. It relies on DB.Exec's automatic ?-to-$N placeholder rebinding,
// so it works unmodified against both SQLite and PostgreSQL.
func (db *DB) Load(dumps []TableDump) error {
	for _, dump := range dumps {
		for _, row := range dump.Rows {
			cols := make([]string, 0, len(row))
			placeholders := make([]string, 0, len(row))
			values := make([]any, 0, len(row))
			for col, val := range row {
				cols = append(cols, col)
				placeholders = append(placeholders, "?")
				values = append(values, val)
			}

			query := fmt.Sprintf(
				"INSERT INTO %s (%s) VALUES (%s)",
				dump.Table,
				strings.Join(cols, ", "),
				strings.Join(placeholders, ", "),
			)

			if _, err := db.Exec(query, values...); err != nil {
				return fmt.Errorf("failed to insert row into table %s: %w", dump.Table, err)
			}
		}
	}
	return nil
}
