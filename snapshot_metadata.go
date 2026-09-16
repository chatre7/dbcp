package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type databaseIdentity struct {
	Server   string `json:"server"`
	Database string `json:"database"`
}

type objectTimestamp struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

type snapshotMetadata struct {
	Identity databaseIdentity
	Objects  []objectTimestamp
}

// SQL Server catalog dates have no timezone. Preserve their local wall-clock
// representation rather than assigning UTC to them in the driver or serializer.
const snapshotTimestampsQuery = `
SELECT s.name, o.name, o.type,
       CONVERT(varchar(23), o.create_date, 121),
       CONVERT(varchar(23), o.modify_date, 121)
FROM sys.objects AS o
JOIN sys.schemas AS s ON s.schema_id = o.schema_id
WHERE o.is_ms_shipped = 0
  AND o.type IN ('U', 'P', 'RF', 'V', 'FN', 'IF', 'TF', 'FS', 'FT', 'SN')
ORDER BY s.name, o.name`

func loadSnapshotMetadata(ctx context.Context, db *sql.DB, loaded *schema) (*snapshotMetadata, error) {
	metadata := &snapshotMetadata{}
	if err := db.QueryRowContext(ctx,
		`SELECT CONVERT(nvarchar(128), SERVERPROPERTY('ServerName')), DB_NAME()`).Scan(
		&metadata.Identity.Server, &metadata.Identity.Database); err != nil {
		return nil, fmt.Errorf("read snapshot database identity: %w", err)
	}
	if strings.TrimSpace(metadata.Identity.Server) == "" || strings.TrimSpace(metadata.Identity.Database) == "" {
		return nil, fmt.Errorf("read snapshot database identity: server and database names must be available and nonempty")
	}

	// Compare names and types, never dates. This catches inventory changes during
	// capture, but does not make the separate catalog reads a transactional snapshot.
	remaining := make(map[objectName]string, len(loaded.tables)+len(loaded.objects))
	for name := range loaded.tables {
		remaining[name] = "U"
	}
	for name, object := range loaded.objects {
		if _, exists := remaining[name]; exists {
			return nil, fmt.Errorf("snapshot object inventory changed during capture: duplicate %s; retry after DDL completes", name)
		}
		remaining[name] = object.typeCode
	}
	metadata.Objects = make([]objectTimestamp, 0, len(remaining))
	rows, err := db.QueryContext(ctx, snapshotTimestampsQuery)
	if err != nil {
		return nil, fmt.Errorf("read snapshot object timestamps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stamp objectTimestamp
		if err := rows.Scan(&stamp.Schema, &stamp.Name, &stamp.Type, &stamp.CreatedAt, &stamp.ModifiedAt); err != nil {
			return nil, fmt.Errorf("decode snapshot object timestamps: %w", err)
		}
		stamp.Type = strings.TrimSpace(stamp.Type)
		name := objectName{schema: stamp.Schema, name: stamp.Name}
		if stamp.Schema == "" || stamp.Name == "" || stamp.Type == "" || stamp.CreatedAt == "" || stamp.ModifiedAt == "" {
			return nil, fmt.Errorf("incomplete snapshot object timestamp metadata for %s", name)
		}
		if kind, exists := remaining[name]; !exists || kind != stamp.Type {
			return nil, fmt.Errorf("snapshot object inventory changed during capture at %s (type %s); retry after DDL completes", name, stamp.Type)
		}
		delete(remaining, name)
		metadata.Objects = append(metadata.Objects, stamp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read snapshot object timestamp rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close snapshot object timestamp rows: %w", err)
	}
	if len(remaining) != 0 {
		return nil, fmt.Errorf("snapshot object inventory changed during capture: timestamps missing for %d loaded objects; retry after DDL completes", len(remaining))
	}
	return metadata, nil
}
