// Package vk — минимальный клиент VK API для бота сообщества:
// Bots Long Poll, messages.send, users.get. Только стандартная библиотека.
package vk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAPIVersion = "5.199"
	DefaultBaseURL    = "https://api.vk.com/method/"
	longPollWait      = 25 // секунд
)

// Client работает от имени сообщества по ключу доступа сообщества.
type Client struct {
	token    string
	groupID  int64
	version  string
	baseURL  string
	api      *http.Client
	longPoll *http.Client
	log      *slog.Logger
}

func New(token string, groupID int64, baseURL string, log *slog.Logger) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		token:    token,
		groupID:  groupID,
		version:  DefaultAPIVersion,
		baseURL:  baseURL,
		api:      &http.Client{Timeout: 60 * time.Second, Transport: ipv4Transport()},
		longPoll: &http.Client{Timeout: (longPollWait + 10) * time.Second, Transport: ipv4Transport()},
		log:      log,
	}
}

// APIError — ошибка, которую вернул VK.
type APIError struct {
	Code int    `json:"error_code"`
	Msg  string `json:"error_msg"`
}

func (e *APIError) Error() string { return fmt.Sprintf("vk api error %d: %s", e.Code, e.Msg) }

// ErrCodeNoPermission — пользователь не разрешил сообществу писать ему (901).
const ErrCodeNoPermission = 901

func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	params.Set("access_token", c.token)
	params.Set("v", c.version)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.api.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read: %w", method, err)
	}
	var env struct {
		Response json.RawMessage `json:"response"`
		Error    *APIError       `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if env.Error != nil {
		return env.Error
	}
	if out != nil {
		if err := json.Unmarshal(env.Response, out); err != nil {
			return fmt.Errorf("%s: decode response: %w", method, err)
		}
	}
	return nil
}

// Send отправляет сообщение от имени сообщества. kb == nil — без клавиатуры.
func (c *Client) Send(ctx context.Context, peerID int64, text string, kb *Keyboard) error {
	p := url.Values{}
	p.Set("peer_id", strconv.FormatInt(peerID, 10))
	p.Set("message", text)
	p.Set("random_id", strconv.FormatInt(int64(randID()), 10))
	p.Set("dont_parse_links", "1")
	if kb != nil {
		raw, err := json.Marshal(kb)
		if err != nil {
			return err
		}
		p.Set("keyboard", string(raw))
	}
	return c.call(ctx, "messages.send", p, nil)
}

func randID() int32 { return rand.Int32() }

// UserName возвращает «Имя Фамилия» пользователя.
func (c *Client) UserName(ctx context.Context, userID int64) (string, error) {
	p := url.Values{}
	p.Set("user_ids", strconv.FormatInt(userID, 10))
	var users []struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := c.call(ctx, "users.get", p, &users); err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", fmt.Errorf("user %d not found", userID)
	}
	return strings.TrimSpace(users[0].FirstName + " " + users[0].LastName), nil
}

// flexString принимает и строку, и число (VK отдаёт ts по-разному).
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(string(b))
	return nil
}

type longPollServer struct {
	Key    string     `json:"key"`
	Server string     `json:"server"`
	TS     flexString `json:"ts"`
}

func (c *Client) getLongPollServer(ctx context.Context) (longPollServer, error) {
	p := url.Values{}
	p.Set("group_id", strconv.FormatInt(c.groupID, 10))
	var s longPollServer
	err := c.call(ctx, "groups.getLongPollServer", p, &s)
	return s, err
}

// Update — одно событие Long Poll.
type Update struct {
	Type    string          `json:"type"`
	Object  json.RawMessage `json:"object"`
	GroupID int64           `json:"group_id"`
}

type pollResponse struct {
	TS      flexString `json:"ts"`
	Updates []Update   `json:"updates"`
	Failed  int        `json:"failed"`
}

func (c *Client) poll(ctx context.Context, s longPollServer, ts string) (pollResponse, error) {
	q := url.Values{}
	q.Set("act", "a_check")
	q.Set("key", s.Key)
	q.Set("ts", ts)
	q.Set("wait", strconv.Itoa(longPollWait))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Server+"?"+q.Encode(), nil)
	if err != nil {
		return pollResponse{}, err
	}
	resp, err := c.longPoll.Do(req)
	if err != nil {
		return pollResponse{}, err
	}
	defer resp.Body.Close()
	var pr pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return pollResponse{}, fmt.Errorf("long poll decode: %w", err)
	}
	return pr, nil
}

// Listen получает события, пока не отменён ctx. Обработчик вызывается последовательно.
func (c *Client) Listen(ctx context.Context, handle func(Update)) error {
	srv, err := c.getLongPollServer(ctx)
	if err != nil {
		return fmt.Errorf("groups.getLongPollServer: %w", err)
	}
	ts := string(srv.TS)
	c.log.Info("long poll started", "group_id", c.groupID)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pr, err := c.poll(ctx, srv, ts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Warn("long poll request failed, retrying", "err", err)
			sleep(ctx, 3*time.Second)
			continue
		}
		switch pr.Failed {
		case 0:
			ts = string(pr.TS)
			for _, u := range pr.Updates {
				handle(u)
			}
		case 1: // история устарела — берём новый ts
			ts = string(pr.TS)
		default: // 2 — истёк ключ, 3 — потеряна информация: запрашиваем сервер заново
			for {
				s, err := c.getLongPollServer(ctx)
				if err == nil {
					srv = s
					if pr.Failed == 3 {
						ts = string(s.TS)
					}
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				c.log.Warn("refresh long poll server failed", "err", err)
				sleep(ctx, 5*time.Second)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// ipv4Transport ходит в VK только по IPv4: адрес загрузки фото VK привязывает к IP,
// и если запрос адреса уйдёт по IPv6, а сама загрузка по IPv4 (или наоборот), VK вернёт пустой photo.
func ipv4Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", addr)
	}
	return t
}
