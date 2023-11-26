package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

var (
	//go:embed schema.sql
	schema string
)

func Str(s string) sql.NullString {
	if s != "" {
		return sql.NullString{String: s, Valid: true}
	}
	return sql.NullString{}
}

func UpsertDatabaseTables(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}
