// Package stats — аналитика сообщества: разбор постов (просмотры, реакции), охвата и заявок,
// недельный отчёт Олегу и краткая сводка для задачи Claude «сделай пост».
package stats

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

// Source — откуда берутся цифры (vk.Client с ключом администратора).
type Source interface {
	WallPosts(ctx context.Context, groupID int64, count int) ([]vk.WallItem, error)
	GroupStats(ctx context.Context, groupID int64, from, to time.Time) (vk.StatPeriod, error)
}

// Messenger отправляет сообщение Олегу.
type Messenger interface {
	Send(ctx context.Context, peerID int64, text string, kb *vk.Keyboard) error
}

// MinPosts — меньше постов за период: выводы по темам и реакциям не делаем, только цифры.
// MinPostsTiming — столько постов нужно, чтобы говорить о лучшем дне и времени.
const (
	MinPosts       = 10
	MinPostsTiming = 15
)

// Marker отделяет сводку статистики от пожелания Олега в тексте запуска задачи Claude.
const Marker = "[СТАТИСТИКА]"

// Topic — результаты по теме постов.
type Topic struct {
	Name     string
	Posts    int
	AvgViews float64
	AvgER    float64 // реакции на 100 просмотров, %
}

// Analysis — разбор постов за период.
type Analysis struct {
	Days      int
	Posts     []vk.WallItem // за период, новые первыми
	AvgViews  float64
	AvgER     float64
	Comments  int
	Top, Low  []vk.WallItem
	Topics    []Topic // по убыванию средних просмотров
	BestDay   string  // пусто — мало данных
	BestSlot  string
	BestDayAv float64
	Notes     []string // рекомендации
}

func reactions(p vk.WallItem) int { return p.Likes + p.Comments + p.Reposts }

func er(p vk.WallItem) float64 {
	if p.Views == 0 {
		return 0
	}
	return float64(reactions(p)) / float64(p.Views) * 100
}

// TopicOf грубо определяет тему поста по тексту (эвристика по ключевым словам и хэштегам).
func TopicOf(text string) string {
	t := strings.ToLower(text)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("что важнее", "опрос", "голосуйте"):
		return "Опросы"
	case has("вопрос клиента", "вопрос:", "разбор:"):
		return "Вопрос–ответ"
	case has("#новостройки"):
		return "Новостройки и застройщики"
	case has("ипотек", "ставк"):
		return "Ипотека"
	case has("за м²", "за кв. м", "цены в", "средняя цена"):
		return "Цены за м²"
	case has("история", "сделк", "продали"):
		return "Истории сделок"
	}
	return "Другое"
}

func slotOf(h int) string {
	switch {
	case h < 12:
		return "утром"
	case h < 18:
		return "днём"
	}
	return "вечером"
}

var weekdays = [...]string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}

// Analyze разбирает посты за последние days дней. Посты без просмотров (ещё не набрали) не учитываются.
func Analyze(all []vk.WallItem, now time.Time, loc *time.Location, days int) Analysis {
	a := Analysis{Days: days}
	since := now.AddDate(0, 0, -days)
	for _, p := range all {
		if time.Unix(p.Date, 0).Before(since) || p.Views == 0 {
			continue
		}
		a.Posts = append(a.Posts, p)
	}
	sort.Slice(a.Posts, func(i, j int) bool { return a.Posts[i].Date > a.Posts[j].Date })
	n := len(a.Posts)
	if n == 0 {
		return a
	}
	var views, comments int
	var erSum float64
	for _, p := range a.Posts {
		views += p.Views
		comments += p.Comments
		erSum += er(p)
	}
	a.AvgViews, a.AvgER, a.Comments = float64(views)/float64(n), erSum/float64(n), comments

	byViews := append([]vk.WallItem(nil), a.Posts...)
	sort.Slice(byViews, func(i, j int) bool { return byViews[i].Views > byViews[j].Views })
	k := 3
	if n < 6 {
		k = n / 2
	}
	if k == 0 && n > 0 {
		k = 1
	}
	a.Top = byViews[:k]
	if n >= 4 {
		a.Low = byViews[n-k:]
	}

	type acc struct {
		n     int
		views int
		er    float64
	}
	topics := map[string]*acc{}
	slots := map[string]*acc{}
	add := func(m map[string]*acc, key string, p vk.WallItem) {
		x := m[key]
		if x == nil {
			x = &acc{}
			m[key] = x
		}
		x.n++
		x.views += p.Views
		x.er += er(p)
	}
	dayAcc := map[string]*acc{}
	for _, p := range a.Posts {
		add(topics, TopicOf(p.Text), p)
		tm := time.Unix(p.Date, 0).In(loc)
		add(dayAcc, weekdays[tm.Weekday()], p)
		add(slots, slotOf(tm.Hour()), p)
	}
	for name, x := range topics {
		a.Topics = append(a.Topics, Topic{Name: name, Posts: x.n, AvgViews: float64(x.views) / float64(x.n), AvgER: x.er / float64(x.n)})
	}
	sort.Slice(a.Topics, func(i, j int) bool {
		if a.Topics[i].AvgViews != a.Topics[j].AvgViews {
			return a.Topics[i].AvgViews > a.Topics[j].AvgViews
		}
		return a.Topics[i].Name < a.Topics[j].Name
	})
	if n >= MinPostsTiming {
		best := func(m map[string]*acc) (string, float64) {
			name, top := "", -1.0
			for k, x := range m {
				if x.n < 2 {
					continue
				}
				if av := float64(x.views) / float64(x.n); av > top || (av == top && k < name) {
					name, top = k, av
				}
			}
			return name, top
		}
		a.BestDay, a.BestDayAv = best(dayAcc)
		a.BestSlot, _ = best(slots)
	}
	a.Notes = a.recommend()
	return a
}

func (a Analysis) recommend() []string {
	var out []string
	n := len(a.Posts)
	if n < MinPosts {
		return append(out, fmt.Sprintf("Данных пока мало: %d постов с просмотрами, для выводов по темам и времени нужно хотя бы %d. Публикуйте регулярно, а пока растите охват: приглашайте людей в группу.", n, MinPosts))
	}
	for _, t := range a.Topics {
		if t.Posts >= 2 && t.AvgViews >= a.AvgViews*1.2 {
			out = append(out, fmt.Sprintf("«%s» заходит лучше всего: в среднем %.0f просмотров (+%.0f%% к среднему) — делайте чаще.", t.Name, t.AvgViews, (t.AvgViews/a.AvgViews-1)*100))
			break
		}
	}
	for i := len(a.Topics) - 1; i >= 0; i-- {
		t := a.Topics[i]
		if t.Posts >= 2 && t.AvgViews <= a.AvgViews*0.7 {
			out = append(out, fmt.Sprintf("«%s» слабее остальных (%.0f просмотров) — реже или другая подача: короче, с вопросом в конце.", t.Name, t.AvgViews))
			break
		}
	}
	for _, t := range a.Topics {
		if t.Posts >= 2 && t.AvgER >= a.AvgER*1.3 && t.AvgER > 0 {
			out = append(out, fmt.Sprintf("Сильнее всего реагируют на «%s»: %.1f реакций на 100 просмотров.", t.Name, t.AvgER))
			break
		}
	}
	if a.BestDay != "" && a.BestSlot != "" {
		out = append(out, fmt.Sprintf("Лучшее время для постов: %s и %s (в этот день в среднем %.0f просмотров).", a.BestDay, a.BestSlot, a.BestDayAv))
	}
	if n >= 3 && a.Comments == 0 {
		out = append(out, "Никто не комментирует — заканчивайте пост вопросом и ставьте рубрику «Что важнее?».")
	}
	return out
}

func firstLine(text string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(без текста)"
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	r := []rune(text)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return text
}

func link(groupID int64, p vk.WallItem) string {
	return fmt.Sprintf("vk.com/wall-%d_%d", groupID, p.ID)
}

// Data — всё, что попадает в отчёт.
type Data struct {
	GroupID        int64
	Analysis       Analysis
	Cur, Prev      *vk.StatPeriod // охват за последние 7 дней и за предыдущие 7; nil — нет доступа
	Leads, PrevLds int
}

func delta(cur, prev int) string {
	if prev == 0 {
		return fmt.Sprintf("%d", cur)
	}
	return fmt.Sprintf("%d (%+d%% к прошлой неделе)", cur, (cur-prev)*100/prev)
}

// Format — подробный отчёт Олегу.
func Format(d Data) string {
	a := d.Analysis
	var b strings.Builder
	fmt.Fprintf(&b, "📈 Аналитика группы за %d дн.\n", a.Days)
	if d.Cur != nil {
		prev := vk.StatPeriod{}
		if d.Prev != nil {
			prev = *d.Prev
		}
		fmt.Fprintf(&b, "\n👥 Охват за 7 дней: %s\nПосетители: %s\nПодписались: +%d, отписались: −%d\n", delta(d.Cur.Reach, prev.Reach), delta(d.Cur.Visitors, prev.Visitors), d.Cur.Subscribed, d.Cur.Unsubscribed)
	}
	fmt.Fprintf(&b, "\n📥 Заявок за 7 дней: %s\n", delta(d.Leads, d.PrevLds))
	if len(a.Posts) == 0 {
		b.WriteString("\nПостов с просмотрами за период пока нет. Опубликуйте первые — и я начну разбор.")
		return b.String()
	}
	fmt.Fprintf(&b, "\n📝 Постов: %d · в среднем %.0f просмотров, %.1f реакций на 100 просмотров\n", len(a.Posts), a.AvgViews, a.AvgER)
	b.WriteString("\n🏆 Лучшие:\n")
	for _, p := range a.Top {
		fmt.Fprintf(&b, "• %s — %d просм., %d ♥, %d 💬, %d ↪ (%s)\n", firstLine(p.Text, 50), p.Views, p.Likes, p.Comments, p.Reposts, link(d.GroupID, p))
	}
	if len(a.Low) > 0 {
		b.WriteString("\n🧊 Слабые:\n")
		for _, p := range a.Low {
			fmt.Fprintf(&b, "• %s — %d просм., %d реакций\n", firstLine(p.Text, 50), p.Views, reactions(p))
		}
	}
	b.WriteString("\n🗂 По темам:\n")
	for _, t := range a.Topics {
		fmt.Fprintf(&b, "• %s: %d, ср. %.0f просм., %.1f реакций/100\n", t.Name, t.Posts, t.AvgViews, t.AvgER)
	}
	if len(a.Notes) > 0 {
		b.WriteString("\n💡 Что делать:\n")
		for _, n := range a.Notes {
			b.WriteString("• " + n + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Brief — короткая сводка для задачи Claude: какие темы и когда заходят лучше.
func Brief(d Data) string {
	a := d.Analysis
	if len(a.Posts) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Постов за %d дн.: %d, в среднем %.0f просмотров, %.1f реакций на 100 просмотров.\n", a.Days, len(a.Posts), a.AvgViews, a.AvgER)
	b.WriteString("Темы по средним просмотрам: ")
	for i, t := range a.Topics {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s — %.0f (%d пост.)", t.Name, t.AvgViews, t.Posts)
	}
	b.WriteString(".\n")
	if len(a.Top) > 0 {
		fmt.Fprintf(&b, "Лучший пост: «%s» (%d просмотров).\n", firstLine(a.Top[0].Text, 60), a.Top[0].Views)
	}
	for _, n := range a.Notes {
		b.WriteString("- " + n + "\n")
	}
	return strings.TrimSpace(b.String())
}

// Reporter собирает данные и шлёт отчёт.
type Reporter struct {
	Src     Source
	Msg     Messenger
	Store   storage.Store
	GroupID int64
	AdminID int64
	Loc     *time.Location
	Log     *slog.Logger
	Now     func() time.Time
	Window  int // дней для разбора постов, по умолчанию 30
}

func (r *Reporter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Collect получает цифры из VK и заявки из хранилища. Охват — по возможности (нужно право stats).
func (r *Reporter) Collect(ctx context.Context) (Data, error) {
	now := r.now()
	days := r.Window
	if days <= 0 {
		days = 30
	}
	posts, err := r.Src.WallPosts(ctx, r.GroupID, 100)
	if err != nil {
		return Data{}, fmt.Errorf("стена: %w", err)
	}
	d := Data{GroupID: r.GroupID, Analysis: Analyze(posts, now, r.Loc, days)}
	week := 7 * 24 * time.Hour
	if cur, err := r.Src.GroupStats(ctx, r.GroupID, now.Add(-week), now); err != nil {
		r.logWarn("stats.get", err)
	} else {
		d.Cur = &cur
		if prev, err := r.Src.GroupStats(ctx, r.GroupID, now.Add(-2*week), now.Add(-week)); err == nil {
			d.Prev = &prev
		}
	}
	if r.Store != nil {
		if l, err := r.Store.ListLeads(ctx, "", now.Add(-2*week)); err == nil {
			for _, x := range l {
				if x.CreatedAt.After(now.Add(-week)) {
					d.Leads++
				} else {
					d.PrevLds++
				}
			}
		}
	}
	return d, nil
}

func (r *Reporter) logWarn(what string, err error) {
	if r.Log != nil {
		r.Log.Warn("аналитика: "+what, "err", err)
	}
}

// Report — текст подробного отчёта (для кнопки «Аналитика»).
func (r *Reporter) Report(ctx context.Context) string {
	d, err := r.Collect(ctx)
	if err != nil {
		return "⚠️ Не получилось собрать статистику: " + err.Error() + "\nДля просмотров нужен VK_USER_TOKEN администратора группы."
	}
	return Format(d)
}

// Brief — короткая сводка для задачи Claude; пусто, если данных нет или VK недоступен.
func (r *Reporter) Brief(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, err := r.Collect(ctx)
	if err != nil {
		r.logWarn("сводка для Claude", err)
		return ""
	}
	return Brief(d)
}

const lastWeekKey = "stats_last_week"

// Run раз в час проверяет: понедельник, 10:00 и позже, отчёт на этой неделе ещё не слали — шлёт.
func (r *Reporter) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick — одна проверка расписания (вынесена для тестов).
func (r *Reporter) Tick(ctx context.Context) {
	now := r.now().In(r.Loc)
	if now.Weekday() != time.Monday || now.Hour() < 10 || r.Store == nil {
		return
	}
	y, w := now.ISOWeek()
	key := fmt.Sprintf(`"%d-W%02d"`, y, w)
	if raw, ok, _ := r.Store.GetSetting(ctx, lastWeekKey); ok && string(raw) == key {
		return
	}
	if err := r.Msg.Send(ctx, r.AdminID, "🗓 Недельный разбор группы\n\n"+r.Report(ctx), nil); err != nil {
		r.logWarn("отправка отчёта", err)
		return
	}
	_ = r.Store.SetSetting(ctx, lastWeekKey, []byte(key))
}

// Writer добавляет сводку статистики к запуску задачи Claude «сделай пост».
type Writer struct {
	Inner interface {
		Fire(ctx context.Context, wish string) (string, error)
	}
	R *Reporter
}

func (w Writer) Fire(ctx context.Context, wish string) (string, error) {
	if brief := w.R.Brief(ctx); brief != "" {
		wish += "\n\n" + Marker + "\n" + brief
	}
	return w.Inner.Fire(ctx, wish)
}
