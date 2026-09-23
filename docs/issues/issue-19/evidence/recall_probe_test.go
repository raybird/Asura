package evidence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"asura/internal/contextwin"
	"asura/internal/engine"
	_ "modernc.org/sqlite"
)

// 2026-09-22：規劃用探測，不供產品執行路徑使用。
func terms(s string) map[string]bool {
	out := map[string]bool{}
	var run []rune
	flush := func() {
		if len(run) > 0 && unicode.Is(unicode.Han, run[0]) {
			for i := 1; i < len(run); i++ { out[string(run[i-1:i+1])] = true }
		} else if len(run) >= 2 { out[string(run)] = true }
		run = nil
	}
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) { flush(); continue }
		if len(run) > 0 && unicode.Is(unicode.Han, r) != unicode.Is(unicode.Han, run[0]) { flush() }
		run = append(run, r)
	}
	flush()
	return out
}

func rank(prompt string, memories map[string]string) []string {
	q := terms(prompt)
	scores := map[string]int{}
	for id, content := range memories {
		for term := range terms(content) { if q[term] { scores[id]++ } }
	}
	ids := []string{}
	for id, n := range scores { if n > 0 { ids = append(ids, id) } }
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] { return scores[ids[i]] > scores[ids[j]] }
		return ids[i] < ids[j]
	})
	return ids
}

func TestRecallProbe(t *testing.T) {
	memories := map[string]string{
		"a": "SQLite workspace 隔離，禁止跨專案讀取",
		"b": "SQLite transaction rollback",
		"c": "部署使用 Docker image",
		"d": "SQLite workspace 隔離，禁止跨專案讀取",
	}
	cases := []struct{ query, want string }{
		{"SQLITE workspace 隔離", "a,d,b"},
		{"transaction rollback", "b"},
		{"部署 Docker", "c"},
		{"香蕉果汁", ""},
		{"資料庫", ""},
		{"隔", ""},
	}
	for _, c := range cases {
		for i := 0; i < 100; i++ {
			got := strings.Join(rank(c.query, memories), ",")
			if got != c.want { t.Fatalf("%q: got %q want %q", c.query, got, c.want) }
		}
		t.Logf("query=%q selected=%q", c.query, c.want)
	}
	e := contextwin.NewHeuristicEstimator(contextwin.DefaultReserveRatio)
	base := "修正 SQLite 隔離"
	ctx := &engine.ExecutionContext{ProjectMemories: []engine.ProjectMemorySnapshot{{ID: "a", Content: memories["a"]}}}
	full := engine.ComposePrompt(base, ctx)
	delta := e.Estimate("direct", full).EstimatedPromptTokens - e.Estimate("direct", base).EstimatedPromptTokens
	raw := e.Estimate("direct", memories["a"]).EstimatedPromptTokens
	if delta <= raw { t.Fatal("fixture must expose wrapper overhead") }
	t.Logf("formatted_bytes=%d estimated_delta=%d raw_content_estimate=%d", len(full)-len(base), delta, raw)
}

func TestBoundedSQLProbe(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil { t.Fatal(err) }; defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE memories(id TEXT PRIMARY KEY, workspace TEXT NOT NULL, content TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'manual', created_at DATETIME NOT NULL);
CREATE INDEX recall_scope_id ON memories(workspace,id);`)
	if err != nil { t.Fatal(err) }
	tx, err := db.Begin(); if err != nil { t.Fatal(err) }
	stmt, err := tx.Prepare("INSERT INTO memories VALUES(?,?,?,'manual','2026-09-22')"); if err != nil { t.Fatal(err) }
	for i := 0; i < 10000; i++ {
		content := "SQLite workspace 隔離"
		if i == 0 { content = strings.Repeat("x", 1024*1024) }
		if _, err := stmt.Exec(fmt.Sprintf("%05d", i), "/allowed", content); err != nil { t.Fatal(err) }
	}
	if _, err := stmt.Exec("foreign", "/other", "SECRET"); err != nil { t.Fatal(err) }
	stmt.Close(); if err := tx.Commit(); err != nil { t.Fatal(err) }
	query := `SELECT id, substr(CAST(content AS BLOB),1,8193) FROM memories WHERE workspace=? ORDER BY id LIMIT 257`
	plan, err := db.Query("EXPLAIN QUERY PLAN "+query, "/allowed"); if err != nil { t.Fatal(err) }
	for plan.Next() {
		var a,b,c int; var detail string
		if err := plan.Scan(&a,&b,&c,&detail); err != nil { t.Fatal(err) }
		if strings.Contains(detail,"TEMP B-TREE") { t.Fatal(detail) }
		t.Log(detail)
	}; if err := plan.Err(); err != nil { t.Fatal(err) }; plan.Close()
	start := time.Now()
	ctx,cancel := context.WithTimeout(context.Background(),100*time.Millisecond); defer cancel()
	rows, err := db.QueryContext(ctx, query,"/allowed"); if err != nil { t.Fatal(err) }
	count, bytes := 0,0
	for rows.Next() {
		var id string; var content []byte
		if err := rows.Scan(&id,&content); err != nil { t.Fatal(err) }
		if id=="foreign" || len(content)>8193 { t.Fatal("scope/byte bound violated") }
		count++; bytes+=len(content)
	}; if err := rows.Err(); err != nil { t.Fatal(err) }; rows.Close()
	if count!=257 { t.Fatalf("count=%d",count) }
	t.Logf("database_rows=10001 returned=%d returned_bytes=%d elapsed=%s",count,bytes,time.Since(start))
}

func BenchmarkBoundedRanking(b *testing.B) {
	memories := map[string]string{}
	for i:=0;i<256;i++ { memories[fmt.Sprintf("%05d",i)]=strings.Repeat("SQLite workspace 隔離 ",300) }
	b.ResetTimer()
	for i:=0;i<b.N;i++ { rank("SQLite workspace 隔離",memories) }
}
