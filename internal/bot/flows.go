package bot

// Сценарии бота. Каждый сценарий — список шагов; движок в engine.go
// задаёт вопросы по очереди, проверяет ответы и собирает заявку.

type stepKind int

const (
	kindChoice     stepKind = iota // кнопки (freeText — можно и написать своё)
	kindText                       // свободный текст
	kindNumber                     // положительное число (площадь)
	kindMoney                      // сумма в рублях
	kindDown                       // первоначальный взнос (% или сумма)
	kindYears                      // срок, лет
	kindRateChoice                 // выбор программы из настроенных ставок
	kindRate                       // своя ставка, %
	kindPhotos                     // фото, до maxPhotos
	kindConsent                    // согласие на обработку данных
	kindPhone                      // телефон
)

type step struct {
	key      string
	label    string // подпись в заявке
	question string
	kind     stepKind
	options  []string
	freeText bool
	optional bool                                   // кнопка «Пропустить»
	when     func(a map[string]string, b *Bot) bool // nil — шаг всегда
}

type flow struct {
	name     string
	leadKind string // пусто — заявка не создаётся (калькулятор)
	// answerFlow — из какого сценария брать подписи ответов для заявки (по умолчанию свой).
	answerFlow string
	steps      []step
}

const (
	flowBuy          = "buy"
	flowSell         = "sell"
	flowMortgage     = "mortgage"
	flowMortgageRes  = "mortgage_result" // показан расчёт, ждём «Хочу подбор»
	flowMortgageLead = "mortgage_lead"
	flowContact      = "contact"

	maxPhotos = 10
)

// Подписи кнопок главного меню и навигации.
const (
	btnBuy      = "🏠 Подобрать квартиру"
	btnSell     = "💰 Продать недвижимость"
	btnMortgage = "🧮 Рассчитать ипотеку"
	btnContact  = "📞 Связаться с Олегом"

	btnBack     = "⬅️ Назад"
	btnMenu     = "❌ В меню"
	btnSkip     = "Пропустить"
	btnDone     = "✅ Готово"
	btnAgree    = "✅ Согласен"
	btnRefuse   = "Отказаться"
	btnOwnRate  = "Своя ставка"
	btnWantPick = "🏠 Хочу подбор под этот платёж"
)

var leadKindTitle = map[string]string{
	flowBuy:      "ПОКУПАТЕЛЬ",
	flowSell:     "ПРОДАВЕЦ",
	flowMortgage: "ИПОТЕКА",
	flowContact:  "ОБРАТНЫЙ ЗВОНОК",
}

var (
	cityOptions = []string{"Старый Оскол", "Воронеж"}
	roomOptions = []string{"Студия", "1", "2", "3", "4 и больше"}
)

func isType(key string, values ...string) func(map[string]string, *Bot) bool {
	return func(a map[string]string, _ *Bot) bool {
		for _, v := range values {
			if a[key] == v {
				return true
			}
		}
		return false
	}
}

func not(f func(map[string]string, *Bot) bool) func(map[string]string, *Bot) bool {
	return func(a map[string]string, b *Bot) bool { return !f(a, b) }
}

func contactSteps() []step {
	return []step{
		{key: "consent", kind: kindConsent,
			question: "Чтобы Олег мог с вами связаться, нужно ваше согласие на обработку персональных данных (имя, телефон и ответы выше)."},
		{key: "phone", kind: kindPhone,
			question: "Напишите номер телефона для связи — например, +7 900 123-45-67."},
	}
}

func buildFlows() map[string]*flow {
	buy := &flow{name: flowBuy, leadKind: flowBuy, steps: append([]step{
		{key: "city", label: "Город", question: "В каком городе ищете квартиру? Выберите или напишите свой.",
			kind: kindChoice, options: cityOptions, freeText: true},
		{key: "market", label: "Рынок", question: "Что рассматриваете?",
			kind: kindChoice, options: []string{"Новостройка", "Вторичка", "Не важно"}},
		{key: "rooms", label: "Комнат", question: "Сколько комнат?",
			kind: kindChoice, options: roomOptions},
		{key: "budget", label: "Бюджет", question: "Какой бюджет? Выберите или напишите сумму.",
			kind: kindChoice, freeText: true,
			options: []string{"до 3 млн", "3–5 млн", "5–7 млн", "7–10 млн", "больше 10 млн"}},
		{key: "payment", label: "Оплата", question: "Как планируете оплачивать?",
			kind: kindChoice, options: []string{"Наличные", "Ипотека", "Ипотека уже одобрена", "Маткапитал / сертификат", "Продаю свою квартиру"}},
		{key: "timing", label: "Сроки", question: "Когда планируете покупку?",
			kind: kindChoice, options: []string{"В ближайший месяц", "В течение 1–3 месяцев", "Пока присматриваюсь"}},
		{key: "wishes", label: "Пожелания", question: "Есть пожелания — район, этаж, ремонт? Напишите или нажмите «Пропустить».",
			kind: kindText, optional: true},
	}, contactSteps()...)}

	isPlot := isType("object", "Участок")
	sell := &flow{name: flowSell, leadKind: flowSell, steps: append([]step{
		{key: "object", label: "Объект", question: "Что хотите продать?",
			kind: kindChoice, options: []string{"Квартира", "Дом", "Участок", "Комната", "Коммерция"}},
		{key: "city", label: "Город", question: "В каком городе? Выберите или напишите свой.",
			kind: kindChoice, options: cityOptions, freeText: true},
		{key: "district", label: "Район", question: "Район или микрорайон?", kind: kindText},
		{key: "address", label: "Адрес", question: "Адрес — улица и дом? Можно пропустить.",
			kind: kindText, optional: true},
		{key: "rooms", label: "Комнат", question: "Сколько комнат?",
			kind: kindChoice, options: roomOptions, when: isType("object", "Квартира")},
		{key: "area", label: "Площадь, м²", question: "Общая площадь в м²? Например: 44.",
			kind: kindNumber, when: not(isPlot)},
		{key: "plot", label: "Участок, соток", question: "Площадь участка в сотках? Например: 8.",
			kind: kindNumber, when: isPlot},
		{key: "floor", label: "Этаж", question: "Этаж и этажность дома — например, 4/9.",
			kind: kindText, optional: true, when: isType("object", "Квартира", "Комната")},
		{key: "condition", label: "Состояние", question: "Какое состояние?",
			kind: kindChoice, options: []string{"Требует ремонта", "Косметический ремонт", "Хороший ремонт", "Дизайнерский ремонт"},
			when: not(isPlot)},
		{key: "price", label: "Желаемая цена", question: "За сколько хотите продать? Если не знаете — нажмите «Пропустить», Олег оценит.",
			kind: kindMoney, optional: true},
		{key: "after", label: "После продажи", question: "Что планируете после продажи?",
			kind: kindChoice, options: []string{"Покупка в этом городе", "Новостройка", "Переезд в другой город", "Просто продаю"}},
		{key: "timing", label: "Сроки", question: "Когда нужно продать?",
			kind: kindChoice, options: []string{"Срочно, до месяца", "В течение 1–3 месяцев", "Не спешу"}},
		{key: "photos", label: "Фото", question: "Пришлите фото объекта (до 10) — так оценка будет точнее. Когда закончите, нажмите «Готово», или «Пропустить».",
			kind: kindPhotos, optional: true},
	}, contactSteps()...)}

	hasRates := func(_ map[string]string, b *Bot) bool { return len(b.rateList()) > 0 }
	mortgage := &flow{name: flowMortgage, steps: []step{
		{key: "price", label: "Стоимость", question: "Сколько стоит квартира? Например: 5 500 000 или 5,5 млн.", kind: kindMoney},
		{key: "down", label: "Первоначальный взнос", question: "Первоначальный взнос? В процентах (20%) или суммой (1,2 млн). Без взноса — 0.", kind: kindDown},
		{key: "years", label: "Срок", question: "На какой срок?", kind: kindYears,
			options: []string{"10 лет", "15 лет", "20 лет", "25 лет", "30 лет"}},
		{key: "program", label: "Программа", question: "Выберите программу:", kind: kindRateChoice, when: hasRates},
		{key: "rate", label: "Ставка, %", question: "Какая ставка, % годовых? Например: 12,5.", kind: kindRate,
			when: func(a map[string]string, b *Bot) bool { return !hasRates(a, b) || a["program"] == btnOwnRate }},
	}}

	mortgageLead := &flow{name: flowMortgageLead, leadKind: flowMortgage, answerFlow: flowMortgage, steps: contactSteps()}

	contact := &flow{name: flowContact, leadKind: flowContact, steps: append([]step{
		{key: "question", label: "Вопрос", question: "Коротко опишите вопрос — или нажмите «Пропустить».", kind: kindText, optional: true},
	}, contactSteps()...)}

	return map[string]*flow{
		flowBuy: buy, flowSell: sell, flowMortgage: mortgage,
		flowMortgageLead: mortgageLead, flowContact: contact,
	}
}
