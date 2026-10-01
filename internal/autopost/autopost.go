// Package autopost — автопостинг в группу VK без браузера:
// очередь из репозитория → фото FLUX + оверлей → превью Олегу с кнопками → публикация по расписанию.
package autopost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

const settingKey = "autopost"

// Статусы поста.
const (
	StatusNew       = "new"       // ждёт подготовки картинки
	StatusAwaiting  = "awaiting"  // превью отправлено, ждём решения
	StatusScheduled = "scheduled" // отложенная запись в VK создана
	StatusPublished = "published" // опубликован
	StatusRejected  = "rejected"
	StatusError     = "error" // не удалось сделать картинку
)

var statusTitle = map[string]string{
	StatusNew: "🛠 готовлю", StatusAwaiting: "⏳ ждёт одобрения", StatusScheduled: "🕒 запланирован",
	StatusPublished: "✅ опубликован", StatusRejected: "❌ отклонён", StatusError: "⚠️ ошибка",
}

type State struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	PublishAt time.Time `json:"publish_at"`
	Text      string    `json:"text"`
	Prompt    string    `json:"prompt"`
	Overlay   string    `json:"overlay"`
	PhotoURL  string    `json:"photo_url,omitempty"` // фото, присланное с /срочно
	Image     string    `json:"image,omitempty"`
	Attempts  int       `json:"attempts,omitempty"`
	Error     string    `json:"error,omitempty"`
	VKPostID  int64     `json:"vk_post_id,omitempty"`
}

// Messenger — личные сообщения Олегу (ключ группы).
type Messenger interface {
	UploadMessagePhoto(ctx context.Context, peerID int64, png []byte) (string, error)
	SendAttachment(ctx context.Context, peerID int64, text, attachment string, kb *vk.Keyboard) error
}

// Wall — публикация на стене группы (пользовательский ключ).
type Wall interface {
	UploadWallPhoto(ctx context.Context, groupID int64, png []byte) (string, error)
	WallPost(ctx context.Context, groupID int64, message, attachments string, publishDate int64) (int64, error)
}

type Generator interface {
	Generate(ctx context.Context, prompt string) ([]byte, error)
}

type ContentSource interface {
	Queue(ctx context.Context) ([]Item, error)
	Overlay(ctx context.Context, path string) ([]byte, error)
}

// UrgentOverlay — плашка для постов /срочно (путь в content/).
const UrgentOverlay = "urgent_overlay.png"

type Manager struct {
	GroupID int64
	AdminID int64
	Msg     Messenger
	Wall    Wall // nil — нет VK_USER_TOKEN, публикация недоступна
	Content ContentSource
	Gen     Generator
	Store   storage.Store
	DataDir string
	HTTP    *http.Client // скачивание фото из сообщений
	Loc     *time.Location
	Log     *slog.Logger
	Now     func() time.Time

	mu sync.Mutex
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) load(ctx context.Context) (map[string]*State, error) {
	raw, ok, err := m.Store.GetSetting(ctx, settingKey)
	states := map[string]*State{}
	if err != nil || !ok {
		return states, err
	}
	return states, json.Unmarshal(raw, &states)
}

func (m *Manager) save(ctx context.Context, states map[string]*State) error {
	raw, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return m.Store.SetSetting(ctx, settingKey, raw)
}

// Run: синхронизация очереди и подготовка превью раз в interval.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		m.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick — один цикл: забрать очередь, подготовить новые посты и отправить превью.
func (m *Manager) Tick(ctx context.Context) {
	if err := m.Sync(ctx); err != nil {
		m.Log.Warn("autopost sync", "err", err)
	}
	m.Process(ctx)
}

// Sync добавляет новые посты из очереди и обновляет ещё не одобренные.
func (m *Manager) Sync(ctx context.Context) error {
	items, err := m.Content.Queue(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.ID == "" || strings.TrimSpace(it.Text) == "" {
			continue
		}
		st, ok := states[it.ID]
		if !ok {
			states[it.ID] = &State{ID: it.ID, Status: StatusNew, PublishAt: it.PublishAt, Text: it.Text, Prompt: it.Prompt, Overlay: it.Overlay}
			continue
		}
		changed := st.Text != it.Text || st.Prompt != it.Prompt || st.Overlay != it.Overlay || !st.PublishAt.Equal(it.PublishAt)
		if changed && (st.Status == StatusNew || st.Status == StatusAwaiting || st.Status == StatusError) {
			st.Text, st.Prompt, st.Overlay, st.PublishAt = it.Text, it.Prompt, it.Overlay, it.PublishAt
			st.Status, st.Attempts, st.Error = StatusNew, 0, ""
		}
	}
	return m.save(ctx, states)
}

// Process готовит картинки для новых постов и отправляет превью.
func (m *Manager) Process(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		m.Log.Error("autopost load", "err", err)
		return
	}
	for _, st := range sorted(states) {
		if st.Status != StatusNew {
			continue
		}
		if err := m.prepare(ctx, st); err != nil {
			st.Attempts++
			st.Error = err.Error()
			m.Log.Warn("autopost prepare", "id", st.ID, "attempt", st.Attempts, "err", err)
			if st.Attempts >= 3 {
				st.Status = StatusError
				m.notify(ctx, fmt.Sprintf("⚠️ Пост «%s»: не получилось сделать картинку.\n%s", st.ID, st.Error), "", m.buttons(st.ID, true))
			}
			continue
		}
		st.Status, st.Error, st.Attempts = StatusAwaiting, "", 0
	}
	if err := m.save(ctx, states); err != nil {
		m.Log.Error("autopost save", "err", err)
	}
}

func (m *Manager) imagePath(id string) string { return filepath.Join(m.DataDir, "posts", id+".png") }

func (m *Manager) prepare(ctx context.Context, st *State) error {
	var photo []byte
	if st.PhotoURL != "" {
		p, err := m.download(ctx, st.PhotoURL)
		if err != nil {
			return fmt.Errorf("фото из сообщения: %w", err)
		}
		photo = p
	} else if st.Prompt != "" {
		p, err := m.Gen.Generate(ctx, st.Prompt)
		if err != nil {
			return err
		}
		photo = p
	}
	overlay, err := m.Content.Overlay(ctx, st.Overlay)
	if err != nil {
		return err
	}
	img, err := Compose(photo, overlay)
	if err != nil {
		return err
	}
	path := m.imagePath(st.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, img, 0o640); err != nil {
		return err
	}
	st.Image = path
	att, err := m.Msg.UploadMessagePhoto(ctx, m.AdminID, img)
	if err != nil {
		return fmt.Errorf("превью: %w", err)
	}
	text := fmt.Sprintf("📝 Пост на одобрение · %s\n🕒 %s\n\n%s", st.ID, m.fmtTime(st.PublishAt), st.Text)
	return m.Msg.SendAttachment(ctx, m.AdminID, text, att, m.previewButtons(st))
}

func (m *Manager) download(ctx context.Context, u string) ([]byte, error) {
	hc := m.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

// previewButtons: для поста с будущей датой добавляет «⚡ Сейчас».
func (m *Manager) previewButtons(st *State) *vk.Keyboard {
	kb := m.buttons(st.ID, false)
	if st.PublishAt.After(m.now().Add(10 * time.Minute)) {
		now := vk.TextButton("⚡ Сейчас", payload("ap_now", st.ID), vk.ColorPositive)
		kb.Buttons[0] = append(kb.Buttons[0], now)
		kb.Buttons[0][0] = vk.TextButton("✅ По расписанию", payload("ap_ok", st.ID), vk.ColorPositive)
	}
	return kb
}

func (m *Manager) fmtTime(t time.Time) string {
	if t.IsZero() {
		return "сразу после одобрения"
	}
	loc := m.Loc
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("02.01.2006 15:04")
}

func payload(cmd, id string) string {
	raw, _ := json.Marshal(map[string]string{"cmd": cmd, "id": id})
	return string(raw)
}

func (m *Manager) buttons(id string, onlyRedo bool) *vk.Keyboard {
	redo := vk.TextButton("🔁 Другое фото", payload("ap_redo", id), vk.ColorPrimary)
	no := vk.TextButton("❌ Отклонить", payload("ap_no", id), vk.ColorNegative)
	if onlyRedo {
		return &vk.Keyboard{Inline: true, Buttons: [][]vk.Button{{redo, no}}}
	}
	ok := vk.TextButton("✅ Опубликовать", payload("ap_ok", id), vk.ColorPositive)
	return &vk.Keyboard{Inline: true, Buttons: [][]vk.Button{{ok}, {redo, no}}}
}

func (m *Manager) notify(ctx context.Context, text, att string, kb *vk.Keyboard) {
	if err := m.Msg.SendAttachment(ctx, m.AdminID, text, att, kb); err != nil {
		m.Log.Error("autopost notify", "err", err)
	}
}

func sorted(states map[string]*State) []*State {
	out := make([]*State, 0, len(states))
	for _, s := range states {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PublishAt.Equal(out[j].PublishAt) {
			return out[i].PublishAt.Before(out[j].PublishAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// HandleAdmin обрабатывает кнопки превью и команды /очередь, /обновить. true — сообщение обработано.
func (m *Manager) HandleAdmin(ctx context.Context, userID int64, text string, pl map[string]string, photos ...string) bool {
	if userID != m.AdminID {
		return false
	}
	cmd, id := pl["cmd"], pl["id"]
	switch {
	case cmd == "ap_ok":
		m.notify(ctx, m.approve(ctx, id, false), "", nil)
	case cmd == "ap_now":
		m.notify(ctx, m.approve(ctx, id, true), "", nil)
	case isUrgent(text):
		m.notify(ctx, m.urgent(ctx, text, photos), "", nil)
		m.Process(ctx)
	case cmd == "ap_no":
		m.notify(ctx, m.reject(ctx, id), "", nil)
	case cmd == "ap_redo":
		m.notify(ctx, m.redo(ctx, id), "", nil)
		m.Process(ctx)
	case strings.EqualFold(strings.TrimSpace(text), "/очередь"):
		m.notify(ctx, m.list(ctx), "", nil)
	case strings.EqualFold(strings.TrimSpace(text), "/обновить"):
		m.notify(ctx, "🔄 Забираю очередь постов…", "", nil)
		m.Tick(ctx)
		m.notify(ctx, m.list(ctx), "", nil)
	default:
		return false
	}
	return true
}

func (m *Manager) withState(ctx context.Context, id string, f func(st *State) string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	st, ok := states[id]
	if !ok {
		return "Не нашёл пост " + id
	}
	msg := f(st)
	if err := m.save(ctx, states); err != nil {
		return "Не сохранилось: " + err.Error()
	}
	return msg
}

func isUrgent(text string) bool {
	f := strings.Fields(strings.ToLower(text))
	return len(f) > 0 && f[0] == "/срочно"
}

// urgent создаёт внеочередной пост из текста после /срочно (и первого фото, если приложено).
func (m *Manager) urgent(ctx context.Context, text string, photos []string) string {
	body := strings.TrimSpace(text)
	body = strings.TrimSpace(body[len(strings.Fields(body)[0]):])
	if body == "" {
		return "Напишите текст поста после команды: /срочно Текст поста… (можно приложить фото)"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	id := "srochno-" + m.now().In(m.loc()).Format("2006-01-02-1504")
	for i := 2; states[id] != nil; i++ {
		id = fmt.Sprintf("srochno-%s-%d", m.now().In(m.loc()).Format("2006-01-02-1504"), i)
	}
	st := &State{ID: id, Status: StatusNew, Text: body, Overlay: UrgentOverlay}
	if len(photos) > 0 {
		st.PhotoURL = photos[0]
	}
	states[id] = st
	if err := m.save(ctx, states); err != nil {
		return "Не сохранилось: " + err.Error()
	}
	return "⚡ Готовлю внеочередной пост — превью придёт через несколько секунд."
}

func (m *Manager) loc() *time.Location {
	if m.Loc != nil {
		return m.Loc
	}
	return time.Local
}

func (m *Manager) approve(ctx context.Context, id string, immediately bool) string {
	return m.withState(ctx, id, func(st *State) string {
		switch st.Status {
		case StatusScheduled, StatusPublished:
			return "Этот пост уже " + statusTitle[st.Status] + "."
		case StatusAwaiting:
		default:
			return "Пост ещё не готов: " + statusTitle[st.Status]
		}
		if m.Wall == nil {
			return "⚠️ Публикация недоступна: на сервере не задан VK_USER_TOKEN."
		}
		img, err := os.ReadFile(st.Image)
		if err != nil {
			return "Не нашёл картинку: " + err.Error()
		}
		att, err := m.Wall.UploadWallPhoto(ctx, m.GroupID, img)
		if err != nil {
			return "⚠️ VK не принял фото: " + err.Error()
		}
		var publishDate int64
		if !immediately && st.PublishAt.After(m.now().Add(10*time.Minute)) {
			publishDate = st.PublishAt.Unix()
		}
		postID, err := m.Wall.WallPost(ctx, m.GroupID, st.Text, att, publishDate)
		if err != nil {
			return "⚠️ VK не опубликовал: " + err.Error()
		}
		st.VKPostID = postID
		if publishDate > 0 {
			st.Status = StatusScheduled
			return fmt.Sprintf("🕒 Запланировано на %s — VK опубликует сам.", m.fmtTime(st.PublishAt))
		}
		st.Status = StatusPublished
		return fmt.Sprintf("✅ Опубликовано: vk.com/wall-%d_%d", m.GroupID, postID)
	})
}

func (m *Manager) reject(ctx context.Context, id string) string {
	return m.withState(ctx, id, func(st *State) string {
		if st.Status == StatusScheduled || st.Status == StatusPublished {
			return "Пост уже " + statusTitle[st.Status] + " — удалить можно в группе VK."
		}
		st.Status = StatusRejected
		return "❌ Пост «" + id + "» отклонён."
	})
}

func (m *Manager) redo(ctx context.Context, id string) string {
	return m.withState(ctx, id, func(st *State) string {
		if st.Status == StatusScheduled || st.Status == StatusPublished {
			return "Пост уже " + statusTitle[st.Status] + "."
		}
		st.Status, st.Attempts, st.Error = StatusNew, 0, ""
		return "🔁 Делаю другое фото для «" + id + "»…"
	})
}

func (m *Manager) list(ctx context.Context) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	if len(states) == 0 {
		return "Очередь постов пуста."
	}
	var sb strings.Builder
	sb.WriteString("📅 Очередь постов:\n")
	for _, st := range sorted(states) {
		fmt.Fprintf(&sb, "\n%s · %s · %s", m.fmtTime(st.PublishAt), st.ID, statusTitle[st.Status])
	}
	return sb.String()
}
