package autopost

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"realty-bot/internal/vk"
)

// Режим «по запросу» (OnDemand): расписания нет. Все посты из очереди — запас.
// «Сделай пост» присылает на одобрение один случайный пост из запаса, «✅ Опубликовать» публикует сразу.
// «Очередь постов» показывает весь запас, любой пост можно открыть и одобрить.

const stockPageSize = 9 // + кнопка «Ещё»: у inline-клавиатуры VK максимум 10 кнопок

// stockItem — пост из запаса для списка.
type stockItem struct {
	Item
	Shown bool // уже присылался на одобрение и ждёт решения
}

// stock — посты, которые ещё можно выдать: их нет в работе или они ждут решения.
func (m *Manager) stock(ctx context.Context) ([]stockItem, error) {
	items, err := m.Content.Queue(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	states, err := m.load(ctx)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []stockItem
	for _, it := range items {
		if it.ID == "" || strings.TrimSpace(it.Text) == "" {
			continue
		}
		st, ok := states[it.ID]
		switch {
		case !ok:
			out = append(out, stockItem{Item: it})
		case st.Status == StatusAwaiting || st.Status == StatusError:
			out = append(out, stockItem{Item: it, Shown: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID }) // свежие сверху
	return out, nil
}

// pick ставит пост из запаса в работу: картинки и превью с кнопкой «✅ Опубликовать».
func (m *Manager) pick(ctx context.Context, id string) string {
	items, err := m.Content.Queue(ctx)
	if err != nil {
		return "Не смог забрать очередь: " + err.Error()
	}
	var item *Item
	for i := range items {
		if items[i].ID == id {
			item = &items[i]
			break
		}
	}
	m.mu.Lock()
	states, err := m.load(ctx)
	if err != nil {
		m.mu.Unlock()
		return "Ошибка: " + err.Error()
	}
	st, ok := states[id]
	switch {
	case ok && (st.Status == StatusPublished || st.Status == StatusScheduled || st.Status == StatusManual):
		m.mu.Unlock()
		return "Этот пост уже " + statusTitle[st.Status] + "."
	case ok:
		// уже готовился — присылаем заново с теми же картинками
		st.Status, st.Attempts, st.Error = StatusNew, 0, ""
		st.PublishAt, st.FromDraft = time.Time{}, true
	case item == nil:
		m.mu.Unlock()
		return "Не нашёл пост " + id + " в очереди."
	default:
		states[id] = &State{ID: id, Status: StatusNew, Text: item.Text, Prompt: item.Prompt, Query: item.Query,
			Overlay: item.Overlay, Slides: item.Slides, FromDraft: true}
	}
	err = m.save(ctx, states)
	m.mu.Unlock()
	if err != nil {
		return "Не сохранилось: " + err.Error()
	}
	m.processSoon(ctx)
	return ""
}

func (m *Manager) processSoon(ctx context.Context) {
	if m.UploadGap > 0 {
		go m.Process(context.WithoutCancel(ctx)) // на сервере — в фоне, чтобы бот не ждал загрузок
		return
	}
	m.Process(ctx)
}

// makeOne — кнопка «Сделай пост» в режиме по запросу: один случайный пост из запаса.
func (m *Manager) makeOne(ctx context.Context, text string) string {
	wish := wishOf(text)
	stock, err := m.stock(ctx)
	if err != nil {
		return "Не смог забрать очередь: " + err.Error()
	}
	var fresh []stockItem
	for _, s := range stock {
		if !s.Shown {
			fresh = append(fresh, s)
		}
	}
	if wish == "" && len(stock) > 0 {
		from := fresh
		if len(from) == 0 {
			from = stock // новых нет — повторяем те, что ждут решения
		}
		chosen := from[rand.IntN(len(from))]
		if msg := m.pick(ctx, chosen.ID); msg != "" {
			return msg
		}
		left := len(fresh)
		if !chosen.Shown {
			left--
		}
		reply := "🎲 Готовлю случайный пост из запаса — пришлю через минуту."
		if left < 3 && m.Writer != nil {
			if _, err := m.Writer.Fire(ctx, ""); err != nil {
				m.Log.Error("autopost: пополнение запаса", "err", err)
			} else {
				reply += "\n📦 В запасе осталось мало — пополняю в фоне."
			}
		}
		return reply
	}
	// пожелание или пустой запас — просим Claude сделать новые и сразу покажем первый
	if m.Writer == nil {
		return "⚠️ Запас пуст, а запуск Claude не настроен: на сервере нет ROUTINE_ID и ROUTINE_TOKEN."
	}
	m.mu.Lock()
	if since := m.now().Sub(m.lastFire); !m.lastFire.IsZero() && since < 15*time.Minute {
		m.mu.Unlock()
		return fmt.Sprintf("⏳ Посты уже готовятся (запуск в %s). Первый пришлю сюда, как будет готов.", m.lastFire.In(m.loc()).Format("15:04"))
	}
	m.lastFire = m.now()
	m.known = map[string]bool{}
	items, _ := m.Content.Queue(ctx)
	for _, it := range items {
		m.known[it.ID] = true
	}
	m.wantNew = true
	m.mu.Unlock()
	if _, err := m.Writer.Fire(ctx, wish); err != nil {
		m.mu.Lock()
		m.lastFire, m.wantNew = time.Time{}, false
		m.mu.Unlock()
		m.Log.Error("autopost: запуск подготовки постов", "err", err)
		return "⚠️ Не получилось запустить подготовку постов: " + err.Error()
	}
	msg := "🛠 Принял! Делаю новые посты"
	if wish != "" {
		msg += " — " + wish
	} else {
		msg += " (запас был пуст)"
	}
	return msg + ". Первый пришлю сюда, как только будет готов, — примерно через 15–30 минут."
}

// checkNew: после запуска Claude ждём новые посты в очереди и сразу показываем первый.
func (m *Manager) checkNew(ctx context.Context) {
	m.mu.Lock()
	want := m.wantNew
	known := m.known
	m.mu.Unlock()
	if !want {
		return
	}
	items, err := m.Content.Queue(ctx)
	if err != nil {
		return
	}
	var fresh []string
	for _, it := range items {
		if !known[it.ID] && strings.TrimSpace(it.Text) != "" {
			fresh = append(fresh, it.ID)
		}
	}
	if len(fresh) == 0 {
		return
	}
	sort.Strings(fresh)
	m.mu.Lock()
	m.wantNew = false
	m.mu.Unlock()
	if msg := m.pick(ctx, fresh[0]); msg != "" {
		m.notify(ctx, msg, "", nil)
	}
}

// stockList — «Очередь постов»: весь запас с кнопками, любой пост можно открыть и одобрить.
func (m *Manager) stockList(ctx context.Context, page int) (string, *vk.Keyboard) {
	stock, err := m.stock(ctx)
	if err != nil {
		return "Не смог забрать очередь: " + err.Error(), nil
	}
	if len(stock) == 0 {
		return "📦 Запас пуст. Нажмите «📝 Сделай пост» — подготовлю новые.", nil
	}
	pages := (len(stock) + stockPageSize - 1) / stockPageSize
	if page < 0 || page >= pages {
		page = 0
	}
	from := page * stockPageSize
	to := min(from+stockPageSize, len(stock))
	var sb strings.Builder
	fmt.Fprintf(&sb, "📦 В запасе %s", plural(len(stock), "пост", "поста", "постов"))
	if pages > 1 {
		fmt.Fprintf(&sb, " (стр. %d из %d)", page+1, pages)
	}
	sb.WriteString(". Нажмите номер — пришлю пост, одобрите его кнопкой.\n")
	kb := &vk.Keyboard{Inline: true}
	var row []vk.Button
	for i := from; i < to; i++ {
		s := stock[i]
		mark := ""
		if s.Shown {
			mark = " 👀"
		}
		fmt.Fprintf(&sb, "\n%d. %s%s", i+1, headline(s.Text), mark)
		row = append(row, vk.TextButton(fmt.Sprint(i+1), payload("ap_pick", s.ID), vk.ColorPrimary))
		if len(row) == 5 {
			kb.Buttons = append(kb.Buttons, row)
			row = nil
		}
	}
	if pages > 1 {
		next := (page + 1) % pages
		row = append(row, vk.TextButton("Ещё ▶", fmt.Sprintf(`{"cmd":"ap_page","id":"%d"}`, next), vk.ColorSecondary))
	}
	if len(row) > 0 {
		kb.Buttons = append(kb.Buttons, row)
	}
	sb.WriteString("\n\n👀 — уже присылал, ждёт решения.")
	return sb.String(), kb
}

// headline — первая содержательная строка поста, коротко.
func headline(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		r := []rune(ln)
		if len(r) > 70 {
			return strings.TrimSpace(string(r[:68])) + "…"
		}
		return ln
	}
	return "(без текста)"
}
