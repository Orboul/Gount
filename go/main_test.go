package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

	got := realIP(req, nil, "auto")
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

	got := realIP(req, trusted, "auto")
	if got != "203.0.113.50" {
		t.Fatalf("realIP() = %q, want %q", got, "203.0.113.50")
	}
}

func TestRealIPUsesXRealIPWhenConfigured(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "10.0.0.5:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.5")
	req.Header.Set("X-Real-IP", "203.0.113.60")

	trusted, err := parseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parseTrustedProxies(): %v", err)
	}

	got := realIP(req, trusted, "x-real-ip")
	if got != "203.0.113.60" {
		t.Fatalf("realIP() = %q, want %q", got, "203.0.113.60")
	}
}

func TestRealIPUsesRemoteAddrWhenConfigured(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "10.0.0.5:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.5")
	req.Header.Set("X-Real-IP", "203.0.113.60")

	trusted, err := parseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parseTrustedProxies(): %v", err)
	}

	got := realIP(req, trusted, "remote-addr")
	if got != "10.0.0.5" {
		t.Fatalf("realIP() = %q, want %q", got, "10.0.0.5")
	}
}

func TestLoadConfigRejectsInvalidRealIPHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "real_ip_header: bad-value\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if _, err := loadConfig(path); err == nil {
		t.Fatal("loadConfig() error = nil, want non-nil")
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

type stubStore struct {
	insertErr error
	healthErr error
	visits    []visitCall
}

func (s *stubStore) InsertVisit(uniqueID, country, city, path, referrer string) error {
	s.visits = append(s.visits, visitCall{
		uniqueID: uniqueID,
		country:  country,
		city:     city,
		path:     path,
		referrer: referrer,
	})
	return s.insertErr
}
func (s *stubStore) DeleteOldVisits(retentionDays int) (int64, error) { return 0, nil }
func (s *stubStore) HealthCheck() error                               { return s.healthErr }
func (s *stubStore) Close() error                                     { return nil }

type visitCall struct {
	uniqueID string
	country  string
	city     string
	path     string
	referrer string
}

func TestTrackHandlerRejectsGetRequests(t *testing.T) {
	app := &App{
		cfg:   &Config{},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodGet, "/t?p=/home", nil)
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); allow != "OPTIONS, POST" {
		t.Fatalf("Allow header = %q, want %q", allow, "OPTIONS, POST")
	}
}

func TestTrackHandlerDeniesCrossOriginByDefault(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt: "test-salt",
		},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fhome"))
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestTrackHandlerAllowsConfiguredOrigin(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt:         "test-salt",
			CORSAllowedOrigins: []string{"https://example.com", "https://www.example.com"},
		},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fhome"))
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("Origin", "https://www.example.com")
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://www.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "https://www.example.com")
	}
}

func TestTrackHandlerDoesNotAllowUnconfiguredOrigin(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt:         "test-salt",
			CORSAllowedOrigins: []string{"https://example.com"},
		},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fhome"))
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("Origin", "https://not-allowed.example")
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func TestTrackHandlerAllowsWildcardOriginWhenConfigured(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt:         "test-salt",
			CORSAllowedOrigins: []string{"*"},
		},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fhome"))
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

func TestTrackHandlerRejectsOversizedPayload(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt: "test-salt",
		},
		store: &stubStore{},
	}

	body := "p=" + strings.Repeat("a", maxBeaconBody)
	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(body))
	req.RemoteAddr = "198.51.100.10:443"
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestTrackHandlerPreflightRejectsUnconfiguredOrigin(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt: "test-salt",
		},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodOptions, "/t", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestTrackHandlerReturns503WhenStrictTrackingErrorsEnabled(t *testing.T) {
	app := &App{
		cfg: &Config{
			SecretSalt:           "test-salt",
			StrictTrackingErrors: true,
		},
		store: &stubStore{insertErr: errors.New("boom")},
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fhome"))
	req.RemoteAddr = "198.51.100.10:443"
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestTrackHandlerAcceptsBeaconPayload(t *testing.T) {
	store := &stubStore{}
	app := &App{
		cfg: &Config{
			SecretSalt: "test-salt",
		},
		store: store,
	}

	req := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("p=%2Fpricing%3Fplan%3Dpro&ref=newsletter"))
	req.RemoteAddr = "198.51.100.10:443"
	rec := httptest.NewRecorder()

	app.trackHandler(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if len(store.visits) != 1 {
		t.Fatalf("inserted visits = %d, want 1", len(store.visits))
	}
	if got := store.visits[0].path; got != "/pricing?plan=pro" {
		t.Fatalf("path = %q, want %q", got, "/pricing?plan=pro")
	}
	if got := store.visits[0].referrer; got != "newsletter" {
		t.Fatalf("referrer = %q, want %q", got, "newsletter")
	}
}

func TestHealthHandlerReturns503WhenStoreIsUnhealthy(t *testing.T) {
	app := &App{
		cfg:   &Config{GeoType: "country"},
		store: &stubStore{healthErr: errors.New("store down")},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	app.healthHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "unhealthy" {
		t.Fatalf("body = %q, want %q", got, "unhealthy")
	}
}

func TestHealthHandlerRejectsUnsupportedMethod(t *testing.T) {
	app := &App{
		cfg:   &Config{},
		store: &stubStore{},
	}

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	app.healthHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow header = %q, want %q", allow, "GET, HEAD")
	}
}

func TestIPRateLimiterBlocksBurstWhenExhausted(t *testing.T) {
	limiter := newIPRateLimiter(1, 1)
	if limiter == nil {
		t.Fatal("newIPRateLimiter() = nil, want non-nil")
	}
	if !limiter.allow("198.51.100.10") {
		t.Fatal("first request should pass")
	}
	if limiter.allow("198.51.100.10") {
		t.Fatal("second immediate request should be rate-limited")
	}
}
