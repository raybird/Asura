package omp

import (
	"asura/internal/backend/clisandbox"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// snapshot 2026-09-30：只匯入此 provider 的有效 access credential，不複製續期憑證。
func snapshot(ctx context.Context, path string) (clisandbox.Authentication, error) {
	var auth clisandbox.Authentication
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	source, err := sql.Open("sqlite", u.String())
	if err != nil {
		return auth, err
	}
	defer source.Close()
	target, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return auth, err
	}
	defer target.Close()
	target.SetMaxOpenConns(1)
	schema, err := source.QueryContext(ctx, "SELECT sql FROM sqlite_master WHERE tbl_name LIKE 'auth_%' AND sql IS NOT NULL ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END")
	if err != nil {
		return auth, err
	}
	var statements []string
	for schema.Next() {
		var s string
		if err = schema.Scan(&s); err != nil {
			schema.Close()
			return auth, err
		}
		statements = append(statements, s)
	}
	err = schema.Err()
	schema.Close()
	if err != nil {
		return auth, err
	}
	for _, s := range statements {
		if _, err = target.ExecContext(ctx, s); err != nil {
			return auth, err
		}
	}
	var schemaVersion int
	if err = source.QueryRowContext(ctx, "SELECT version FROM auth_schema_version WHERE id=1").Scan(&schemaVersion); err != nil {
		return auth, err
	}
	if _, err = target.ExecContext(ctx, "INSERT INTO auth_schema_version(id,version) VALUES(1,?)", schemaVersion); err != nil {
		return auth, err
	}
	rows, err := source.QueryContext(ctx, "SELECT * FROM auth_credentials WHERE provider='openai-codex' AND credential_type='oauth' AND disabled_cause IS NULL ORDER BY id")
	if err != nil {
		return auth, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return auth, err
	}
	dataIndex := -1
	for i, c := range columns {
		if c == "data" {
			dataIndex = i
		}
	}
	if dataIndex < 0 {
		return auth, errors.New("unsupported OMP auth schema")
	}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		scan := make([]interface{}, len(columns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err = rows.Scan(scan...); err != nil {
			return auth, err
		}
		raw, ok := values[dataIndex].(string)
		if !ok {
			return auth, errors.New("invalid OMP auth data")
		}
		var credential map[string]json.RawMessage
		if err = json.Unmarshal([]byte(raw), &credential); err != nil {
			return auth, err
		}
		var expiry int64
		var access, refresh string
		if json.Unmarshal(credential["expires"], &expiry) != nil || json.Unmarshal(credential["access"], &access) != nil || access == "" {
			continue
		}
		expires := time.UnixMilli(expiry)
		if !expires.After(time.Now()) {
			continue
		}
		_ = json.Unmarshal(credential["refresh"], &refresh)
		auth.Secrets = append(auth.Secrets, access, refresh)
		credential["refresh"] = json.RawMessage(`""`)
		clean, err := json.Marshal(credential)
		if err != nil {
			return auth, err
		}
		values[dataIndex] = string(clean)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		if _, err = target.ExecContext(ctx, "INSERT INTO auth_credentials VALUES("+placeholders+")", values...); err != nil {
			return auth, err
		}
		if auth.Expiry.IsZero() || expires.Before(auth.Expiry) {
			auth.Expiry = expires
		}
	}
	if err = rows.Err(); err != nil {
		return auth, err
	}
	if auth.Expiry.IsZero() {
		return auth, errors.New("OMP login missing or expired; update original CLI login")
	}
	conn, err := target.Conn(ctx)
	if err != nil {
		return auth, err
	}
	defer conn.Close()
	err = conn.Raw(func(driver interface{}) error {
		serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("SQLite serialization unavailable")
		}
		auth.Payload, err = serializer.Serialize()
		return err
	})
	return auth, err
}
