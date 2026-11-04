package migrate

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hookshield-hq/hookshield/backend/migrations"
)

func fsWith(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestLoadOrdersAndHashes(t *testing.T) {
	got, err := Load(fsWith(map[string]string{
		"0002_second.sql": "SELECT 2;",
		"0001_first.sql":  "SELECT 1;",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[0].Name != "first" || len(got[0].Checksum) != 64 {
		t.Fatalf("unexpected migration: %+v", got[0])
	}
	if got[0].Checksum == got[1].Checksum {
		t.Fatal("different files must have different checksums")
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	cases := map[string]map[string]string{
		"bad name":          {"1_first.sql": "x"},
		"uppercase":         {"0001_First.sql": "x"},
		"not sql":           {"0001_first.txt": "x"},
		"version zero":      {"0000_first.sql": "x"},
		"duplicate version": {"0001_a.sql": "x", "0001_b.sql": "y"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(fsWith(files)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadIgnoresDirectories(t *testing.T) {
	fsys := fsWith(map[string]string{"0001_a.sql": "x", "tests/schema_test.sql": "y"})
	got, err := Load(fsys)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestPlan(t *testing.T) {
	avail, err := Load(fsWith(map[string]string{
		"0001_a.sql": "SELECT 1;",
		"0002_b.sql": "SELECT 2;",
		"0003_c.sql": "SELECT 3;",
	}))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("fresh database", func(t *testing.T) {
		p, err := Plan(avail, nil)
		if err != nil || len(p) != 3 {
			t.Fatalf("got %d pending, err %v", len(p), err)
		}
	})

	t.Run("partially applied", func(t *testing.T) {
		p, err := Plan(avail, []Applied{{1, avail[0].Checksum}, {2, avail[1].Checksum}})
		if err != nil || len(p) != 1 || p[0].Version != 3 {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		p, err := Plan(avail, []Applied{{1, avail[0].Checksum}, {2, avail[1].Checksum}, {3, avail[2].Checksum}})
		if err != nil || len(p) != 0 {
			t.Fatalf("got %+v, err %v", p, err)
		}
	})

	t.Run("edited migration", func(t *testing.T) {
		_, err := Plan(avail, []Applied{{1, "deadbeef"}})
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("want ErrChecksumMismatch, got %v", err)
		}
	})

	t.Run("database ahead of binary", func(t *testing.T) {
		_, err := Plan(avail, []Applied{{9, "x"}})
		if !errors.Is(err, ErrUnknownApplied) {
			t.Fatalf("want ErrUnknownApplied, got %v", err)
		}
	})
}

// TestEmbeddedMigrations guards the real migration set shipped in the binary.
func TestEmbeddedMigrations(t *testing.T) {
	got, err := Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no embedded migrations")
	}
	for i, m := range got {
		if m.Version != i+1 {
			t.Fatalf("migration versions must be contiguous: position %d has %04d", i, m.Version)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("%04d_%s is empty", m.Version, m.Name)
		}
	}
}
