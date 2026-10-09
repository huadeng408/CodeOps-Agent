// Package localidentity persists authentication facts, never Session projections.
package localidentity

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"code-agent/internal/model"
	"code-agent/pkg/hash"

	"gorm.io/gorm"
	_ "modernc.org/sqlite"
)

type Store struct {
	db            *sql.DB
	setupUser     string
	setupPassword string
}

func Open(path, setupUser, setupPassword string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("local identity path is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := rejectRedirectedPath(path); err != nil {
		return nil, err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		base := filepath.Base(path)
		if entry.IsDir() || (name != base && name != base+"-journal" && name != base+"-wal" && name != base+"-shm") {
			return nil, errors.New("local identity requires a dedicated private directory")
		}
		if err := rejectRedirectedPath(filepath.Join(directory, name)); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var tables, identityTables int
	err = db.QueryRow(`SELECT count(*), coalesce(sum(name IN ('local_identity','revoked_tokens')),0) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables, &identityTables)
	if err != nil || (tables != 0 && (tables != 2 || identityTables != 2)) {
		db.Close()
		return nil, errors.New("local identity refuses an unrelated database; explicit migration is required")
	}
	privatePaths := []string{directory, path}
	for _, entry := range entries {
		privatePaths = append(privatePaths, filepath.Join(directory, entry.Name()))
	}
	for _, privatePath := range privatePaths {
		if err := protectIdentityPath(privatePath); err != nil {
			db.Close()
			return nil, err
		}
	}
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS local_identity (
 id INTEGER PRIMARY KEY, slot INTEGER NOT NULL DEFAULT 1 UNIQUE CHECK(slot=1),
 username TEXT NOT NULL UNIQUE, password TEXT NOT NULL, role TEXT NOT NULL CHECK(role='USER'),
 org_tags TEXT NOT NULL DEFAULT '', primary_org TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS revoked_tokens (digest TEXT PRIMARY KEY, expires_at INTEGER NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, setupUser: strings.TrimSpace(setupUser), setupPassword: setupPassword}, nil
}

func rejectRedirectedPath(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if err := rejectReparsePoint(current); err != nil {
				return err
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return resolveErr
			}
			if info.Mode()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Clean(resolved), current) {
				return errors.New("local identity path contains a symlink or reparse redirect")
			}
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Create(user *model.User) error {
	if s.setupUser == "" || len(s.setupPassword) < 12 || user.Username != s.setupUser || !hash.CheckPasswordHash(s.setupPassword, user.Password) {
		return errors.New("local identity setup requires approved process credentials")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	// Keep local IDs distinct from usual service IDs and exactly representable in JS.
	user.ID = uint((binary.BigEndian.Uint64(random[:]) & ((1 << 52) - 1)) | (1 << 51))
	user.CreatedAt, user.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO local_identity (id, username, password, role, created_at, updated_at) VALUES (?,?,?,?,?,?)`, user.ID, user.Username, user.Password, "USER", user.CreatedAt.Format(time.RFC3339Nano), user.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return errors.New("local identity is already initialized or could not be persisted")
	}
	return nil
}

func (s *Store) FindByUsername(username string) (*model.User, error) {
	var user model.User
	var created, updated string
	err := s.db.QueryRow(`SELECT id, username, password, role, org_tags, primary_org, created_at, updated_at FROM local_identity WHERE username=?`, username).Scan(&user.ID, &user.Username, &user.Password, &user.Role, &user.OrgTags, &user.PrimaryOrg, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gorm.ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}
	if user.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, err
	}
	if user.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) Update(user *model.User) error {
	result, err := s.db.Exec(`UPDATE local_identity SET org_tags=?, primary_org=?, updated_at=? WHERE id=? AND username=?`, user.OrgTags, user.PrimaryOrg, time.Now().UTC().Format(time.RFC3339Nano), user.ID, user.Username)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func tokenDigest(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (s *Store) Revoke(raw string, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO revoked_tokens (digest, expires_at) VALUES (?,?) ON CONFLICT(digest) DO UPDATE SET expires_at=max(expires_at,excluded.expires_at)`, tokenDigest(raw), expires.Unix())
	return err
}

func (s *Store) IsRevoked(raw string) (bool, error) {
	var found int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM revoked_tokens WHERE digest=? AND expires_at>?)`, tokenDigest(raw), time.Now().Unix()).Scan(&found)
	return found != 0, err
}
