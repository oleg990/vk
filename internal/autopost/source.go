package autopost

import (
	"context"
	"fmt"
	"strings"
)

// Режим «Добавить источник»: Олег присылает ссылки на застройщиков, новости или
// любую другую информацию — бот копит сообщения и одним пожеланием передаёт Claude,
// который сам разберёт материал, обновит content/sources.json и сделает посты.

func doneWord(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "готово", "хватит", "все", "всё", "закончил", "закончила", "стоп":
		return true
	}
	return false
}

func cancelWord(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "отмена", "отменить", "cancel":
		return true
	}
	return false
}

// sourceWaiting — true, если сейчас ждём от Олега ссылки/материалы.
func (m *Manager) sourceWaiting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.awaitSource
}

// startSource включает режим ожидания материалов.
func (m *Manager) startSource() string {
	m.mu.Lock()
	m.awaitSource = true
	m.sourceNotes = nil
	m.mu.Unlock()
	return "📎 Пришлите ссылку на застройщика, новость, статью или просто текст — можно несколько сообщений подряд (и фото, если есть).\n\n" +
		"Когда закончите — напишите «готово». Передам Claude, он сам разберёт и обновит базу.\n\nЧтобы отменить — «отмена»."
}

// addSourceNote добавляет одно присланное сообщение в копилку.
func (m *Manager) addSourceNote(text string) string {
	text = strings.TrimSpace(text)
	m.mu.Lock()
	m.sourceNotes = append(m.sourceNotes, text)
	n := len(m.sourceNotes)
	m.mu.Unlock()
	return fmt.Sprintf("✅ Добавлено (%d). Жду ещё или напишите «готово».", n)
}

// finishSource завершает режим: cancel — просто выключить, иначе отдать накопленное Claude.
func (m *Manager) finishSource(ctx context.Context, cancel bool) string {
	m.mu.Lock()
	notes := m.sourceNotes
	m.awaitSource = false
	m.sourceNotes = nil
	m.mu.Unlock()
	if cancel {
		return "Отменено."
	}
	if len(notes) == 0 {
		return "Ничего не прислали — режим выключен."
	}
	if m.Writer == nil {
		return "⚠️ Запуск Claude не настроен: на сервере нет ROUTINE_ID и ROUTINE_TOKEN, обновить базу автоматически не получится."
	}
	wish := "Олег прислал новый материал — ссылку или информацию о застройщике, новость рынка или тему для статьи:\n\n" +
		strings.Join(notes, "\n") +
		"\n\nЕсли это застройщик — зайди на сайт, собери название, город, url (и materials, если есть папка с рендерами/прайсами)," +
		" и добавь или обнови запись в content/sources.json (не дублируй, если такой застройщик уже есть)." +
		" Затем сделай по этому материалу 1–2 поста." +
		" Если это новость рынка, статья или другая тема — используй как основу для поста (новость или полезная статья), фактов не выдумывай, сверяй с источником."
	if _, err := m.Writer.Fire(ctx, wish); err != nil {
		m.Log.Error("autopost: добавление источника", "err", err)
		return "⚠️ Не получилось запустить обработку: " + err.Error()
	}
	return fmt.Sprintf("🛠 Принял %s — разбираю и обновляю базу. Посты пришлю сюда, как будут готовы (15–30 минут).",
		plural(len(notes), "материал", "материала", "материалов"))
}
