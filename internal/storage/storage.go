// Package storage хранит диалоги, заявки и настройки бота.
// Интерфейс Store позволяет позже заменить файл на PostgreSQL без правок бота.
package storage

import (
	"context"
	"encoding/json"
	"time"
)

// Session — текущий шаг пользователя в сценарии.
type Session struct {
	UserID    int64             `json:"user_id"`
	Flow      string            `json:"flow"`
	Step      int               `json:"step"`
	Answers   map[string]string `json:"answers"`
	Photos    []string          `json:"photos,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Answer — один ответ в заявке, в порядке вопросов.
type Answer struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// Lead — готовая заявка.
type Lead struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"` // buy | sell | mortgage | contact
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Answers   []Answer  `json:"answers"`
	Photos    []string  `json:"photos,omitempty"`
	ConsentAt time.Time `json:"consent_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Store — всё, что боту нужно от хранилища.
type Store interface {
	GetSession(ctx context.Context, userID int64) (*Session, error) // nil, nil — сессии нет
	SaveSession(ctx context.Context, s *Session) error
	DeleteSession(ctx context.Context, userID int64) error

	SaveLead(ctx context.Context, l *Lead) (int64, error)
	// ListLeads — заявки начиная с since, новые первыми; kind == "" — все виды.
	ListLeads(ctx context.Context, kind string, since time.Time) ([]Lead, error)

	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SetSetting(ctx context.Context, key string, value json.RawMessage) error
}
