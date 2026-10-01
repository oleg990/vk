package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileStore держит всё в памяти и сохраняет в один JSON-файл после каждого изменения.
// Подходит для старта (сотни заявок); для роста — заменить на PostgreSQL.
// Пустой path — только память (для тестов).
type FileStore struct {
	mu   sync.Mutex
	path string
	data fileData
}

type fileData struct {
	NextLeadID int64                      `json:"next_lead_id"`
	Sessions   map[int64]*Session         `json:"sessions"`
	Leads      []Lead                     `json:"leads"`
	Settings   map[string]json.RawMessage `json:"settings"`
}

// OpenFile загружает файл (если есть) или создаёт пустое хранилище.
func OpenFile(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: fileData{
		NextLeadID: 1,
		Sessions:   map[int64]*Session{},
		Settings:   map[string]json.RawMessage{},
	}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, s.flush()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[int64]*Session{}
	}
	if s.data.Settings == nil {
		s.data.Settings = map[string]json.RawMessage{}
	}
	if s.data.NextLeadID < 1 {
		s.data.NextLeadID = 1
	}
	return s, nil
}

// NewMemory — хранилище только в памяти.
func NewMemory() *FileStore {
	s, _ := OpenFile("")
	return s
}

// flush атомарно пишет файл: временный файл + rename. Вызывать под mu.
func (s *FileStore) flush() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneSession(in *Session) *Session {
	out := *in
	out.Answers = make(map[string]string, len(in.Answers))
	for k, v := range in.Answers {
		out.Answers[k] = v
	}
	out.Photos = append([]string(nil), in.Photos...)
	return &out
}

func (s *FileStore) GetSession(_ context.Context, userID int64) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.data.Sessions[userID]; ok {
		return cloneSession(v), nil
	}
	return nil, nil
}

func (s *FileStore) SaveSession(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := cloneSession(sess)
	c.UpdatedAt = time.Now()
	s.data.Sessions[sess.UserID] = c
	return s.flush()
}

func (s *FileStore) DeleteSession(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sessions[userID]; !ok {
		return nil
	}
	delete(s.data.Sessions, userID)
	return s.flush()
}

func (s *FileStore) SaveLead(_ context.Context, l *Lead) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *l
	c.ID = s.data.NextLeadID
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	c.Answers = append([]Answer(nil), l.Answers...)
	c.Photos = append([]string(nil), l.Photos...)
	s.data.NextLeadID++
	s.data.Leads = append(s.data.Leads, c)
	if err := s.flush(); err != nil {
		return 0, err
	}
	return c.ID, nil
}

func (s *FileStore) ListLeads(_ context.Context, kind string, since time.Time) ([]Lead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Lead
	for _, l := range s.data.Leads {
		if (kind == "" || l.Kind == kind) && !l.CreatedAt.Before(since) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (s *FileStore) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Settings[key]
	return append(json.RawMessage(nil), v...), ok, nil
}

func (s *FileStore) SetSetting(_ context.Context, key string, value json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Settings[key] = append(json.RawMessage(nil), value...)
	return s.flush()
}
