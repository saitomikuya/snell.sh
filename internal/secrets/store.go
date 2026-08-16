package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/proxy-panel/proxy-panel/internal/database"
)

type Store struct {
	db  *sql.DB
	key []byte
}

func Open(db *sql.DB, dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "secrets", "master.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, key, 0600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes")
	}
	_ = os.Chmod(path, 0600)
	if os.Geteuid() == 0 {
		_ = os.Chown(path, 10001, 10001)
		_ = os.Chmod(path, 0640)
	}
	return &Store{db: db, key: key}, nil
}

func (s *Store) Put(ctx context.Context, kind string, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	id := uuid.NewString()
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(id+":"+kind))
	now := database.Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO secrets(id,kind,ciphertext,nonce,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, kind, ciphertext, nonce, now, now)
	return id, err
}

func (s *Store) Get(ctx context.Context, id string) ([]byte, error) {
	var kind string
	var ciphertext, nonce []byte
	if err := s.db.QueryRowContext(ctx, `SELECT kind,ciphertext,nonce FROM secrets WHERE id=?`, id).Scan(&kind, &ciphertext, &nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(id+":"+kind))
}

func Random(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, b)
	return b, err
}
