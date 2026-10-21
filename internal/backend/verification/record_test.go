package verification

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	codexNew = Identity{Runtime: "codex", Version: "0.163.0", Digest: "aaaa"}
	builtin  = []Identity{{Runtime: "codex", Version: "0.155.0", Digest: "bbbb"}}
)

func recordPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "asura", "runtime-verification.json")
}

func TestLoadMissingFileHasNoRecords(t *testing.T) {
	records, err := Load(recordPath(t))
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%v err=%v", records, err)
	}
}

func TestAddWritesPrivateFileAndReplacesSameIdentity(t *testing.T) {
	path := recordPath(t)
	first := Record{Identity: codexNew, VerifiedAt: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC), AsuraVersion: "v1.2.0"}
	second := first
	second.VerifiedAt = time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)
	other := Record{Identity: Identity{Runtime: "opencode", Version: "1.18.31", Digest: "cccc"}, VerifiedAt: first.VerifiedAt, AsuraVersion: "v1.2.0"}
	for _, r := range []Record{first, other, second} {
		if err := Add(path, r); err != nil {
			t.Fatal(err)
		}
	}
	records, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0] != second || records[1] != other {
		t.Fatalf("records=%+v", records)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode=%v err=%v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode=%v err=%v", dir, err)
	}
}

func TestRemove(t *testing.T) {
	path := recordPath(t)
	if err := Add(path, Record{Identity: codexNew, AsuraVersion: "v1.2.0"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := Remove(path, builtin[0], builtin); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("remove builtin err=%v", err)
	}
	if err := Remove(path, Identity{Runtime: "codex", Version: "9.9.9", Digest: "aaaa"}, builtin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove unknown err=%v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("failed removals changed the file:\n%s\n%s", before, after)
	}
	if err := Remove(path, codexNew, builtin); err != nil {
		t.Fatal(err)
	}
	if records, err := Load(path); err != nil || len(records) != 0 {
		t.Fatalf("records=%v err=%v", records, err)
	}
}

func TestLoadRejectsCorruptedOrUnsafeFile(t *testing.T) {
	path := recordPath(t)
	if err := Add(path, Record{Identity: codexNew}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("group/other-writable record file was accepted")
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("corrupted record file was accepted")
	}
}

func TestAllowed(t *testing.T) {
	records := []Record{{Identity: codexNew}}
	cases := []struct {
		name string
		id   Identity
		want bool
	}{
		{"builtin", builtin[0], true},
		{"verified", codexNew, true},
		{"same version, different binary", Identity{Runtime: "codex", Version: "0.163.0", Digest: "dddd"}, false},
		{"same binary, different version", Identity{Runtime: "codex", Version: "0.164.0", Digest: "aaaa"}, false},
		{"other runtime", Identity{Runtime: "opencode", Version: "0.163.0", Digest: "aaaa"}, false},
		{"empty digest", Identity{Runtime: "codex", Version: "0.163.0"}, false},
	}
	versionOnly := []Identity{{Runtime: "opencode", Version: "1.18.23"}}
	if !Allowed(Identity{Runtime: "opencode", Version: "1.18.23", Digest: "any"}, versionOnly, nil) {
		t.Error("version-only builtin did not admit its version")
	}
	if Allowed(Identity{Runtime: "opencode", Version: "1.18.31", Digest: "any"}, versionOnly, nil) {
		t.Error("version-only builtin admitted another version")
	}
	for _, c := range cases {
		if got := Allowed(c.id, builtin, records); got != c.want {
			t.Errorf("%s: Allowed=%v want %v", c.name, got, c.want)
		}
	}
}

func TestFileDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, []byte("asura"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := FileDigest(path)
	if err != nil || got != "bd7cbbd0b3d5bf143b32f21f0a049d2c8fc227ad0d94f55d16a8433693738fd5" {
		t.Fatalf("digest=%q err=%v", got, err)
	}
}
