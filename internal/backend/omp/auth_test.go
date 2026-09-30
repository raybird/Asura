package omp

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// 2026-09-30：BADAPT-003／007，來源資料不改寫且快照不保留 refresh。
func TestAccessOnlySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	source, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, s := range []string{"CREATE TABLE auth_schema_version(id INTEGER PRIMARY KEY,version INTEGER)", "INSERT INTO auth_schema_version VALUES(1,1)", "CREATE TABLE auth_credentials(id INTEGER PRIMARY KEY,provider TEXT,credential_type TEXT,data TEXT,disabled_cause TEXT)"} {
		if _, err = source.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	data := `{"access":"access-probe","refresh":"refresh-probe","expires":` + fmtExpiry(time.Now().Add(time.Hour)) + `}`
	if _, err = source.Exec("INSERT INTO auth_credentials VALUES(1,'openai-codex','oauth',?,NULL)", data); err != nil {
		t.Fatal(err)
	}
	auth, err := snapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := sql.Open("sqlite", ":memory:")
	defer target.Close()
	target.SetMaxOpenConns(1)
	conn, _ := target.Conn(context.Background())
	err = conn.Raw(func(c interface{}) error { return c.(interface{ Deserialize([]byte) error }).Deserialize(auth.Payload) })
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err = target.QueryRow("SELECT data FROM auth_credentials").Scan(&got); err != nil {
		t.Fatal(err)
	}
	var d map[string]interface{}
	json.Unmarshal([]byte(got), &d)
	if d["refresh"] != "" || d["access"] != "access-probe" {
		t.Fatalf("snapshot %s", got)
	}
	var original string
	source.QueryRow("SELECT data FROM auth_credentials").Scan(&original)
	if original != data {
		t.Fatal("source modified")
	}
	source.Exec("UPDATE auth_credentials SET data=?", `{"access":"expired","refresh":"refresh-probe","expires":1}`)
	if _, err = snapshot(context.Background(), path); err == nil {
		t.Fatal("expired credential accepted")
	}
}
func fmtExpiry(t time.Time) string { b, _ := json.Marshal(t.UnixMilli()); return string(b) }
