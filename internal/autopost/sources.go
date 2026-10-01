package autopost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// Item — пост из очереди content/queue.json в репозитории.
type Item struct {
	ID        string    `json:"id"`
	PublishAt time.Time `json:"publish_at"`
	Text      string    `json:"text"`
	Prompt    string    `json:"prompt"`  // запрос для FLUX на английском; пусто — фирменный фон
	Overlay   string    `json:"overlay"` // путь к PNG 1080×1350 относительно content/
}

// Content читает очередь постов (по умолчанию raw-файлы GitHub).
type Content struct {
	BaseURL string // например https://raw.githubusercontent.com/oleg990/vk/main/content/
	HTTP    *http.Client
}

func (c Content) get(ctx context.Context, path string) ([]byte, error) {
	u := strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

func (c Content) Queue(ctx context.Context) ([]Item, error) {
	raw, err := c.get(ctx, "queue.json")
	if err != nil {
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("queue.json: %w", err)
	}
	return items, nil
}

func (c Content) Overlay(ctx context.Context, path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	return c.get(ctx, path)
}

// HF генерирует картинку через Hugging Face Inference API (FLUX.1-schnell).
type HF struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func (h HF) Generate(ctx context.Context, prompt string) ([]byte, error) {
	if h.Token == "" {
		return nil, fmt.Errorf("HF_TOKEN не задан")
	}
	body, _ := json.Marshal(map[string]any{
		"inputs": prompt,
		"parameters": map[string]any{
			"width": 1024, "height": 1280, "num_inference_steps": 4, "seed": rand.IntN(1 << 30),
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "image/png")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("Hugging Face %s: %s", resp.Status, msg)
	}
	return raw, nil
}
