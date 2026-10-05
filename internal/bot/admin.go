package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"realty-bot/internal/autopost"
	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
	"sort"
	"strconv"
	"strings"
	"time"
)

const adminHelp = `Кнопки панели — внизу. Текстом можно:
сделай пост — пришлю 1 случайный пост из запаса; «✅ Опубликовать» — сразу на стену
сделай пост [пожелание] — Claude сделает новые посты по теме (например: «сделай пост про ДСК»), первый пришлю сразу как будет готов
меню — показать панель

Команды:
/заявки [дней] — все заявки (по умолчанию за сутки)
/продавцы [дней] — продавцы (по умолчанию 30 дней)
/покупатели [дней] — покупатели (по умолчанию 30 дней)
/ставки — программы ипотеки в калькуляторе
/ставка Название 6 — добавить или изменить программу
/удалить_ставку Название
/цены — цены за м² для оценки продавцам
/цена Район 114700 — цена за м² по району или городу
/удалить_цену Район
/очередь — все посты в запасе: нажмите номер, чтобы открыть и одобрить
/срочно Текст — внеочередной пост (можно приложить фото)
/обновить — забрать новые посты из очереди сейчас
/помощь — этот список`

func (b *Bot) admin(ctx context.Context, peer int64, text string) {
	fields := strings.Fields(text)
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	var out string
	switch cmd {
	case "/помощь", "/help", "/start":
		out = adminHelp
	case "/статистика", "/стат":
		out = b.stats(ctx)
	case "/заявки":
		out = b.listLeads(ctx, "", days(args, 1))
	case "/продавцы":
		out = b.listLeads(ctx, flowSell, days(args, 30))
	case "/покупатели":
		out = b.listLeads(ctx, flowBuy, days(args, 30))
	case "/ставки":
		out = b.showMap("Программы ипотеки", b.rates, "%")
	case "/ставка":
		out = b.setValue(ctx, settingRates, &b.rates, args, 0.01, 50, "%")
	case "/удалить_ставку":
		out = b.deleteValue(ctx, settingRates, &b.rates, args)
	case "/цены":
		out = b.showMap("Цены за м²", b.prices, " ₽/м²")
	case "/цена":
		out = b.setValue(ctx, settingPrices, &b.prices, args, 1000, 2_000_000, " ₽/м²")
	case "/удалить_цену":
		out = b.deleteValue(ctx, settingPrices, &b.prices, args)
	default:
		out = "Не знаю такой команды.\n\n" + adminHelp
	}
	b.reply(ctx, peer, out, nil)
}

func days(args []string, def int) int {
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 && n <= 3650 {
			return n
		}
	}
	return def
}

func (b *Bot) listLeads(ctx context.Context, kind string, d int) string {
	since := time.Now().Add(-time.Duration(d) * 24 * time.Hour)
	leads, err := b.store.ListLeads(ctx, kind, since)
	if err != nil {
		return "Ошибка чтения заявок: " + err.Error()
	}
	if len(leads) == 0 {
		return fmt.Sprintf("За %d дн. заявок нет.", d)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Заявок за %d дн.: %d\n\n", d, len(leads))
	for i, l := range leads {
		if i == 30 {
			fmt.Fprintf(&sb, "…и ещё %d", len(leads)-30)
			break
		}
		fmt.Fprintf(&sb, "#%d %s · %s · %s · %s · vk.com/id%d\n",
			l.ID, l.CreatedAt.In(b.opt.Location).Format("02.01 15:04"),
			leadKindTitle[l.Kind], l.Name, l.Phone, l.UserID)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *Bot) showMap(title string, m map[string]float64, unit string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(m) == 0 {
		return title + ": пока не заданы."
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(title + ":\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "• %s — %s%s\n", k, FormatNumber(m[k]), unit)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// splitNameValue: «Семейная ипотека 6» → «Семейная ипотека», 6.
func splitNameValue(args []string) (string, float64, bool) {
	if len(args) < 2 {
		return "", 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(args[len(args)-1], "%"), ",", "."), 64)
	if err != nil {
		return "", 0, false
	}
	return strings.Join(args[:len(args)-1], " "), v, true
}

func (b *Bot) setValue(ctx context.Context, key string, m *map[string]float64, args []string, min, max float64, unit string) string {
	name, v, ok := splitNameValue(args)
	if !ok || v < min || v > max {
		return fmt.Sprintf("Формат: название и число от %s до %s. Пример в /помощь.", FormatNumber(min), FormatNumber(max))
	}
	b.mu.Lock()
	for k := range *m { // одно название без учёта регистра
		if normalize(k) == normalize(name) {
			delete(*m, k)
		}
	}
	(*m)[name] = v
	cp := copyMap(*m)
	b.mu.Unlock()
	if err := b.saveMap(ctx, key, cp); err != nil {
		return "Не сохранилось: " + err.Error()
	}
	return fmt.Sprintf("Сохранено: %s — %s%s", name, FormatNumber(v), unit)
}

func (b *Bot) deleteValue(ctx context.Context, key string, m *map[string]float64, args []string) string {
	name := strings.Join(args, " ")
	b.mu.Lock()
	found := ""
	for k := range *m {
		if normalize(k) == normalize(name) {
			found = k
			delete(*m, k)
		}
	}
	cp := copyMap(*m)
	b.mu.Unlock()
	if found == "" {
		return "Не нашёл: " + name
	}
	if err := b.saveMap(ctx, key, cp); err != nil {
		return "Не сохранилось: " + err.Error()
	}
	return "Удалено: " + found
}

func (b *Bot) stats(ctx context.Context) string {
	now := time.Now().In(b.opt.Location)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, b.opt.Location)
	periods := []struct {
		name  string
		since time.Time
	}{
		{"Сегодня", today},
		{"7 дней", now.Add(-7 * 24 * time.Hour)},
		{"30 дней", now.Add(-30 * 24 * time.Hour)},
	}
	kinds := []string{flowBuy, flowSell, flowMortgage, flowContact}
	var sb strings.Builder
	sb.WriteString("📊 Статистика заявок\n")
	for _, p := range periods {
		leads, err := b.store.ListLeads(ctx, "", p.since)
		if err != nil {
			return "Ошибка чтения заявок: " + err.Error()
		}
		count := map[string]int{}
		double := 0
		for _, l := range leads {
			count[l.Kind]++
			for _, a := range l.Answers {
				if a.Key == "after" && a.Value == "Новостройка" {
					double++
				}
			}
		}
		fmt.Fprintf(&sb, "\n%s — всего %d\n", p.name, len(leads))
		for _, k := range kinds {
			if count[k] > 0 {
				fmt.Fprintf(&sb, "• %s: %d\n", leadKindTitle[k], count[k])
			}
		}
		if double > 0 {
			fmt.Fprintf(&sb, "• ⭐ двойные сделки: %d\n", double)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Кнопки панели Олега (payload cmd).
const (
	admPost    = "adm_post"
	admUrgent  = "adm_urgent"
	admQueue   = "adm_queue"
	admRefresh = "adm_refresh"
	admMailing = "adm_mailing"
	admHistory = "adm_history"
	admLeads   = "adm_leads"
	admSellers = "adm_sellers"
	admBuyers  = "adm_buyers"
	admClient  = "adm_client"
	admHelp    = "adm_help"
)

// AdminKeyboard — постоянная клавиатура Олега вместо клиентского меню.
func AdminKeyboard() *vk.Keyboard {
	btn := func(label, cmd string, color string) vk.Button { return vk.TextButton(label, payload(cmd), color) }
	return &vk.Keyboard{Buttons: [][]vk.Button{
		{btn("📝 Сделай пост", admPost, vk.ColorPositive), btn("⚡ Срочный пост", admUrgent, vk.ColorNegative)},
		{btn("📋 Очередь постов", admQueue, vk.ColorPrimary), btn("🔄 Обновить", admRefresh, vk.ColorSecondary)},
		{btn("📥 Заявки за сутки", admLeads, vk.ColorPrimary), btn("📧 История постов", admHistory, vk.ColorPrimary)},
		{btn("💰 Продавцы", admSellers, vk.ColorSecondary), btn("🏠 Покупатели", admBuyers, vk.ColorSecondary)},
		{btn("📬 Рассылка", admMailing, vk.ColorPositive)},
		{btn("👀 Меню клиента", admClient, vk.ColorSecondary), btn("❓ Помощь", admHelp, vk.ColorSecondary)},
	}}
}

func (b *Bot) sendAdminPanel(ctx context.Context, peer int64, text string) {
	b.reply(ctx, peer, text, AdminKeyboard())
}

// adminButton обрабатывает кнопки панели, которые относятся к заявкам и меню.
// Кнопки постов (сделай пост, очередь, обновить, срочный) обрабатывает автопостинг до бота.
func (b *Bot) adminButton(ctx context.Context, peer int64, cmd string) bool {
	var out string
	switch cmd {
	case admLeads:
		out = b.listLeads(ctx, "", 1)
	case admSellers:
		out = b.listLeads(ctx, flowSell, 30)
	case admBuyers:
		out = b.listLeads(ctx, flowBuy, 30)
	case admHistory:
		out = b.listPublishedPosts(ctx)
	case admMailing:
		out = b.prepareMailingList(ctx, peer)
	case admHelp:
		out = adminHelp
	case admUrgent:
		out = "Напишите /срочно и текст поста одним сообщением. Можно приложить фото — оно станет картинкой поста.\nПример: /срочно Снизили цену на 2-комнатную на Жукова!"
	case admClient:
		b.dropSession(ctx, peer)
		b.sendMenu(ctx, peer, "Так меню видят клиенты. Вернуться в панель — напишите «меню».")
		return true
	default:
		return false
	}
	b.reply(ctx, peer, out, AdminKeyboard())
	return true
}

// listPublishedPosts returns a formatted list of published posts.
func (b *Bot) listPublishedPosts(ctx context.Context) string {
	// Get autopost queue from storage
	raw, ok, err := b.store.GetSetting(ctx, "autopost")
	if err != nil || !ok {
		return "История постов не найдена."
	}

	// Parse posts
	var posts map[string]autopost.State
	if err := json.Unmarshal(raw, &posts); err != nil {
		return "Ошибка чтения истории: " + err.Error()
	}

	// Collect published posts (авто через VK_USER_TOKEN и отмеченные вручную)
	var published []struct {
		id     string
		date   time.Time
		text   string
		postID int64
		manual bool
	}

	for id, post := range posts {
		switch post.Status {
		case autopost.StatusPublished, autopost.StatusManual:
		default:
			continue
		}
		// Extract preview text (first 100 chars)
		preview := post.Text
		if len(preview) > 100 {
			preview = preview[:100] + "…"
		}
		published = append(published, struct {
			id     string
			date   time.Time
			text   string
			postID int64
			manual bool
		}{
			id:     id,
			date:   post.PublishAt,
			text:   preview,
			postID: post.VKPostID,
			manual: post.Status == autopost.StatusManual,
		})
	}

	if len(published) == 0 {
		return "История постов пока пуста."
	}

	// Sort by date descending (newest first)
	sort.Slice(published, func(i, j int) bool {
		return published[i].date.After(published[j].date)
	})

	var sb strings.Builder
	sb.WriteString("📧 История постов:\n\n")
	for i, p := range published {
		if i == 20 {
			fmt.Fprintf(&sb, "…и ещё %d\n", len(published)-20)
			break
		}
		dateStr := p.date.In(b.opt.Location).Format("02.01 15:04")
		mark := fmt.Sprintf("vk.com/wall-%d", p.postID)
		if p.manual || p.postID == 0 {
			mark = "✍️ вручную"
		}
		fmt.Fprintf(&sb, "#%d %s | %s\n%s\n\n", i+1, dateStr, mark, p.text)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// prepareMailingList returns a prompt for mailing or sends mailing to all users.
func (b *Bot) prepareMailingList(ctx context.Context, peer int64) string {
	// Create a session to track that admin is in mailing mode
	s := &storage.Session{
		UserID:    peer,
		Flow:      "mailing",
		Step:      0,
		Answers:   map[string]string{},
		UpdatedAt: time.Now(),
	}
	if err := b.store.SaveSession(ctx, s); err != nil {
		b.log.Error("save mailing session", "err", err)
		return "Ошибка: не удалось начать рассылку."
	}
	return `📬 Рассылка всем клиентам

Напишите сообщение одним текстом, и оно будет отправлено всем пользователям, которые оставляли заявки.

Пример: Уважаемые клиенты! У нас новая подборка объектов...

Для отмены напишите "отмена".`
}
