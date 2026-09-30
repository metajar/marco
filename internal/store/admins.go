package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Admin is a parent who can log in to the UI and receive alerts.
type Admin struct {
	ID           int64
	Name         string
	Username     string
	PasswordHash string
	Phone        string
	Notify       bool
	CreatedAt    time.Time
}

func (a *Admin) CheckPassword(pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(pw)) == nil
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

const adminCols = `id, name, username, password_hash, phone, notify, created_at`

func scanAdmin(sc interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	var created int64
	if err := sc.Scan(&a.ID, &a.Name, &a.Username, &a.PasswordHash, &a.Phone, &a.Notify, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.CreatedAt = fromTS(created)
	return &a, nil
}

func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Store) ListAdmins() ([]*Admin, error) {
	rows, err := s.db.Query(`SELECT ` + adminCols + ` FROM admins ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Admin
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAdmin(id int64) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT `+adminCols+` FROM admins WHERE id = ?`, id))
}

func (s *Store) GetAdminByUsername(username string) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT `+adminCols+` FROM admins WHERE username = ?`, strings.TrimSpace(username)))
}

func (s *Store) SaveAdmin(a *Admin) error {
	if a.ID == 0 {
		a.CreatedAt = time.Now()
		res, err := s.db.Exec(`INSERT INTO admins (name, username, password_hash, phone, notify, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			a.Name, a.Username, a.PasswordHash, a.Phone, b2i(a.Notify), ts(a.CreatedAt))
		if err != nil {
			return err
		}
		a.ID, err = res.LastInsertId()
		return err
	}
	_, err := s.db.Exec(`UPDATE admins SET name = ?, username = ?, password_hash = ?, phone = ?, notify = ? WHERE id = ?`,
		a.Name, a.Username, a.PasswordHash, a.Phone, b2i(a.Notify), a.ID)
	return err
}

func (s *Store) DeleteAdmin(id int64) error {
	_, err := s.db.Exec(`DELETE FROM admins WHERE id = ?`, id)
	return err
}

// AlertRecipients returns admins who want text alerts and have a phone number.
func (s *Store) AlertRecipients() ([]*Admin, error) {
	all, err := s.ListAdmins()
	if err != nil {
		return nil, err
	}
	var out []*Admin
	for _, a := range all {
		if a.Notify && strings.TrimSpace(a.Phone) != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// CreateSession issues a login token valid for the given duration.
func (s *Store) CreateSession(adminID int64, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := s.db.Exec(`INSERT INTO sessions (token, admin_id, expires_at) VALUES (?, ?, ?)`,
		token, adminID, time.Now().Add(ttl).Unix())
	return token, err
}

func (s *Store) SessionAdmin(token string) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT a.id, a.name, a.username, a.password_hash, a.phone, a.notify, a.created_at
		FROM sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token = ? AND s.expires_at > ?`, token, time.Now().Unix()))
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}
