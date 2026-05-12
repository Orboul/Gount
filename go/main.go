// gount - A lightweight, privacy-first pixel tracker written in Go.
//
// It exposes a single /t endpoint that:
//   - Derives a pseudonymous User ID from IP + User-Agent + secret salt (SHA-256)
//   - Resolves the visitor's country (and optionally city) via a local MaxMind GeoLite2 database
//   - Persists the visit record to a configurable storage backend
//   - Returns HTTP 204 No Content so the payload is as small as possible
//
// Directory layout (relative to the binary):
//
//	binary
//	config.yaml
//	data/
//	  visits.db          (sqlite, default)
//	  visits.csv         (csv)
//	  visits.jsonl       (json)
//	  geodata/
//	    GeoLite2-Country.mmdb  (or GeoLite2-City.mmdb when geo_type: city)
//
// Supported db_type values: sqlite, postgres, mysql, csv, json
//
// On first run the binary bootstraps itself: writes a default config.yaml and
// downloads the chosen GeoLite2 edition from https://github.com/P3TERX/GeoLite.mmdb
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
	"gopkg.in/yaml.v3"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

type Config struct {
	ServerPort int    `yaml:"server_port"`
	ServerIP   string `yaml:"server_ip"`
	SecretSalt string `yaml:"secret_salt"`

	// DBType selects the storage backend: sqlite, postgres, mysql, csv, json
	DBType string `yaml:"db_type"`
	// DBPath is used by sqlite, csv, and json backends (relative to binary dir).
	DBPath string `yaml:"db_path"`
	// DBDSN is the full connection string for postgres and mysql backends.
	// postgres: "postgres://user:pass@host:5432/dbname?sslmode=disable"
	// mysql:    "user:pass@tcp(host:3306)/dbname?parseTime=true"
	DBDSN string `yaml:"db_dsn"`

	GeoType             string `yaml:"geo_type"`
	GeoDBPath           string `yaml:"geodb_path"`
	RetentionDays       int    `yaml:"retention_days"`
	UpdateFrequencyDays int    `yaml:"update_frequency_days"`
}

const defaultConfigYAML = `# gount configuration
# -------------------------------------------------------------------

# IP address the tracker binds to.
# Leave blank (or "0.0.0.0") to listen on all interfaces.
# Set to "127.0.0.1" to accept only local connections.
server_ip: ""

# TCP port the tracker listens on.
server_port: 8080

# Secret salt mixed into the SHA-256 User ID hash.
# CHANGE THIS before deploying — treat it like a password.
# Generate a good value with: openssl rand -hex 32
secret_salt: "CHANGE_ME_use_openssl_rand_hex_32"

# Storage backend: sqlite | postgres | mysql | csv | json
db_type: "sqlite"

# For sqlite, csv, json — path to the data file (relative to binary dir).
# Leave blank for the default:
#   sqlite -> data/visits.db
#   csv    -> data/visits.csv
#   json   -> data/visits.jsonl
db_path: ""

# For postgres / mysql — full DSN connection string.
# postgres example: postgres://user:pass@localhost:5432/gount?sslmode=disable
# mysql example:    user:pass@tcp(localhost:3306)/gount?parseTime=true
db_dsn: ""

# GeoLite2 edition: "country" (smaller) or "city" (includes city name).
# Auto-downloaded from https://github.com/P3TERX/GeoLite.mmdb on first run.
geo_type: "country"

# Path to the GeoLite2 .mmdb file (relative to binary dir).
# Leave blank to use the default derived from geo_type:
#   data/geodata/GeoLite2-Country.mmdb
#   data/geodata/GeoLite2-City.mmdb
geodb_path: ""

# Number of days to retain visit records.
retention_days: 90

# Reminder cadence for refreshing the GeoLite2 DB (metadata only).
update_frequency_days: 14
`

func writeDefaultConfig(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create default config: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(defaultConfigYAML)
	return err
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	if cfg.ServerPort == 0 {
		cfg.ServerPort = 8080
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = 90
	}
	if cfg.UpdateFrequencyDays == 0 {
		cfg.UpdateFrequencyDays = 14
	}
	if cfg.GeoType != "city" {
		cfg.GeoType = "country"
	}
	if cfg.DBType == "" {
		cfg.DBType = "sqlite"
	}

	return &cfg, nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

func binaryDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("eval symlinks: %w", err)
	}
	return filepath.Dir(real), nil
}

func resolveFromBinary(binDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(binDir, p)
}

// ---------------------------------------------------------------------------
// GeoLite2 auto-download
// ---------------------------------------------------------------------------

func geoEdition(geoType string) string {
	if geoType == "city" {
		return "GeoLite2-City"
	}
	return "GeoLite2-Country"
}

var geoLiteSources = []string{
	"https://github.com/P3TERX/GeoLite.mmdb/raw/download",
	"https://git.io",
}

func ensureGeoDB(destPath, geoType string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create geodata dir: %w", err)
	}

	edition := geoEdition(geoType)
	var lastErr error
	for _, base := range geoLiteSources {
		url := fmt.Sprintf("%s/%s.mmdb", base, edition)
		log.Printf("[geodb] downloading %s.mmdb from %s ...", edition, base)

		resp, err := http.Get(url) //nolint:gosec
		if err != nil {
			lastErr = fmt.Errorf("download %s from %s: %w", edition, base, err)
			log.Printf("[geodb] source failed, trying next: %v", lastErr)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("download %s from %s: server returned %s", edition, base, resp.Status)
			log.Printf("[geodb] source failed, trying next: %v", lastErr)
			continue
		}

		out, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", destPath, err)
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			_ = os.Remove(destPath)
			return fmt.Errorf("write %s: %w", destPath, err)
		}

		log.Printf("[geodb] %s.mmdb saved to %s", edition, destPath)
		return nil
	}
	return lastErr
}

// ---------------------------------------------------------------------------
// Store interface
// ---------------------------------------------------------------------------

// Store is the persistence interface satisfied by all backends.
type Store interface {
	InsertVisit(uniqueID, country, city, path, referrer string) error
	DeleteOldVisits(retentionDays int) (int64, error)
	Close() error
}

// visitRecord is the common in-memory representation used by file backends.
type visitRecord struct {
	UniqueID  string `json:"unique_id"`
	Country   string `json:"country"`
	City      string `json:"city"`
	Path      string `json:"path"`
	Referrer  string `json:"referrer"`
	Timestamp int64  `json:"timestamp"`
}

// truncate caps s at max bytes. Used to bound user-supplied strings before storage.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// extractReferrer parses a raw Referer header value and returns only the host
// (e.g. "google.com"). Returns "Direct" when the header is absent or unparseable.
func extractReferrer(raw string) string {
	if raw == "" {
		return "Direct"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "Direct"
	}
	return u.Host
}

// ---------------------------------------------------------------------------
// SQL store (sqlite / postgres / mysql)
// ---------------------------------------------------------------------------

type sqlStore struct {
	db     *sql.DB
	dbType string
}

func newSQLStore(dbType, dsn string) (*sqlStore, error) {
	driver := dbType
	if dbType == "sqlite" {
		driver = "sqlite"
	}

	if err := os.MkdirAll(filepath.Dir(dsn), 0o755); dbType == "sqlite" && err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbType, err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping %s: %w", dbType, err)
	}

	if dbType == "sqlite" {
		if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
			return nil, fmt.Errorf("enable WAL: %w", err)
		}
	}

	schema := sqlSchema(dbType)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	migrateReferrer(db, dbType)

	return &sqlStore{db: db, dbType: dbType}, nil
}

// sqlSchema returns the CREATE TABLE statement for the given driver dialect.
func sqlSchema(dbType string) string {
	switch dbType {
	case "postgres":
		return `
CREATE TABLE IF NOT EXISTS visits (
    id        BIGSERIAL PRIMARY KEY,
    unique_id TEXT      NOT NULL,
    country   TEXT      NOT NULL DEFAULT '',
    city      TEXT      NOT NULL DEFAULT '',
    path      TEXT      NOT NULL DEFAULT '',
    referrer  TEXT      NOT NULL DEFAULT 'Direct',
    timestamp BIGINT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_visits_timestamp ON visits(timestamp);`
	case "mysql":
		return `
CREATE TABLE IF NOT EXISTS visits (
    id        BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    unique_id VARCHAR(64)  NOT NULL,
    country   VARCHAR(8)   NOT NULL DEFAULT '',
    city      VARCHAR(128) NOT NULL DEFAULT '',
    path      VARCHAR(255) NOT NULL DEFAULT '',
    referrer  VARCHAR(255) NOT NULL DEFAULT 'Direct',
    timestamp BIGINT       NOT NULL,
    INDEX idx_visits_timestamp (timestamp)
);`
	default: // sqlite
		return `
CREATE TABLE IF NOT EXISTS visits (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    unique_id TEXT    NOT NULL,
    country   TEXT    NOT NULL DEFAULT '',
    city      TEXT    NOT NULL DEFAULT '',
    path      TEXT    NOT NULL DEFAULT '',
    referrer  TEXT    NOT NULL DEFAULT 'Direct',
    timestamp INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_visits_timestamp ON visits(timestamp);`
	}
}

// migrateReferrer adds the referrer column to tables created before this field
// existed. Errors are intentionally swallowed — the column already existing is
// the only realistic failure case.
func migrateReferrer(db *sql.DB, dbType string) {
	switch dbType {
	case "postgres":
		_, _ = db.Exec(`ALTER TABLE visits ADD COLUMN IF NOT EXISTS referrer TEXT NOT NULL DEFAULT 'Direct'`)
	case "mysql":
		_, _ = db.Exec(`ALTER TABLE visits ADD COLUMN IF NOT EXISTS referrer VARCHAR(255) NOT NULL DEFAULT 'Direct'`)
	default: // sqlite — no IF NOT EXISTS support; duplicate column error is harmless
		_, _ = db.Exec(`ALTER TABLE visits ADD COLUMN referrer TEXT NOT NULL DEFAULT 'Direct'`)
	}
}

func (s *sqlStore) InsertVisit(uniqueID, country, city, path, referrer string) error {
	var q string
	if s.dbType == "postgres" {
		q = `INSERT INTO visits (unique_id, country, city, path, referrer, timestamp) VALUES ($1, $2, $3, $4, $5, $6)`
	} else {
		q = `INSERT INTO visits (unique_id, country, city, path, referrer, timestamp) VALUES (?, ?, ?, ?, ?, ?)`
	}
	_, err := s.db.Exec(q, uniqueID, country, city, path, referrer, time.Now().UTC().Unix())
	return err
}

func (s *sqlStore) DeleteOldVisits(retentionDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Unix()
	var q string
	if s.dbType == "postgres" {
		q = `DELETE FROM visits WHERE timestamp < $1`
	} else {
		q = `DELETE FROM visits WHERE timestamp < ?`
	}
	res, err := s.db.Exec(q, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *sqlStore) Close() error { return s.db.Close() }

// ---------------------------------------------------------------------------
// CSV store
// ---------------------------------------------------------------------------

type csvStore struct {
	mu   sync.Mutex
	path string
}

var csvHeader = []string{"unique_id", "country", "city", "path", "timestamp", "referrer"}

func newCSVStore(path string) (*csvStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create csv dir: %w", err)
	}

	// Write header only if the file is new / empty.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		w := csv.NewWriter(f)
		if err := w.Write(csvHeader); err != nil {
			return nil, fmt.Errorf("write csv header: %w", err)
		}
		w.Flush()
	}

	return &csvStore{path: path}, nil
}

func (s *csvStore) InsertVisit(uniqueID, country, city, path, referrer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	err = w.Write([]string{
		uniqueID, country, city, path,
		strconv.FormatInt(time.Now().UTC().Unix(), 10),
		referrer,
	})
	w.Flush()
	return err
}

func (s *csvStore) DeleteOldVisits(retentionDays int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Unix()

	f, err := os.Open(s.path)
	if err != nil {
		return 0, err
	}
	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	f.Close()
	if err != nil {
		return 0, err
	}

	kept := rows[:1] // preserve header
	var deleted int64
	for _, row := range rows[1:] {
		if len(row) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(row[4], 10, 64)
		if ts < cutoff {
			deleted++
		} else {
			kept = append(kept, row)
		}
	}

	out, err := os.Create(s.path)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	w := csv.NewWriter(out)
	if err := w.WriteAll(kept); err != nil {
		return 0, err
	}
	w.Flush()
	return deleted, w.Error()
}

func (s *csvStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// JSON (JSONL) store
// ---------------------------------------------------------------------------

type jsonStore struct {
	mu   sync.Mutex
	path string
}

func newJSONStore(path string) (*jsonStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create json dir: %w", err)
	}
	// Touch the file so it exists.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open jsonl: %w", err)
	}
	f.Close()
	return &jsonStore{path: path}, nil
}

func (s *jsonStore) InsertVisit(uniqueID, country, city, path, referrer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	rec := visitRecord{
		UniqueID:  uniqueID,
		Country:   country,
		City:      city,
		Path:      path,
		Referrer:  referrer,
		Timestamp: time.Now().UTC().Unix(),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

func (s *jsonStore) DeleteOldVisits(retentionDays int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Unix()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return 0, err
	}

	var kept [][]byte
	var deleted int64
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec visitRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			kept = append(kept, []byte(line)) // preserve unparseable lines
			continue
		}
		if rec.Timestamp < cutoff {
			deleted++
		} else {
			kept = append(kept, []byte(line))
		}
	}

	out := strings.Join(func() []string {
		ss := make([]string, len(kept))
		for i, b := range kept {
			ss[i] = string(b)
		}
		return ss
	}(), "\n")
	if len(out) > 0 {
		out += "\n"
	}

	if err := os.WriteFile(s.path, []byte(out), 0o644); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *jsonStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// Store factory
// ---------------------------------------------------------------------------

func newStore(cfg *Config, binDir string) (Store, error) {
	switch cfg.DBType {
	case "sqlite":
		path := cfg.DBPath
		if path == "" {
			path = filepath.Join(binDir, "data", "visits.db")
		}
		return newSQLStore("sqlite", path)

	case "postgres", "mysql":
		if cfg.DBDSN == "" {
			return nil, fmt.Errorf("db_dsn must be set for db_type=%s", cfg.DBType)
		}
		return newSQLStore(cfg.DBType, cfg.DBDSN)

	case "csv":
		path := cfg.DBPath
		if path == "" {
			path = filepath.Join(binDir, "data", "visits.csv")
		}
		return newCSVStore(path)

	case "json":
		path := cfg.DBPath
		if path == "" {
			path = filepath.Join(binDir, "data", "visits.jsonl")
		}
		return newJSONStore(path)

	default:
		return nil, fmt.Errorf("unknown db_type %q — valid values: sqlite, postgres, mysql, csv, json", cfg.DBType)
	}
}

// ---------------------------------------------------------------------------
// Background cleanup goroutine
// ---------------------------------------------------------------------------

func startCleanupWorker(store Store, retentionDays int, done <-chan struct{}) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n, err := store.DeleteOldVisits(retentionDays)
				if err != nil {
					log.Printf("[cleanup] error pruning old visits: %v", err)
				} else {
					log.Printf("[cleanup] pruned %d visit(s) older than %d day(s)", n, retentionDays)
				}
			case <-done:
				log.Println("[cleanup] worker stopped")
				return
			}
		}
	}()
	log.Printf("[cleanup] worker started (retention=%d days, interval=24h)", retentionDays)
}

// ---------------------------------------------------------------------------
// IP extraction
// ---------------------------------------------------------------------------

func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip := strings.TrimSpace(xri)
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------------------
// Privacy-preserving User ID
// ---------------------------------------------------------------------------

func deriveUserID(ip, userAgent, salt string) string {
	h := sha256.New()
	h.Write([]byte(ip + "|" + userAgent + "|" + salt))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ---------------------------------------------------------------------------
// GeoIP lookup
// ---------------------------------------------------------------------------

func geoLookup(geoDB *geoip2.Reader, ipStr, geoType string) (country, city string) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", ""
	}

	if geoType == "city" {
		record, err := geoDB.City(ip)
		if err != nil {
			return "", ""
		}
		country = record.Country.IsoCode
		if name, ok := record.City.Names["en"]; ok {
			city = name
		}
		return country, city
	}

	record, err := geoDB.Country(ip)
	if err != nil {
		return "", ""
	}
	return record.Country.IsoCode, ""
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

type App struct {
	cfg   *Config
	store Store
	geoDB *geoip2.Reader
}

func (a *App) trackHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Browsers send a preflight OPTIONS before cross-origin GETs with custom headers.
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	ip := realIP(r)
	ua := r.UserAgent()

	path := r.URL.Query().Get("p")
	if path == "" {
		path = "/"
	}
	path = truncate(path, 255)

	// Explicit ?ref= param takes priority over the HTTP Referer header.
	// This lets UTM-style links (page.html?ref=newsletter) be tracked
	// even when the browser sends no Referer.
	var referrer string
	if ref := r.URL.Query().Get("ref"); ref != "" {
		referrer = truncate(ref, 255)
	} else {
		referrer = truncate(extractReferrer(r.Referer()), 255)
	}

	uniqueID := deriveUserID(ip, ua, a.cfg.SecretSalt)
	country, city := geoLookup(a.geoDB, ip, a.cfg.GeoType)

	if err := a.store.InsertVisit(uniqueID, country, city, path, referrer); err != nil {
		log.Printf("[track] insert error: %v", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	dir, err := binaryDir()
	if err != nil {
		log.Fatalf("locate binary: %v", err)
	}

	configPath := filepath.Join(dir, "config.yaml")
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := writeDefaultConfig(configPath); err != nil {
			log.Fatalf("bootstrap config: %v", err)
		}
		log.Printf("[init] created default config at %s — review settings before continuing", configPath)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("[init] config loaded from %s", configPath)

	// Resolve geodb path.
	if cfg.GeoDBPath == "" {
		cfg.GeoDBPath = filepath.Join(dir, "data", "geodata", geoEdition(cfg.GeoType)+".mmdb")
	} else {
		cfg.GeoDBPath = resolveFromBinary(dir, cfg.GeoDBPath)
	}
	// Resolve file-based db paths relative to binary dir.
	if cfg.DBPath != "" {
		cfg.DBPath = resolveFromBinary(dir, cfg.DBPath)
	}

	log.Printf("[init] db type:    %s", cfg.DBType)
	log.Printf("[init] geo type:   %s", cfg.GeoType)
	log.Printf("[init] geodb path: %s", cfg.GeoDBPath)
	log.Printf("[init] geo-DB update reminder: every %d day(s)", cfg.UpdateFrequencyDays)

	// GeoLite2: download if missing.
	if err := ensureGeoDB(cfg.GeoDBPath, cfg.GeoType); err != nil {
		log.Fatalf("geodb: %v", err)
	}

	// Storage backend.
	store, err := newStore(cfg, dir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer store.Close()
	log.Printf("[init] store ready (%s)", cfg.DBType)

	// GeoIP.
	geoDB, err := geoip2.Open(cfg.GeoDBPath)
	if err != nil {
		log.Fatalf("geoip: %v", err)
	}
	defer geoDB.Close()
	log.Printf("[init] GeoIP2 DB ready")

	// Background cleanup.
	done := make(chan struct{})
	defer close(done)
	startCleanupWorker(store, cfg.RetentionDays, done)

	// HTTP routes.
	app := &App{cfg: cfg, store: store, geoDB: geoDB}
	mux := http.NewServeMux()
	mux.HandleFunc("/t", app.trackHandler)
	mux.HandleFunc("/health", healthHandler)

	addr := fmt.Sprintf("%s:%d", cfg.ServerIP, cfg.ServerPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	logIP := cfg.ServerIP
	if logIP == "" || logIP == "0.0.0.0" {
		logIP = "0.0.0.0"
	}
	log.Printf("[init] listening on %s:%d", logIP, cfg.ServerPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
