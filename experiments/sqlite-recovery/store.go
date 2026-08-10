package sqliterecovery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

const (
	applicationID = 0x53504147 // "SPAG"
	schemaVersion = 1
	defaultBusy   = 2 * time.Second
	maximumBusy   = 30 * time.Second
	maximumDepth  = 32
)

var (
	ErrPoisoned       = errors.New("sqlite recovery recorder is poisoned")
	ErrUnsafeRecovery = errors.New("sqlite recovery refused unsafe state")
)

// Options bounds store startup and branch lineage.
type Options struct {
	BusyTimeout time.Duration
	MaxBranches int
}

// Store owns one embedded database connection and one crash-detection writer
// epoch. Store methods serialize database/sql access intentionally.
type Store struct {
	db          *sql.DB
	epoch       string
	now         func() time.Time
	mu          sync.Mutex
	closed      bool
	depth       int
	afterCommit func() error
}

// Open creates or validates a version-one recovery store. The path is local
// application state, never a daemon endpoint or shared network database.
func Open(ctx context.Context, path string, options Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" || path != strings.TrimSpace(path) {
		return nil, errors.New("sqlite recovery path must be non-empty without surrounding whitespace")
	}
	busy := options.BusyTimeout
	if busy == 0 {
		busy = defaultBusy
	}
	if busy < time.Millisecond || busy > maximumBusy {
		return nil, fmt.Errorf("sqlite recovery busy timeout must be between 1ms and %s", maximumBusy)
	}
	depth := options.MaxBranches
	if depth == 0 {
		depth = maximumDepth
	}
	if depth < 1 || depth > maximumDepth {
		return nil, fmt.Errorf("sqlite recovery branch depth must be between 1 and %d", maximumDepth)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite recovery path: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite recovery directory: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create sqlite recovery file: %w", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		return nil, fmt.Errorf("close sqlite recovery file: %w", closeErr)
	}
	dsn := "file:" + url.PathEscape(filepath.ToSlash(abs)) + "?_txlock=immediate"
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite recovery database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxLifetime(0)
	store := &Store{db: database, now: time.Now, depth: depth}
	if err = store.initialize(ctx, busy); err != nil {
		_ = database.Close()
		return nil, err
	}
	epoch, err := randomID()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	store.epoch = epoch
	if _, err = database.ExecContext(ctx,
		`INSERT INTO writer_epochs(epoch_id, opened_unix_nano) VALUES (?, ?)`, epoch, store.now().UnixNano()); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("open sqlite recovery writer epoch: %w", err)
	}
	return store, nil
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("create sqlite recovery identity: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// Close durably closes the current writer epoch. A process termination leaves
// it open so the next process can report a crash marker.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	if _, err := store.db.Exec(`UPDATE writer_epochs SET closed_unix_nano = ? WHERE epoch_id = ? AND closed_unix_nano IS NULL`, store.now().UnixNano(), store.epoch); err != nil {
		return fmt.Errorf("close sqlite recovery writer epoch: %w", err)
	}
	store.closed = true
	if err := store.db.Close(); err != nil {
		return fmt.Errorf("close sqlite recovery database: %w", err)
	}
	return nil
}

// CrashMarker describes an epoch which did not close cleanly. It contains no
// source paths, tool arguments, or secrets.
type CrashMarker struct {
	EpochID  string
	OpenedAt time.Time
}

// Crashes returns prior unclosed writer epochs, excluding the current epoch.
func (store *Store) Crashes(ctx context.Context) ([]CrashMarker, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT epoch_id, opened_unix_nano FROM writer_epochs WHERE closed_unix_nano IS NULL AND epoch_id <> ? ORDER BY opened_unix_nano, epoch_id`, store.epoch)
	if err != nil {
		return nil, fmt.Errorf("query sqlite recovery crash markers: %w", err)
	}
	defer rows.Close()
	var result []CrashMarker
	for rows.Next() {
		var marker CrashMarker
		var opened int64
		if err = rows.Scan(&marker.EpochID, &opened); err != nil {
			return nil, fmt.Errorf("scan sqlite recovery crash marker: %w", err)
		}
		marker.OpenedAt = time.Unix(0, opened).UTC()
		result = append(result, marker)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite recovery crash markers: %w", err)
	}
	return result, nil
}
