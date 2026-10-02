package autopost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	Draft     bool      `json:"draft,omitempty"`  // запас: ждёт кнопки «сделай пост», дату назначит бот
	Prompt    string    `json:"prompt"`           // запрос для FLUX на английском; пусто — фирменный фон
	Query     string    `json:"query,omitempty"`  // запрос к фотостоку (англ., 2–5 слов) — пробуется первым
	Overlay   string    `json:"overlay"`          // путь к PNG 1080×1350 относительно content/
	Slides    []Slide   `json:"slides,omitempty"` // карусель: если задано, Prompt/Overlay не нужны
}

// Slide — одна картинка карусели. Prompt пустой — фирменный фон.
type Slide struct {
	Prompt  string `json:"prompt,omitempty"`
	Query   string `json:"query,omitempty"`
	Overlay string `json:"overlay,omitempty"`
}

// Content читает очередь постов (по умолчанию raw-файлы GitHub).
type Content struct {
	BaseURL string // например https://raw.githubusercontent.com/oleg990/vk/main/content/
	// Extra — дополнительные ветки (например claude/posts, куда пушит задача Claude). Посты оттуда,
	// которых нет в основной очереди, тоже берутся; ветки может не быть — тогда она пропускается.
	Extra []string
	HTTP  *http.Client
}

func (c Content) get(ctx context.Context, path string) ([]byte, error) {
	return c.getFrom(ctx, c.BaseURL, path)
}

func (c Content) getFrom(ctx context.Context, base, path string) ([]byte, error) {
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	}
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
	items, err := c.queueFrom(ctx, c.BaseURL)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.ID] = true
	}
	for _, base := range c.Extra {
		extra, err := c.queueFrom(ctx, base)
		if err != nil {
			continue // ветки ещё нет — это нормально
		}
		for _, it := range extra {
			if seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			// картинки этого поста лежат в той же ветке — делаем пути полными
			abs := func(p string) string {
				if p == "" || strings.HasPrefix(p, "http") {
					return p
				}
				return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(p, "/")
			}
			it.Overlay = abs(it.Overlay)
			for i := range it.Slides {
				it.Slides[i].Overlay = abs(it.Slides[i].Overlay)
			}
			items = append(items, it)
		}
	}
	return items, nil
}

func (c Content) queueFrom(ctx context.Context, base string) ([]Item, error) {
	raw, err := c.getFrom(ctx, base, "queue.json")
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

// ErrNoToken — HF_TOKEN не задан: посты ждут, попытки не тратятся.
var ErrNoToken = errors.New("HF_TOKEN не задан")

// HF генерирует картинку FLUX.1-schnell через Hugging Face Inference Providers
// (router.huggingface.co, оплата — из кредитов аккаунта HF). Провайдеры пробуются по порядку.
type HF struct {
	URL       string   // https://router.huggingface.co
	Providers []string // fal-ai, nscale
	Token     string
	HTTP      *http.Client
}

func (h HF) Generate(ctx context.Context, prompt string) ([]byte, error) {
	if h.Token == "" {
		return nil, ErrNoToken
	}
	providers := h.Providers
	if len(providers) == 0 {
		providers = []string{"fal-ai", "nscale"}
	}
	var errs []string
	for _, p := range providers {
		p = strings.TrimSpace(p)
		var img []byte
		var err error
		switch p {
		case "fal-ai":
			img, err = h.fal(ctx, prompt)
		case "nscale":
			img, err = h.nscale(ctx, prompt)
		default:
			err = fmt.Errorf("неизвестный провайдер")
		}
		if err == nil {
			return img, nil
		}
		errs = append(errs, p+": "+err.Error())
	}
	return nil, fmt.Errorf("Hugging Face: %s", strings.Join(errs, "; "))
}

func (h HF) post(ctx context.Context, path string, payload any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 40<<20))
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s: %s", resp.Status, msg)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ответ: %w", err)
	}
	return nil
}

func (h HF) fal(ctx context.Context, prompt string) ([]byte, error) {
	var out struct {
		Images []struct {
			URL string `json:"url"`
		} `json:"images"`
	}
	err := h.post(ctx, "/fal-ai/fal-ai/flux/schnell", map[string]any{
		"prompt":              prompt,
		"image_size":          map[string]int{"width": 1024, "height": 1280},
		"num_inference_steps": 4,
		"num_images":          1,
		"seed":                rand.IntN(1 << 30),
		"sync_mode":           true,
	}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Images) == 0 || out.Images[0].URL == "" {
		return nil, fmt.Errorf("пустой ответ")
	}
	return h.fetchImage(ctx, out.Images[0].URL)
}

func (h HF) nscale(ctx context.Context, prompt string) ([]byte, error) {
	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	err := h.post(ctx, "/nscale/v1/images/generations", map[string]any{
		"model":           "black-forest-labs/FLUX.1-schnell",
		"prompt":          prompt,
		"size":            "1024x1280",
		"response_format": "b64_json",
	}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || out.Data[0].B64 == "" {
		return nil, fmt.Errorf("пустой ответ")
	}
	return base64.StdEncoding.DecodeString(out.Data[0].B64)
}

// fetchImage понимает data:image/...;base64,... и обычные https-ссылки.
func (h HF) fetchImage(ctx context.Context, u string) ([]byte, error) {
	if strings.HasPrefix(u, "data:") {
		i := strings.Index(u, ",")
		if i < 0 {
			return nil, fmt.Errorf("битый data URI")
		}
		return base64.StdEncoding.DecodeString(u[i+1:])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("скачивание картинки: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 30<<20))
}
