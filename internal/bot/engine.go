package bot

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

type acceptStatus int

const (
	accepted acceptStatus = iota
	invalid
	photoAdded
	refused
)

type acceptResult struct {
	status acceptStatus
	value  string
	extra  map[string]string
	hint   string
}

// startFlow начинает сценарий; answers — ответы, перенесённые из прошлого сценария.
func (b *Bot) startFlow(ctx context.Context, user int64, name string, answers map[string]string) {
	f := b.flows[name]
	if answers == nil {
		answers = map[string]string{}
	}
	s := &storage.Session{UserID: user, Flow: name, Answers: answers}
	s.Step = b.nextStep(f, s, 0)
	if s.Step >= len(f.steps) {
		b.finish(ctx, f, s)
		return
	}
	b.save(ctx, s)
	b.ask(ctx, f, s, "")
}

func (b *Bot) save(ctx context.Context, s *storage.Session) {
	if err := b.store.SaveSession(ctx, s); err != nil {
		b.log.Error("save session", "user", s.UserID, "err", err)
	}
}

func (b *Bot) applies(st step, s *storage.Session) bool {
	return st.when == nil || st.when(s.Answers, b)
}

func (b *Bot) nextStep(f *flow, s *storage.Session, from int) int {
	i := from
	for i < len(f.steps) && !b.applies(f.steps[i], s) {
		i++
	}
	return i
}

func (b *Bot) prevStep(f *flow, s *storage.Session, from int) int {
	for i := from - 1; i >= 0; i-- {
		if b.applies(f.steps[i], s) {
			return i
		}
	}
	return -1
}

func (b *Bot) stepOptions(st step) []string {
	if st.kind == kindRateChoice {
		var out []string
		for _, r := range b.rateList() {
			out = append(out, r.label())
		}
		return append(out, btnOwnRate)
	}
	return st.options
}

func (b *Bot) stepKeyboard(f *flow, s *storage.Session, st step) *vk.Keyboard {
	var rows [][]vk.Button
	switch st.kind {
	case kindConsent:
		rows = append(rows, []vk.Button{
			vk.TextButton(btnAgree, "", vk.ColorPositive),
			vk.TextButton(btnRefuse, "", vk.ColorSecondary),
		})
	case kindPhotos:
		rows = append(rows, []vk.Button{vk.TextButton(btnDone, "", vk.ColorPositive)})
	case kindPhone:
		if st.callButton && b.opt.CallPhone != "" {
			rows = append(rows, []vk.Button{vk.TextButton(btnCall, "", vk.ColorPositive)})
		}
	default:
		var row []vk.Button
		for _, o := range b.stepOptions(st) {
			wide := len([]rune(o)) > 18
			if wide && len(row) > 0 {
				rows, row = append(rows, row), nil
			}
			row = append(row, vk.TextButton(o, "", vk.ColorPrimary))
			if wide || len(row) == 2 {
				rows, row = append(rows, row), nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		if st.optional {
			rows = append(rows, []vk.Button{vk.TextButton(btnSkip, "", vk.ColorSecondary)})
		}
	}
	nav := []vk.Button{}
	if b.prevStep(f, s, s.Step) >= 0 {
		nav = append(nav, vk.TextButton(btnBack, "", vk.ColorSecondary))
	}
	nav = append(nav, vk.TextButton(btnMenu, payload("menu"), vk.ColorNegative))
	// VK: не больше 10 рядов в обычной клавиатуре.
	if len(rows) > 9 {
		rows = rows[:9]
	}
	return &vk.Keyboard{Buttons: append(rows, nav)}
}

func (b *Bot) ask(ctx context.Context, f *flow, s *storage.Session, prefix string) {
	st := f.steps[s.Step]
	q := st.question
	if st.kind == kindConsent && b.opt.PrivacyURL != "" {
		q += "\n\nПолитика обработки данных: " + b.opt.PrivacyURL
	}
	if prefix != "" {
		q = prefix + "\n\n" + q
	}
	b.reply(ctx, s.UserID, q, b.stepKeyboard(f, s, st))
}

func matchOption(options []string, text string) (string, bool) {
	n := normalize(text)
	for _, o := range options {
		if normalize(o) == n {
			return o, true
		}
	}
	return "", false
}

func (b *Bot) accept(st step, s *storage.Session, in Incoming, text string) acceptResult {
	if st.optional && (text == btnSkip || normalize(text) == "пропустить") && st.kind != kindPhotos {
		return acceptResult{status: accepted}
	}
	bad := func(h string) acceptResult { return acceptResult{status: invalid, hint: h} }

	switch st.kind {
	case kindChoice:
		if o, ok := matchOption(st.options, text); ok {
			return acceptResult{status: accepted, value: o}
		}
		if st.freeText && text != "" {
			return acceptResult{status: accepted, value: truncate(text, 200)}
		}
		return bad("Выберите вариант на кнопках 👇")

	case kindText:
		if text == "" {
			return bad("Напишите ответ текстом 👇")
		}
		return acceptResult{status: accepted, value: truncate(text, 500)}

	case kindNumber:
		v, ok := ParseNumber(text)
		if !ok || v > 100000 {
			return bad("Напишите число, например: 44")
		}
		return acceptResult{status: accepted, value: strconv.FormatFloat(v, 'f', -1, 64)}

	case kindMoney:
		v, ok := ParseMoney(text)
		if !ok || v < 100_000 || v > 10_000_000_000 {
			return bad("Не понял сумму. Напишите, например: 5 500 000 или 5,5 млн")
		}
		return acceptResult{status: accepted, value: strconv.FormatFloat(v, 'f', 0, 64)}

	case kindDown:
		price, _ := strconv.ParseFloat(s.Answers["price"], 64)
		v, ok := ParseDown(text, price)
		if !ok {
			return bad("Взнос должен быть меньше стоимости. Напишите, например: 20% или 1,2 млн")
		}
		return acceptResult{status: accepted, value: strconv.FormatFloat(v, 'f', 0, 64)}

	case kindYears:
		y, ok := ParseYears(text)
		if !ok {
			return bad("Выберите срок на кнопках или напишите число лет от 1 до 35")
		}
		return acceptResult{status: accepted, value: strconv.Itoa(y)}

	case kindRateChoice:
		if normalize(text) == normalize(btnOwnRate) {
			return acceptResult{status: accepted, value: btnOwnRate}
		}
		for _, r := range b.rateList() {
			if normalize(r.label()) == normalize(text) || normalize(r.Name) == normalize(text) {
				return acceptResult{status: accepted, value: r.Name,
					extra: map[string]string{"rate": strconv.FormatFloat(r.Rate, 'f', -1, 64)}}
			}
		}
		return bad("Выберите программу на кнопках 👇")

	case kindRate:
		v, ok := ParseNumber(text)
		if !ok || v > 50 {
			return bad("Напишите ставку числом, например: 12,5")
		}
		return acceptResult{status: accepted, value: strconv.FormatFloat(v, 'f', -1, 64)}

	case kindPhotos:
		if len(in.Photos) > 0 {
			for _, p := range in.Photos {
				if len(s.Photos) < maxPhotos {
					s.Photos = append(s.Photos, p)
				}
			}
			return acceptResult{status: photoAdded}
		}
		if text == btnDone || normalize(text) == "готово" || text == btnSkip || normalize(text) == "пропустить" {
			return acceptResult{status: accepted, value: strconv.Itoa(len(s.Photos))}
		}
		return bad("Пришлите фото или нажмите «Готово» 👇")

	case kindConsent:
		switch n := normalize(text); {
		case text == btnAgree || n == "согласен" || n == "согласна" || n == "да":
			return acceptResult{status: accepted, value: time.Now().UTC().Format(time.RFC3339)}
		case text == btnRefuse || n == "отказаться" || n == "нет":
			return acceptResult{status: refused}
		}
		return bad("Нажмите «Согласен» или «Отказаться» 👇")

	case kindPhone:
		p, ok := ParsePhone(text)
		if !ok {
			return bad("Не похоже на номер. Напишите, например: 8 999 456 78 90")
		}
		return acceptResult{status: accepted, value: p}
	}
	return bad("")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (b *Bot) advance(ctx context.Context, s *storage.Session, in Incoming, text string) {
	f, ok := b.flows[s.Flow]
	if !ok || s.Step < 0 || s.Step >= len(f.steps) {
		b.dropSession(ctx, s.UserID)
		b.sendMenu(ctx, s.UserID, "Давайте начнём сначала 👇")
		return
	}

	if text == btnBack || normalize(text) == "назад" {
		if p := b.prevStep(f, s, s.Step); p >= 0 {
			s.Step = p
			b.save(ctx, s)
		}
		b.ask(ctx, f, s, "")
		return
	}

	st := f.steps[s.Step]
	if st.callButton && text == btnCall && b.opt.CallPhone != "" {
		b.reply(ctx, s.UserID, "Звоните, буду рад помочь: "+FormatPhone(b.opt.CallPhone)+"\n\nИли оставьте свой номер — перезвоню сам.", b.stepKeyboard(f, s, st))
		name, err := b.send.UserName(ctx, s.UserID)
		if err != nil || name == "" {
			name = fmt.Sprintf("id%d", s.UserID)
		}
		b.notifyAdmin(ctx, fmt.Sprintf("📞 %s — vk.com/id%d нажал «Позвонить», ждите звонка.", name, s.UserID))
		return
	}
	res := b.accept(st, s, in, text)
	switch res.status {
	case invalid:
		b.ask(ctx, f, s, res.hint)
		return
	case photoAdded:
		b.save(ctx, s)
		msg := fmt.Sprintf("Фото получено: %d из %d. Пришлите ещё или нажмите «Готово».", len(s.Photos), maxPhotos)
		if len(s.Photos) >= maxPhotos {
			msg = "Получил 10 фото — это максимум. Нажмите «Готово»."
		}
		b.reply(ctx, s.UserID, msg, b.stepKeyboard(f, s, st))
		return
	case refused:
		b.dropSession(ctx, s.UserID)
		b.sendMenu(ctx, s.UserID, "Понимаю. Без согласия мы не можем сохранить заявку. Если передумаете — выберите нужный пункт в меню или просто напишите сюда вопрос.")
		return
	}

	s.Answers[st.key] = res.value
	for k, v := range res.extra {
		s.Answers[k] = v
	}
	s.Step = b.nextStep(f, s, s.Step+1)
	if s.Step >= len(f.steps) {
		b.finish(ctx, f, s)
		return
	}
	b.save(ctx, s)
	b.ask(ctx, f, s, "")
}

func (b *Bot) finish(ctx context.Context, f *flow, s *storage.Session) {
	if f.leadKind == "" {
		b.dropSession(ctx, s.UserID)
		b.sendMenu(ctx, s.UserID, "Готово!")
		return
	}
	b.finishLead(ctx, f, s)
}

func yearsWord(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 14:
		return "лет"
	case n%10 == 1:
		return "год"
	case n%10 >= 2 && n%10 <= 4:
		return "года"
	}
	return "лет"
}

// displayValue — значение ответа для заявки.
func displayValue(st step, raw string) string {
	switch st.kind {
	case kindMoney, kindDown:
		v, err := strconv.ParseFloat(raw, 64)
		if err == nil {
			return FormatRub(v)
		}
	case kindYears:
		if n, err := strconv.Atoi(raw); err == nil {
			return fmt.Sprintf("%d %s", n, yearsWord(n))
		}
	case kindRate, kindNumber:
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			if st.kind == kindRate {
				return FormatNumber(v) + "%"
			}
			return FormatNumber(v)
		}
	}
	return raw
}

func (b *Bot) leadAnswers(f *flow, a map[string]string) []storage.Answer {
	src := f
	if f.answerFlow != "" {
		src = b.flows[f.answerFlow]
	}
	var out []storage.Answer
	for _, st := range src.steps {
		switch st.kind {
		case kindConsent, kindPhone, kindPhotos:
			continue
		}
		v := a[st.key]
		if v == "" || (st.kind == kindRateChoice && v == btnOwnRate) {
			continue
		}
		out = append(out, storage.Answer{Key: st.key, Label: st.label, Value: displayValue(st, v)})
	}
	return out
}

// estimate — предварительная вилка цены для продавца по цене м².
func (b *Bot) estimate(a map[string]string) (string, bool) {
	if a["object"] == "Участок" {
		return "", false
	}
	area, err := strconv.ParseFloat(a["area"], 64)
	if err != nil || area <= 0 {
		return "", false
	}
	per, place, ok := b.pricePerM2(a["district"], a["city"])
	if !ok {
		return "", false
	}
	mid := area * per
	low := math.Round(mid*0.9/10000) * 10000
	high := math.Round(mid*1.1/10000) * 10000
	return fmt.Sprintf("%s – %s (по средней цене %s/м², %s)",
		FormatRub(low), FormatRub(high), FormatRub(per), place), true
}

func (b *Bot) finishLead(ctx context.Context, f *flow, s *storage.Session) {
	name, err := b.send.UserName(ctx, s.UserID)
	if err != nil || name == "" {
		name = fmt.Sprintf("id%d", s.UserID)
	}
	consentAt, _ := time.Parse(time.RFC3339, s.Answers["consent"])
	lead := &storage.Lead{
		Kind:      f.leadKind,
		UserID:    s.UserID,
		Name:      name,
		Phone:     s.Answers["phone"],
		Answers:   b.leadAnswers(f, s.Answers),
		Photos:    s.Photos,
		ConsentAt: consentAt,
	}
	id, err := b.store.SaveLead(ctx, lead)
	if err != nil {
		b.log.Error("save lead", "user", s.UserID, "err", err)
	}
	lead.ID = id

	est, hasEst := "", false
	if f.leadKind == flowSell {
		est, hasEst = b.estimate(s.Answers)
	}
	b.notifyAdmin(ctx, b.leadMessage(lead, s.Answers, est))
	b.dropSession(ctx, s.UserID)

	var thanks string
	switch f.leadKind {
	case flowBuy:
		thanks = "Спасибо! Заявка принята. Олег свяжется с вами в ближайшее время и пришлёт подходящие варианты."
	case flowSell:
		thanks = "Спасибо! Заявка принята. Олег свяжется с вами, чтобы обсудить продажу."
		if hasEst {
			thanks += "\n\n📊 Предварительная оценка: " + est + ".\nТочную цену Олег назовёт после осмотра."
		}
	case flowMortgage:
		thanks = "Спасибо! Олег свяжется с вами и проконсультирует по ипотеке."
	default:
		thanks = "Спасибо! Олег свяжется с вами в ближайшее время."
	}
	b.sendMenu(ctx, s.UserID, thanks)
}

func (b *Bot) leadMessage(l *storage.Lead, a map[string]string, est string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔥 Новая заявка #%d · %s\n", l.ID, leadKindTitle[l.Kind])
	fmt.Fprintf(&sb, "👤 %s — vk.com/id%d\n", l.Name, l.UserID)
	fmt.Fprintf(&sb, "📞 %s\n\n", l.Phone)
	for _, ans := range l.Answers {
		fmt.Fprintf(&sb, "%s: %s\n", ans.Label, ans.Value)
	}
	if l.Kind == flowSell && a["after"] == "Новостройка" {
		sb.WriteString("\n⭐ Двойная сделка: после продажи покупает новостройку\n")
	}
	if est != "" {
		fmt.Fprintf(&sb, "\n📊 Оценка бота: %s\n", est)
	}
	if len(l.Photos) > 0 {
		fmt.Fprintf(&sb, "\n📷 Фото: %d\n", len(l.Photos))
		for _, p := range l.Photos {
			sb.WriteString(p + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
