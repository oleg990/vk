package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const adminHelp = `Команды:
/статистика — заявки за сегодня, 7 и 30 дней по видам
/заявки [дней] — все заявки (по умолчанию за сутки)
/продавцы [дней] — продавцы (по умолчанию 30 дней)
/покупатели [дней] — покупатели (по умолчанию 30 дней)
/ставки — программы ипотеки в калькуляторе
/ставка Название 6 — добавить или изменить программу
/удалить_ставку Название
/цены — цены за м² для оценки продавцам
/цена Район 114700 — цена за м² по району или городу
/удалить_цену Район
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
