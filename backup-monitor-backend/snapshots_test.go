package main

import "testing"

func TestParseSnapshotList(t *testing.T) {
	input := "2026-09-28/;-1;2026-09-28 05:00:00\n" +
		"2026-09-28/site/web1.tar.gz;86830;2026-09-28 05:00:02\n" +
		"2026-09-28/database/db1.sql.gz;738;2026-09-28 04:30:02\n" +
		"2026-09-28/root-file.tar.gz;42;2026-09-28 05:01:00\n"
	got := parseSnapshotList(input)
	if len(got) != 3 {
		t.Fatalf("got %d files, want 3", len(got))
	}
	if got[0].Path != "2026-09-28/site/web1.tar.gz" || got[0].Date != "2026-09-28" ||
		got[0].Category != "site" || got[0].Name != "web1.tar.gz" || got[0].Size != 86830 ||
		got[0].Modified != "2026-09-28 05:00:02" {
		t.Fatalf("unexpected site backup: %+v", got[0])
	}
	if got[1].Category != "database" || got[1].Size != 738 {
		t.Fatalf("unexpected database backup: %+v", got[1])
	}
	if got[2].Category != "root-file.tar.gz" || got[2].Name != "root-file.tar.gz" {
		t.Fatalf("unexpected root backup: %+v", got[2])
	}
}
