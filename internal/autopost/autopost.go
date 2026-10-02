// Package autopost — автопостинг в группу VK без браузера:
// очередь из репозитория → фото FLUX + оверлей → превью Олегу с кнопками → публикация по расписанию.
package autopost

import (
	"context"
	"encoding/json"
	"errors"
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
	Query     string    `json:"query,omitempty"`
	Overlay   string    `json:"overlay"`
	PhotoURL  string    `json:"photo_url,omitempty"` // фото, присланное с /срочно
	Slides    []Slide   `json:"slides,omitempty"`
	Image     string    `json:"image,omitempty"` // старый формат (одна картинка)
	Images    []string  `json:"images,omitempty"`
	Attempts  int       `json:"attempts,omitempty"`
	Error     string    `json:"error,omitempty"`
	VKPostID  int64     `json:"vk_post_id,omitempty"`
	Rerender  bool      `json:"rerender,omitempty"`   // картинки надо сделать заново (правка поста, «🔁»)
	Atts      []string  `json:"atts,omitempty"`       // фото уже загружены в группу — при публикации не грузим снова
	Fallback  string    `json:"fallback,omitempty"`   // почему обложка на фирменном фоне вместо фото
	FromDraft bool      `json:"from_draft,omitempty"` // выдан из запаса: дату назначил бот
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

// PostWriter запускает подготовку постов (задача Claude) с пожеланием Олега.
type PostWriter interface {
	Fire(ctx context.Context, wish string) (string, error)
}

// PhotoSearch ищет готовое фото по запросу (фотосток).
type PhotoSearch interface {
	Search(ctx context.Context, query string) ([]byte, error)
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
	Photos  PhotoSearch // nil — без фотостока
	// Writer запускает Claude, который делает посты по команде «сделай пост» (nil — команда недоступна)
	Writer   PostWriter
	lastFire time.Time
	// UploadGap — пауза между загрузками фото в VK (VK режет частые загрузки)
	UploadGap time.Duration
	Store     storage.Store
	DataDir   string
	HTTP      *http.Client // скачивание фото из сообщений
	Loc       *time.Location
	Log       *slog.Logger
	Now       func() time.Time

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
	m.retryErrors(ctx)
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
		if it.Draft && !ok {
			continue // лежит в запасе до кнопки «сделай пост»
		}
		if ok && st.FromDraft {
			it.PublishAt = st.PublishAt // дату выданному из запаса поста назначил бот
		}
		if !ok {
			states[it.ID] = &State{ID: it.ID, Status: StatusNew, PublishAt: it.PublishAt, Text: it.Text, Prompt: it.Prompt, Query: it.Query, Overlay: it.Overlay, Slides: it.Slides}
			continue
		}
		st.Query = it.Query // запрос к фотостоку не пересобирает уже готовые картинки
		changed := st.Text != it.Text || st.Prompt != it.Prompt || st.Overlay != it.Overlay || !st.PublishAt.Equal(it.PublishAt) || !sameSlides(st.Slides, it.Slides)
		if changed && (st.Status == StatusNew || st.Status == StatusAwaiting || st.Status == StatusError) {
			st.Text, st.Prompt, st.Overlay, st.PublishAt, st.Slides = it.Text, it.Prompt, it.Overlay, it.PublishAt, it.Slides
			st.Status, st.Attempts, st.Error = StatusNew, 0, ""
			st.Rerender, st.Atts = true, nil
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
	noTokenLogged := false
	tried := 0
	for _, st := range sorted(states) {
		if st.Status != StatusNew {
			continue
		}
		if tried > 0 && m.UploadGap > 0 {
			if tried >= 3 {
				break // остальные — в следующем цикле: не держим бота занятым и не злим VK частыми загрузками
			}
			time.Sleep(m.UploadGap)
		}
		tried++
		if err := m.prepare(ctx, st); err != nil {
			if errors.Is(err, ErrNoToken) {
				// не ошибка поста: ждём, пока в .env появится HF_TOKEN
				if !noTokenLogged {
					m.Log.Warn("autopost: HF_TOKEN не задан, посты с фото ждут")
					noTokenLogged = true
				}
				continue
			}
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

// maxSlides — VK принимает до 10 вложений в посте и сообщении.
const maxSlides = 10

func sameSlides(a, b []Slide) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (st *State) slideList() []Slide {
	if len(st.Slides) > 0 {
		if len(st.Slides) > maxSlides {
			return st.Slides[:maxSlides]
		}
		return st.Slides
	}
	return []Slide{{Prompt: st.Prompt, Query: st.Query, Overlay: st.Overlay}}
}

func (st *State) imageList() []string {
	if len(st.Images) > 0 {
		return st.Images
	}
	if st.Image != "" {
		return []string{st.Image}
	}
	return nil
}

func (m *Manager) imagePath(id string, i int) string {
	return filepath.Join(m.DataDir, "posts", fmt.Sprintf("%s-%02d.png", id, i+1))
}

func (m *Manager) prepare(ctx context.Context, st *State) error {
	var images, atts []string
	inGroup := true // все фото загружены в группу (ключом пользователя) — их можно сразу публиковать
	if st.Rerender {
		st.Fallback = ""
	}
	for i, sl := range st.slideList() {
		path := m.imagePath(st.ID, i)
		var img []byte
		if !st.Rerender {
			// уже сделанная картинка (например, упала только отправка превью) — не тратим генерацию повторно
			img, _ = os.ReadFile(path)
		}
		if len(img) == 0 {
			var err error
			img, err = m.renderSlide(ctx, st, i, sl)
			if err != nil {
				return fmt.Errorf("слайд %d: %w", i+1, err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return err
			}
			if err := os.WriteFile(path, img, 0o640); err != nil {
				return err
			}
		}
		images = append(images, path)
		if i > 0 && m.UploadGap > 0 {
			time.Sleep(m.UploadGap)
		}
		att, wall, err := m.uploadPreview(ctx, img)
		if err != nil {
			return fmt.Errorf("превью: %w", err)
		}
		inGroup = inGroup && wall
		atts = append(atts, att)
	}
	st.Images, st.Image, st.Rerender = images, "", false
	st.Atts = nil
	if inGroup {
		st.Atts = atts
	}
	slides := ""
	if len(images) > 1 {
		slides = fmt.Sprintf(" · %d слайдов", len(images))
	}
	text := fmt.Sprintf("📝 Пост на одобрение · %s%s\n🕒 %s\n\n%s", st.ID, slides, m.fmtTime(st.PublishAt), st.Text)
	if st.Fallback != "" {
		text = "⚠️ Фото не сгенерировалось, обложка на фирменном фоне. «🔁 Другое фото» — попробовать ещё раз.\nПричина: " + st.Fallback + "\n\n" + text
	}
	return m.Msg.SendAttachment(ctx, m.AdminID, text, strings.Join(atts, ","), m.previewButtons(st))
}

// uploadPreview: превью — фото в личные сообщения ключом сообщества (так VK их точно показывает);
// если ключу сообщества не хватает прав — загрузка в альбом группы ключом пользователя.
func (m *Manager) uploadPreview(ctx context.Context, img []byte) (att string, wall bool, err error) {
	att, err = m.Msg.UploadMessagePhoto(ctx, m.AdminID, img)
	if err == nil || m.Wall == nil {
		return att, false, err
	}
	m.Log.Warn("autopost: фото в сообщения не загрузилось, пробую через группу", "err", err)
	att, err = m.Wall.UploadWallPhoto(ctx, m.GroupID, img)
	return att, err == nil, err
}

func (m *Manager) renderSlide(ctx context.Context, st *State, i int, sl Slide) ([]byte, error) {
	var photo []byte
	if i == 0 && st.PhotoURL != "" {
		p, err := m.download(ctx, st.PhotoURL)
		if err != nil {
			return nil, fmt.Errorf("фото из сообщения: %w", err)
		}
		photo = p
	} else if sl.Photo != "" {
		p, err := m.Content.Overlay(ctx, sl.Photo)
		if err != nil {
			return nil, fmt.Errorf("фото слайда: %w", err)
		}
		photo = p
	} else if sl.Prompt != "" || sl.Query != "" {
		p, err := m.photoFor(ctx, sl)
		switch {
		case err == nil:
			photo = p
		case errors.Is(err, ErrNoToken) || st.Attempts < 2:
			return nil, err
		default:
			// третья попытка: не держим пост — делаем обложку на фирменном фоне
			m.Log.Warn("autopost: генерация не удалась, фирменный фон", "id", st.ID, "err", err)
			msg := err.Error()
			if len(msg) > 200 {
				msg = msg[:200] + "…"
			}
			st.Fallback = msg
		}
	}
	overlay, err := m.Content.Overlay(ctx, sl.Overlay)
	if err != nil {
		return nil, err
	}
	return Compose(photo, overlay)
}

// photoFor: сначала настоящее фото со стока по запросу (Query), затем генерация по описанию (Prompt).
func (m *Manager) photoFor(ctx context.Context, sl Slide) ([]byte, error) {
	var errs []string
	noKey := false
	try := func(img []byte, err error) []byte {
		switch {
		case err == nil:
			return img
		case errors.Is(err, ErrNoToken):
			noKey = true
		default:
			errs = append(errs, err.Error())
		}
		return nil
	}
	if sl.Query != "" && m.Photos != nil {
		if img := try(m.Photos.Search(ctx, sl.Query)); img != nil {
			return img, nil
		}
	}
	if sl.Prompt != "" && m.Gen != nil {
		if img := try(m.Gen.Generate(ctx, sl.Prompt)); img != nil {
			return img, nil
		}
	}
	if len(errs) == 0 && noKey {
		return nil, ErrNoToken
	}
	if len(errs) == 0 {
		return nil, nil
	}
	return nil, errors.New(strings.Join(errs, "; "))
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
	case isMakePost(text) || cmd == "adm_post":
		m.notify(ctx, m.makePosts(ctx, text), "", nil)
	case strings.EqualFold(strings.TrimSpace(text), "/очередь") || cmd == "adm_queue":
		m.notify(ctx, m.list(ctx), "", nil)
	case strings.EqualFold(strings.TrimSpace(text), "/обновить") || cmd == "adm_refresh":
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
		atts := st.Atts
		if len(atts) != len(st.imageList()) {
			atts = nil
		}
		for _, path := range st.imageList() {
			if len(st.Atts) == len(st.imageList()) {
				break
			}
			img, err := os.ReadFile(path)
			if err != nil {
				return "Не нашёл картинку: " + err.Error()
			}
			a, err := m.Wall.UploadWallPhoto(ctx, m.GroupID, img)
			if err != nil {
				return "⚠️ VK не принял фото: " + err.Error()
			}
			atts = append(atts, a)
		}
		att := strings.Join(atts, ",")
		var publishDate int64
		if !immediately && st.PublishAt.After(m.now().Add(10*time.Minute)) {
			publishDate = st.PublishAt.Unix()
		}
		att = stripAccessKeys(att)
		postID, err := m.Wall.WallPost(ctx, m.GroupID, st.Text, att, publishDate)
		if err != nil && strings.Contains(err.Error(), "error 10:") {
			// «Internal server error» у VK бывает разовым — пробуем ещё раз
			time.Sleep(m.retryPause())
			postID, err = m.Wall.WallPost(ctx, m.GroupID, st.Text, att, publishDate)
		}
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
		st.Rerender, st.Atts = true, nil
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

// retryErrors при старте даёт постам с ошибкой новые попытки: после перезапуска
// обычно что-то исправлено (ключ, провайдер), и жать «🔁» под каждым не нужно.
func (m *Manager) retryErrors(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return
	}
	const migKey = "autopost_resend_access_key"
	_, done, _ := m.Store.GetSetting(ctx, migKey)
	resend := !done
	if resend {
		_ = m.Store.SetSetting(ctx, migKey, json.RawMessage(`true`))
	}
	for _, st := range states {
		if st.Status == StatusError {
			st.Status, st.Attempts, st.Error = StatusNew, 0, ""
		}
		// один раз: превью, отправленные без ключа доступа к фото (картинка не была видна), — отправить заново
		if resend && st.Status == StatusAwaiting && len(st.Atts) > 0 && strings.Count(st.Atts[0], "_") < 2 {
			st.Status, st.Atts = StatusNew, nil
		}
	}
	if err := m.save(ctx, states); err != nil {
		m.Log.Error("autopost save", "err", err)
	}
}

// stripAccessKeys: на стене свои фото группы прикладываются без ключа доступа (photo-1_2_key → photo-1_2).
func stripAccessKeys(att string) string {
	parts := strings.Split(att, ",")
	for i, p := range parts {
		if f := strings.Split(p, "_"); len(f) > 2 {
			parts[i] = f[0] + "_" + f[1]
		}
	}
	return strings.Join(parts, ",")
}

func (m *Manager) retryPause() time.Duration {
	if m.UploadGap > 0 {
		return m.UploadGap
	}
	return 0
}

var makePostPrefixes = []string{"сделай посты", "сделай пост", "сделать посты", "сделать пост", "/пост"}

func isMakePost(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, p := range makePostPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// makePosts запускает Claude: он соберёт факты с сайтов застройщиков и добавит посты в очередь.
func (m *Manager) makePosts(ctx context.Context, text string) string {
	wish := wishOf(text)
	if wish == "" {
		// без пожеланий — сразу выдаём готовые посты из запаса, запас пополняем в фоне
		n, left, err := m.releaseDrafts(ctx, 3)
		if err != nil {
			m.Log.Error("autopost: запас", "err", err)
		}
		if n > 0 {
			if m.UploadGap > 0 {
				go m.Process(context.WithoutCancel(ctx)) // на сервере — в фоне, чтобы бот не ждал загрузок
			} else {
				m.Process(ctx)
			}
			msg := fmt.Sprintf("🚀 Беру %s из запаса — превью придут сюда через 1–2 минуты.", plural(n, "готовый пост", "готовых поста", "готовых постов"))
			if m.Writer != nil && left < 3 {
				if _, err := m.Writer.Fire(ctx, ""); err != nil {
					m.Log.Error("autopost: пополнение запаса", "err", err)
				} else {
					msg += "\n📦 Пополняю запас в фоне."
				}
			}
			return msg
		}
	}
	if m.Writer == nil {
		return "⚠️ Запас пуст, а запуск Claude не настроен: на сервере нет ROUTINE_ID и ROUTINE_TOKEN."
	}
	m.mu.Lock()
	if since := m.now().Sub(m.lastFire); !m.lastFire.IsZero() && since < 15*time.Minute {
		m.mu.Unlock()
		return fmt.Sprintf("⏳ Посты уже готовятся (запуск в %s). Превью придут сюда — подождите немного.", m.lastFire.In(m.loc()).Format("15:04"))
	}
	m.lastFire = m.now()
	m.mu.Unlock()
	fire := wish
	if fire == "" {
		fire = "Запас пуст, Олег ждёт: сделай 3 поста сразу в очередь (не черновики), и ещё 3 черновика в запас."
	}
	if _, err := m.Writer.Fire(ctx, fire); err != nil {
		m.mu.Lock()
		m.lastFire = time.Time{}
		m.mu.Unlock()
		m.Log.Error("autopost: запуск подготовки постов", "err", err)
		return "⚠️ Не получилось запустить подготовку постов: " + err.Error()
	}
	msg := "🛠 Принял! Захожу на сайты застройщиков, собираю цены, сроки и акции и делаю посты в разных стилях."
	if wish != "" {
		msg += "\nПожелание: " + wish
	} else {
		msg += "\nЗапас был пуст — заодно положу новые посты в запас, в следующий раз будут сразу."
	}
	return msg + "\nПревью придут сюда на одобрение примерно через 15–30 минут."
}

func wishOf(text string) string {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	for _, p := range makePostPrefixes {
		if strings.HasPrefix(low, p) {
			return strings.TrimSpace(strings.TrimLeft(string([]rune(t)[len([]rune(p)):]), " ,.:-—!"))
		}
	}
	return ""
}

func plural(n int, one, few, many string) string {
	w := many
	switch {
	case n%10 == 1 && n%100 != 11:
		w = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		w = few
	}
	return fmt.Sprintf("%d %s", n, w)
}

// releaseDrafts выдаёт до n постов из запаса: назначает им ближайшие свободные даты (через день, 19:00)
// и ставит в работу. Возвращает, сколько выдано и сколько осталось в запасе.
func (m *Manager) releaseDrafts(ctx context.Context, n int) (int, int, error) {
	items, err := m.Content.Queue(ctx)
	if err != nil {
		return 0, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.load(ctx)
	if err != nil {
		return 0, 0, err
	}
	var drafts []Item
	for _, it := range items {
		if _, used := states[it.ID]; it.Draft && !used && strings.TrimSpace(it.Text) != "" {
			drafts = append(drafts, it)
		}
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].ID < drafts[j].ID })
	slot := m.nextSlot(states)
	given := 0
	for _, it := range drafts {
		if given == n {
			break
		}
		states[it.ID] = &State{ID: it.ID, Status: StatusNew, PublishAt: slot, Text: it.Text, Prompt: it.Prompt, Query: it.Query,
			Overlay: it.Overlay, Slides: it.Slides, FromDraft: true}
		slot = slot.AddDate(0, 0, 2)
		given++
	}
	if given == 0 {
		return 0, len(drafts), nil
	}
	return given, len(drafts) - given, m.save(ctx, states)
}

// nextSlot — первая свободная дата: через день после последнего запланированного поста, в 19:00.
func (m *Manager) nextSlot(states map[string]*State) time.Time {
	loc := m.loc()
	now := m.now().In(loc)
	last := time.Time{}
	for _, st := range states {
		if st.Status != StatusRejected && st.PublishAt.After(last) {
			last = st.PublishAt
		}
	}
	if last.After(now) {
		l := last.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day()+2, 19, 0, 0, 0, loc)
	}
	return time.Date(now.Year(), now.Month(), now.Day()+1, 19, 0, 0, 0, loc)
}
