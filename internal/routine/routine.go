// Package routine запускает задачу Claude («Посты VK: сделай пост») через Routines API.
package routine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client — запуск одной задачи по её ID и токену (claude.ai/code/routines → API trigger).
type Client struct {
	URL   string // https://api.anthropic.com
	ID    string
	Token string
	HTTP  *http.Client
}

func (c Client) Enabled() bool { return c.ID != "" && c.Token != "" }

// Fire запускает задачу; text — пожелание Олега. Возвращает ссылку на сессию.
func (c Client) Fire(ctx context.Context, text string) (string, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("на сервере не заданы ROUTINE_ID и ROUTINE_TOKEN")
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	u := strings.TrimRight(c.URL, "/") + "/v1/claude_code/routines/" + c.ID + "/fire"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("%s: %s", resp.Status, msg)
	}
	var out struct {
		URL string `json:"claude_code_session_url"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.URL, nil
}
