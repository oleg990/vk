package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

type sent struct {
	peer int64
	text string
	kb   *vk.Keyboard
}

type fakeSender struct{ msgs []sent }

func (f *fakeSender) Send(_ context.Context, peer int64, text string, kb *vk.Keyboard) error {
	f.msgs = append(f.msgs, sent{peer, text, kb})
	return nil
}
func (f *fakeSender) UserName(context.Context, int64) (string, error) {
	return "Иван Петров", nil
}

func (f *fakeSender) last() sent { return f.msgs[len(f.msgs)-1] }

func (f *fakeSender) to(peer int64) []sent {
	var out []sent
	for _, m := range f.msgs {
		if m.peer == peer {
			out = append(out, m)
		}
	}
	return out
}

const (
	admin = int64(1)
	user  = int64(100)
)

func newBot(t *testing.T) (*Bot, *fakeSender, *storage.FileStore) {
	t.Helper()
	fs := &fakeSender{}
	st := storage.NewMemory()
	b, err := New(context.Background(), fs, st, Options{AdminID: admin, PrivacyURL: "https://example.ru/privacy", CallPhone: "+79205952888", Now: func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return b, fs, st
}

func say(b *Bot, from int64, text string) {
	b.Handle(context.Background(), Incoming{UserID: from, Text: text, Payload: map[string]string{}})
}

func hasButton(kb *vk.Keyboard, label string) bool {
	if kb == nil {
		return false
	}
	for _, row := range kb.Buttons {
		for _, btn := range row {
			if btn.Action.Label == label {
				return true
			}
		}
	}
	return false
}

func TestStartShowsMenu(t *testing.T) {
	b, fs, _ := newBot(t)
	b.Handle(context.Background(), Incoming{UserID: user, Payload: map[string]string{"command": "start"}})
	m := fs.last()
	if !hasButton(m.kb, btnBuy) || !hasButton(m.kb, btnSell) || !hasButton(m.kb, btnMortgage) {
		t.Fatalf("menu missing buttons: %+v", m.kb)
	}
}

func TestSellFlowCreatesLeadWithEstimate(t *testing.T) {
	b, fs, st := newBot(t)
	ctx := context.Background()
	if err := b.SeedPrices(ctx, map[string]float64{"Старый Оскол": 100000}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{
		btnSell, "Квартира", "Старый Оскол", "Юго-запад", btnSkip, "2", "50", "4/9",
		"Хороший ремонт", btnSkip, "Новостройка", "Не спешу",
	} {
		say(b, user, msg)
	}
	if !strings.Contains(fs.last().text, "согласие") || !hasButton(fs.last().kb, btnAgree) {
		t.Fatalf("expected consent step, got %q", fs.last().text)
	}
	if !strings.Contains(fs.last().text, "https://example.ru/privacy") {
		t.Fatal("privacy link missing")
	}
	say(b, user, btnAgree)
	say(b, user, "не номер")
	if !strings.Contains(fs.last().text, "Не похоже на номер") {
		t.Fatalf("phone validation: %q", fs.last().text)
	}
	say(b, user, "8 900 123 45 67")

	leads, _ := st.ListLeads(ctx, flowSell, time.Time{})
	if len(leads) != 1 {
		t.Fatalf("leads = %d", len(leads))
	}
	l := leads[0]
	if l.Phone != "+79001234567" || l.Name != "Иван Петров" || len(l.Photos) != 0 || l.ConsentAt.IsZero() {
		t.Fatalf("bad lead: %+v", l)
	}

	adminMsgs := fs.to(admin)
	if len(adminMsgs) != 1 {
		t.Fatalf("admin got %d messages", len(adminMsgs))
	}
	am := adminMsgs[0].text
	for _, want := range []string{"ПРОДАВЕЦ", "vk.com/id100", "+79001234567", "Площадь, м²: 50", "Двойная сделка", "4 500 000 ₽ – 5 500 000 ₽"} {
		if !strings.Contains(am, want) {
			t.Errorf("admin message missing %q:\n%s", want, am)
		}
	}
	if strings.Contains(am, "Фото") {
		t.Error("photos step must be gone")
	}
	if strings.Contains(am, "Адрес") {
		t.Error("skipped address must not appear")
	}
	if !strings.Contains(fs.last().text, "Предварительная оценка") || !hasButton(fs.last().kb, btnBuy) {
		t.Fatalf("user thanks = %q", fs.last().text)
	}
	if s, _ := st.GetSession(ctx, user); s != nil {
		t.Fatal("session must be cleared")
	}
}

func TestPlotSkipsApartmentQuestions(t *testing.T) {
	b, fs, _ := newBot(t)
	for _, msg := range []string{btnSell, "Участок", "Воронеж", "Шилово", btnSkip} {
		say(b, user, msg)
	}
	if !strings.Contains(fs.last().text, "сотках") {
		t.Fatalf("expected plot question, got %q", fs.last().text)
	}
}

func TestBackButton(t *testing.T) {
	b, fs, _ := newBot(t)
	say(b, user, btnBuy)
	say(b, user, "Воронеж")
	say(b, user, btnBack)
	if !strings.Contains(fs.last().text, "В каком городе") {
		t.Fatalf("back went to %q", fs.last().text)
	}
}

func TestInvalidChoiceReasks(t *testing.T) {
	b, fs, _ := newBot(t)
	say(b, user, btnBuy)
	say(b, user, "Воронеж")
	say(b, user, "что-то странное")
	if !strings.Contains(fs.last().text, "Выберите вариант") {
		t.Fatalf("got %q", fs.last().text)
	}
}

func TestMortgageConsultationLead(t *testing.T) {
	b, fs, st := newBot(t)
	for _, msg := range []string{btnMortgage, "Семейная ипотека на вторичку", btnAgree, "+79001112233"} {
		say(b, user, msg)
	}
	leads, _ := st.ListLeads(context.Background(), flowMortgage, time.Time{})
	if len(leads) != 1 {
		t.Fatalf("mortgage leads = %d", len(leads))
	}
	am := fs.to(admin)[0].text
	if !strings.Contains(am, "ИПОТЕКА") || !strings.Contains(am, "Семейная ипотека на вторичку") {
		t.Fatalf("admin msg:\n%s", am)
	}
	if !strings.Contains(fs.last().text, "по ипотеке") {
		t.Fatalf("thanks: %q", fs.last().text)
	}
}

func TestConsentRefusal(t *testing.T) {
	b, fs, st := newBot(t)
	say(b, user, btnContact)
	say(b, user, btnSkip)
	say(b, user, btnRefuse)
	if !strings.Contains(fs.last().text, "Без согласия") {
		t.Fatalf("got %q", fs.last().text)
	}
	leads, _ := st.ListLeads(context.Background(), "", time.Time{})
	if len(leads) != 0 || len(fs.to(admin)) != 0 {
		t.Fatal("no lead must be created without consent")
	}
}

func TestAdminCommands(t *testing.T) {
	b, fs, _ := newBot(t)
	say(b, admin, "/цена Юго-запад 120000")
	say(b, admin, "/цены")
	if !strings.Contains(fs.last().text, "Юго-запад — 120000 ₽/м²") {
		t.Fatalf("prices: %q", fs.last().text)
	}
	say(b, admin, "/заявки")
	if !strings.Contains(fs.last().text, "заявок нет") {
		t.Fatalf("leads: %q", fs.last().text)
	}
	// не-админ не может выполнять команды
	say(b, user, "/ставка Хак 1")
	if len(b.rateList()) != 0 {
		t.Fatal("non-admin changed rates")
	}
}

func TestDistrictPriceWins(t *testing.T) {
	b, _, _ := newBot(t)
	ctx := context.Background()
	say(b, admin, "/цена Старый Оскол 100000")
	say(b, admin, "/цена юго-запад 120000")
	est, ok := b.estimate(map[string]string{"object": "Квартира", "city": "Старый Оскол", "district": "Юго-Запад", "area": "50"})
	if !ok || !strings.Contains(est, "5 400 000 ₽ – 6 600 000 ₽") {
		t.Fatalf("estimate = %q %v", est, ok)
	}
	_ = ctx
}

func TestStatsCommand(t *testing.T) {
	b, fs, st := newBot(t)
	ctx := context.Background()
	_, _ = st.SaveLead(ctx, &storage.Lead{Kind: flowSell, Answers: []storage.Answer{{Key: "after", Value: "Новостройка"}}})
	_, _ = st.SaveLead(ctx, &storage.Lead{Kind: flowBuy})
	say(b, admin, "/статистика")
	out := fs.last().text
	for _, want := range []string{"Сегодня — всего 2", "ПРОДАВЕЦ: 1", "ПОКУПАТЕЛЬ: 1", "двойные сделки: 1", "30 дней — всего 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats missing %q:\n%s", want, out)
		}
	}
}

func TestMenuButtonsAllColored(t *testing.T) {
	for _, row := range menuKeyboard().Buttons {
		for _, btn := range row {
			if btn.Color == vk.ColorSecondary || btn.Color == "" {
				t.Errorf("menu button %q is not colored", btn.Action.Label)
			}
		}
	}
}

func TestStaryOskolSkipsMarket(t *testing.T) {
	b, fs, _ := newBot(t)
	say(b, user, btnBuy)
	say(b, user, "Старый Оскол")
	if !strings.Contains(fs.last().text, "Сколько комнат") {
		t.Fatalf("expected rooms step, got %q", fs.last().text)
	}
	say(b, user, btnBack)
	if !strings.Contains(fs.last().text, "В каком городе") {
		t.Fatalf("back should skip market, got %q", fs.last().text)
	}
}

func TestContactCallButton(t *testing.T) {
	b, fs, _ := newBot(t)
	say(b, user, btnContact)
	say(b, user, btnSkip)
	say(b, user, btnAgree)
	if !hasButton(fs.last().kb, btnCall) || !strings.Contains(fs.last().text, "8 999 456 78 90") {
		t.Fatalf("phone step: %q %+v", fs.last().text, fs.last().kb)
	}
	say(b, user, btnCall)
	um := fs.to(user)
	if last := um[len(um)-1]; !strings.Contains(last.text, "+7 920 595-28-88") || !hasButton(last.kb, btnCall) {
		t.Fatalf("call reply: %q", last.text)
	}
	if am := fs.to(admin); len(am) != 1 || !strings.Contains(am[0].text, "Позвонить") {
		t.Fatalf("admin not notified about call: %+v", am)
	}
	// в сценарии продажи кнопки звонка нет
	b2, fs2, _ := newBot(t)
	for _, m := range []string{btnSell, "Участок", "Воронеж", "Шилово", btnSkip, "8", btnSkip, "Просто продаю", "Не спешу", btnAgree} {
		say(b2, user, m)
	}
	if hasButton(fs2.last().kb, btnCall) {
		t.Fatal("call button must be only in contact flow")
	}
}

func TestAdminPanel(t *testing.T) {
	b, fs, _ := newBot(t)
	ctx := context.Background()
	b.Handle(ctx, Incoming{UserID: admin, Text: "Начать"})
	if fs.last().kb == nil || fs.last().kb.Buttons[0][0].Action.Label != "📝 Сделай пост" {
		t.Fatalf("admin must get the panel: %+v", fs.last())
	}
	b.Handle(ctx, Incoming{UserID: admin, Text: "📊 Статистика", Payload: map[string]string{"cmd": admStats}})
	if !strings.Contains(fs.last().text, "всего") {
		t.Fatalf("stats: %q", fs.last().text)
	}
	b.Handle(ctx, Incoming{UserID: admin, Text: "👀 Меню клиента", Payload: map[string]string{"cmd": admClient}})
	if fs.last().kb.Buttons[0][0].Action.Label != btnBuy {
		t.Fatal("client menu expected")
	}
	b.Handle(ctx, Incoming{UserID: 555, Text: "Начать"})
	if fs.last().kb.Buttons[0][0].Action.Label != btnBuy {
		t.Fatal("clients keep the client menu")
	}
}

func TestNightNotice(t *testing.T) {
	b, fs, _ := newBot(t)
	loc := time.FixedZone("MSK", 3*3600)
	b.opt.Location = loc
	night := time.Date(2026, 10, 2, 2, 30, 0, 0, loc)
	b.opt.Now = func() time.Time { return night }

	say(b, user, "Здравствуйте")
	msgs := fs.to(user)
	if len(msgs) < 2 || !strings.Contains(msgs[0].text, "с 9:00") {
		t.Fatalf("expected night notice first, got %+v", msgs)
	}
	n := len(msgs)
	say(b, user, "меню")
	if got := len(fs.to(user)); got != n+1 {
		t.Fatalf("notice must be sent once per night, messages %d -> %d", n, got)
	}
	if len(fs.to(admin)) != 0 {
		t.Fatal("admin must not get the notice")
	}
	say(b, admin, "меню")
	for _, m := range fs.to(admin) {
		if strings.Contains(m.text, "с 9:00") {
			t.Fatal("admin got night notice")
		}
	}
}

func TestNoNightNoticeDaytime(t *testing.T) {
	b, fs, _ := newBot(t)
	b.opt.Location = time.FixedZone("MSK", 3*3600)
	b.opt.Now = func() time.Time { return time.Date(2026, 10, 2, 9, 0, 0, 0, b.opt.Location) }
	say(b, user, "Здравствуйте")
	for _, m := range fs.to(user) {
		if strings.Contains(m.text, "с 9:00") {
			t.Fatal("notice at 09:00 sharp must not be sent")
		}
	}
}
