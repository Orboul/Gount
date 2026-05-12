package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealIPIgnoresForwardedHeadersFromUntrustedClients(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("X-Real-IP", "203.0.113.51")

	got := realIP(req, nil)
	if got != "198.51.100.10" {
		t.Fatalf("realIP() = %q, want %q", got, "198.51.100.10")
	}
}

func TestRealIPUsesForwardedHeaderFromTrustedProxy(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "10.0.0.5:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.5")

	trusted, err := parseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parseTrustedProxies(): %v", err)
	}

	got := realIP(req, trusted)
	if got != "203.0.113.50" {
		t.Fatalf("realIP() = %q, want %q", got, "203.0.113.50")
	}
}

func TestParseTrustedProxiesRejectsInvalidEntries(t *testing.T) {
	if _, err := parseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("parseTrustedProxies() error = nil, want non-nil")
	}
}

func TestCSVStoreRecreatesMissingFileOnInsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "visits.csv")

	store, err := newCSVStore(path)
	if err != nil {
		t.Fatalf("newCSVStore(): %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove csv file: %v", err)
	}

	if err := store.InsertVisit("u1", "US", "", "/home", "Direct"); err != nil {
		t.Fatalf("InsertVisit(): %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "unique_id,country,city,path,timestamp,referrer") {
		t.Fatalf("csv header missing from recreated file: %q", text)
	}
	if !strings.Contains(text, "u1,US,,/home,") {
		t.Fatalf("visit row missing from recreated file: %q", text)
	}
}

func TestJSONStoreRecreatesMissingFileOnInsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "visits.jsonl")

	store, err := newJSONStore(path)
	if err != nil {
		t.Fatalf("newJSONStore(): %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove json file: %v", err)
	}

	if err := store.InsertVisit("u1", "US", "", "/home", "Direct"); err != nil {
		t.Fatalf("InsertVisit(): %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"unique_id":"u1"`) {
		t.Fatalf("json row missing from recreated file: %q", text)
	}
}

func TestSQLiteStoreRecoversWhenDatabaseFileIsDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "visits.db")

	store, err := newSQLStore("sqlite", path)
	if err != nil {
		t.Fatalf("newSQLStore(): %v", err)
	}
	defer store.Close()

	if err := store.InsertVisit("u1", "US", "", "/before-delete", "Direct"); err != nil {
		t.Fatalf("initial InsertVisit(): %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove sqlite db: %v", err)
	}

	time.Sleep(sqliteExistenceCheckInterval + 100*time.Millisecond)

	if err := store.InsertVisit("u2", "US", "", "/after-delete", "Direct"); err != nil {
		t.Fatalf("InsertVisit() after delete: %v", err)
	}

	reopened, err := newSQLStore("sqlite", path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()

	var count int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM visits WHERE unique_id = ?`, "u2").Scan(&count); err != nil {
		t.Fatalf("query recovered row: %v", err)
	}
	if count != 1 {
		t.Fatalf("recovered row count = %d, want 1", count)
	}
}
