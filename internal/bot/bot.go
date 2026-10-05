// Package bot — диалоги бота: меню, сценарии «Купить», «Продать»,
// калькулятор ипотеки, «Связаться», заявки Олегу и команды администратора.
package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

// Sender — то, что бот делает в VK. Реализует *vk.Client; в тестах — фейк.
type Sender interface {
	Send(ctx context.Context, peerID int64, text string, kb *vk.Keyboard) error
	UserName(ctx context.Context, userID int64) (string, error)
}

// Incoming — входящее личное сообщение сообществу.
type Incoming struct {
	UserID  int64
	Text    string
	Payload map[string]string
	Photos  []string
}

type Options struct {
	AdminID    int64  // VK id Олега: ему приходят заявки, ему доступны /команды
	PrivacyURL string // ссылка на политику обработки персональных данных
	CallPhone  string // номер Олега для кнопки «Позвонить», формат +7XXXXXXXXXX
	Location   *time.Location
	Log        *slog.Logger
	Now        func() time.Time // для тестов; по умолчанию time.Now
}

type Bot struct {
	send  Sender
	store storage.Store
	opt   Options
	flows map[string]*flow
	log   *slog.Logger

	nightMu   sync.Mutex
	nightSeen map[int64]time.Time // когда клиенту в последний раз писали «отвечу с 9:00»

	mu     sync.RWMutex
	rates  map[string]float64 // программа → ставка, %
	prices map[string]float64 // район или город → ₽ за м²
}

const (
	settingRates  = "rates"
	settingPrices = "prices"
)

func New(ctx context.Context, send Sender, store storage.Store, opt Options) (*Bot, error) {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.Location == nil {
		opt.Location = time.UTC
	}
	b := &Bot{
		send: send, store: store, opt: opt, flows: buildFlows(), log: opt.Log,
		rates: map[string]float64{}, prices: map[string]float64{},
		nightSeen: map[int64]time.Time{},
	}
	if err := b.loadMap(ctx, settingRates, &b.rates); err != nil {
		return nil, err
	}
	if err := b.loadMap(ctx, settingPrices, &b.prices); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Bot) loadMap(ctx context.Context, key string, dst *map[string]float64) error {
	raw, ok, err := b.store.GetSetting(ctx, key)
	if err != nil || !ok {
		return err
	}
	m := map[string]float64{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	*dst = m
	return nil
}

func (b *Bot) saveMap(ctx context.Context, key string, m map[string]float64) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.store.SetSetting(ctx, key, raw)
}

// SeedPrices задаёт цены за м², только если их ещё ни разу не настраивали.
func (b *Bot) SeedPrices(ctx context.Context, defaults map[string]float64) error {
	_, ok, err := b.store.GetSetting(ctx, settingPrices)
	if err != nil || ok {
		return err
	}
	b.mu.Lock()
	for k, v := range defaults {
		b.prices[k] = v
	}
	cp := copyMap(b.prices)
	b.mu.Unlock()
	return b.saveMap(ctx, settingPrices, cp)
}

func copyMap(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type rateOption struct {
	Name string
	Rate float64
}

func (o rateOption) label() string { return o.Name + " — " + FormatNumber(o.Rate) + "%" }

func (b *Bot) rateList() []rateOption {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]rateOption, 0, len(b.rates))
	for n, r := range b.rates {
		out = append(out, rateOption{n, r})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// pricePerM2 ищет цену сначала по району, затем по городу.
func (b *Bot) pricePerM2(district, city string) (float64, string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, place := range []string{district, city} {
		if place == "" {
			continue
		}
		for k, v := range b.prices {
			if normalize(k) == normalize(place) {
				return v, k, true
			}
		}
	}
	return 0, "", false
}

// Handle обрабатывает одно входящее сообщение.
func (b *Bot) Handle(ctx context.Context, in Incoming) {
	text := strings.TrimSpace(in.Text)
	if in.UserID == b.opt.AdminID && strings.HasPrefix(text, "/") {
		b.admin(ctx, in.UserID, text)
		return
	}

	cmd := in.Payload["cmd"]
	if in.Payload["command"] == "start" {
		cmd = "menu"
	}
	isAdmin := in.UserID == b.opt.AdminID && b.opt.AdminID != 0
	if !isAdmin {
		b.nightNotice(ctx, in.UserID)
	}
	if isAdmin && b.adminButton(ctx, in.UserID, cmd) {
		return
	}
	switch norm := normalize(text); {
	case isAdmin && (cmd == "menu" || norm == "начать" || norm == "меню" || norm == "start" || norm == "старт" || norm == "панель"):
		b.dropSession(ctx, in.UserID)
		b.sendAdminPanel(ctx, in.UserID, "Панель управления 👇")
		return
	case cmd == "menu", text == btnMenu, norm == "начать", norm == "меню", norm == "start", norm == "старт":
		b.dropSession(ctx, in.UserID)
		b.sendMenu(ctx, in.UserID, "Здравствуйте! Я помогу подобрать или продать недвижимость. Что вас интересует?")
		return
	}

	if name := menuFlow(cmd, text); name != "" {
		b.startFlow(ctx, in.UserID, name, nil)
		return
	}

	s, err := b.store.GetSession(ctx, in.UserID)
	if err != nil {
		b.log.Error("get session", "user", in.UserID, "err", err)
		return
	}
	if s == nil {
		if isAdmin {
			b.sendAdminPanel(ctx, in.UserID, "Не понял команду. Панель управления 👇\nМожно написать: «сделай пост про ДСК» или /помощь")
			return
		}
		b.sendMenu(ctx, in.UserID, "Выберите, что вас интересует 👇")
		return
	}
	// Handle mailing mode
	if s.Flow == "mailing" {
		if norm := normalize(text); norm == "отмена" {
			b.dropSession(ctx, in.UserID)
			b.sendAdminPanel(ctx, in.UserID, "Рассылка отменена.")
			return
		}
		b.sendMailing(ctx, in.UserID, text)
		b.dropSession(ctx, in.UserID)
		return
	}
	b.advance(ctx, s, in, text)
}

func menuFlow(cmd, text string) string {
	switch {
	case cmd == flowBuy || text == btnBuy:
		return flowBuy
	case cmd == flowSell || text == btnSell:
		return flowSell
	case cmd == flowMortgage || text == btnMortgage:
		return flowMortgage
	case cmd == flowContact || text == btnContact:
		return flowContact
	}
	return ""
}

func payload(cmd string) string {
	raw, _ := json.Marshal(map[string]string{"cmd": cmd})
	return string(raw)
}

func menuKeyboard() *vk.Keyboard {
	return &vk.Keyboard{Buttons: [][]vk.Button{
		{vk.TextButton(btnBuy, payload(flowBuy), vk.ColorPrimary)},
		{vk.TextButton(btnSell, payload(flowSell), vk.ColorPositive)},
		{vk.TextButton(btnMortgage, payload(flowMortgage), vk.ColorPrimary)},
		{vk.TextButton(btnContact, payload(flowContact), vk.ColorPositive)},
	}}
}

func (b *Bot) sendMenu(ctx context.Context, peer int64, text string) {
	b.reply(ctx, peer, text, menuKeyboard())
}

func (b *Bot) reply(ctx context.Context, peer int64, text string, kb *vk.Keyboard) {
	if err := b.send.Send(ctx, peer, text, kb); err != nil {
		b.log.Error("send message", "peer", peer, "err", err)
	}
}

func (b *Bot) dropSession(ctx context.Context, user int64) {
	if err := b.store.DeleteSession(ctx, user); err != nil {
		b.log.Error("delete session", "user", user, "err", err)
	}
}

// notifyAdmin отправляет Олегу сообщение от имени сообщества.
func (b *Bot) notifyAdmin(ctx context.Context, text string) {
	if b.opt.AdminID == 0 {
		return
	}
	err := b.send.Send(ctx, b.opt.AdminID, text, nil)
	var apiErr *vk.APIError
	if errors.As(err, &apiErr) && apiErr.Code == vk.ErrCodeNoPermission {
		b.log.Error("admin has not allowed messages from the community: write any message to the group first", "admin", b.opt.AdminID)
		return
	}
	if err != nil {
		b.log.Error("notify admin", "err", err)
	}
}

// Ночью (с 00:00 до 09:00 по времени бота) клиенту один раз за ночь сообщаем, когда ответит Олег.
// Бот при этом работает как обычно: клиент может сразу пройти квиз.
const (
	nightFrom = 0
	nightTo   = 9
)

func (b *Bot) now() time.Time {
	if b.opt.Now != nil {
		return b.opt.Now()
	}
	return time.Now()
}

func (b *Bot) nightNotice(ctx context.Context, peer int64) {
	now := b.now().In(b.opt.Location)
	if h := now.Hour(); h < nightFrom || h >= nightTo {
		return
	}
	b.nightMu.Lock()
	last, seen := b.nightSeen[peer]
	if seen && now.Sub(last) < 8*time.Hour {
		b.nightMu.Unlock()
		return
	}
	b.nightSeen[peer] = now
	b.nightMu.Unlock()
	b.reply(ctx, peer, "🌙 Сейчас ночь — Олег ответит завтра с 9:00. А пока можно пройти квиз: ответьте на несколько вопросов, и я сразу пойму, чем помочь.", nil)
}

// sendMailing sends a message to all users who have submitted leads.
func (b *Bot) sendMailing(ctx context.Context, adminID int64, text string) {
	// Get all leads to find unique users
	leads, err := b.store.ListLeads(ctx, "", time.Time{})
	if err != nil {
		b.log.Error("list leads for mailing", "err", err)
		b.reply(ctx, adminID, "Ошибка: не удалось получить список получателей.", AdminKeyboard())
		return
	}

	// Extract unique user IDs from leads
	users := make(map[int64]bool)
	for _, lead := range leads {
		if lead.UserID > 0 {
			users[lead.UserID] = true
		}
	}

	if len(users) == 0 {
		b.reply(ctx, adminID, "Нет получателей для рассылки.", AdminKeyboard())
		return
	}

	// Send message to each user
	successCount := 0
	failCount := 0
	for userID := range users {
		if err := b.send.Send(ctx, userID, text, nil); err != nil {
			b.log.Error("send mailing", "user", userID, "err", err)
			failCount++
		} else {
			successCount++
		}
	}

	// Send summary to admin
	summary := fmt.Sprintf("✅ Рассылка завершена!\n\nУспешно: %d\nОшибок: %d\nВсего получателей: %d",
		successCount, failCount, len(users))
	b.reply(ctx, adminID, summary, AdminKeyboard())
}
