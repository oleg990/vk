// Package config читает настройки из переменных окружения (см. .env.example).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	VKToken    string // ключ доступа сообщества (сообщения + управление)
	VKGroupID  int64  // id группы: 241936618
	AdminVKID  int64  // VK id Олега — ему приходят заявки
	DataFile   string // файл хранилища
	PrivacyURL string // ссылка на политику обработки ПДн
	CallPhone  string // номер для кнопки «Позвонить Олегу»

	// Автопостинг
	VKUserToken  string   // пользовательский ключ (wall, photos, offline) — публикация на стене
	HFToken      string   // токен Hugging Face для генерации фото
	HFRouterURL  string   // https://router.huggingface.co
	HFProviders  []string // провайдеры FLUX по порядку: fal-ai, nscale
	FBKey        string   // Fusion Brain (Kandinsky): ключ
	FBSecret     string   // Fusion Brain: секрет
	FBURL        string
	PexelsKey    string   // фотосток Pexels (бесплатный ключ)
	PixabayKey   string   // фотосток Pixabay (бесплатный ключ)
	ContentExtra []string // доп. ветки с очередью (claude/posts)
	RoutineID    string   // задача Claude «сделай пост» (claude.ai/code/routines)
	RoutineToken string   // её API-токен
	ContentURL   string   // откуда брать очередь постов
	VKAPIURL     string   // по умолчанию https://api.vk.com/method/
	Timezone     string
}

func Load() (Config, error) {
	c := Config{
		VKToken:    strings.TrimSpace(os.Getenv("VK_TOKEN")),
		DataFile:   envOr("DATA_FILE", "data/bot.json"),
		PrivacyURL: os.Getenv("PRIVACY_URL"),
		CallPhone:  envOr("CALL_PHONE", "+79205952888"),

		VKUserToken:  strings.TrimSpace(os.Getenv("VK_USER_TOKEN")),
		HFToken:      strings.TrimSpace(os.Getenv("HF_TOKEN")),
		HFRouterURL:  envOr("HF_ROUTER_URL", "https://router.huggingface.co"),
		HFProviders:  strings.Split(envOr("HF_PROVIDERS", "fal-ai,nscale"), ","),
		FBKey:        strings.TrimSpace(os.Getenv("FB_KEY")),
		FBSecret:     strings.TrimSpace(os.Getenv("FB_SECRET")),
		FBURL:        envOr("FB_URL", "https://api-key.fusionbrain.ai"),
		PexelsKey:    strings.TrimSpace(os.Getenv("PEXELS_KEY")),
		PixabayKey:   strings.TrimSpace(os.Getenv("PIXABAY_KEY")),
		ContentExtra: strings.Split(envOr("CONTENT_EXTRA_URLS", "https://raw.githubusercontent.com/oleg990/vk/claude/posts/content/"), ","),
		RoutineID:    strings.TrimSpace(os.Getenv("ROUTINE_ID")),
		RoutineToken: strings.TrimSpace(os.Getenv("ROUTINE_TOKEN")),
		ContentURL:   envOr("CONTENT_URL", "https://raw.githubusercontent.com/oleg990/vk/main/content/"),
		VKAPIURL:     os.Getenv("VK_API_URL"),
		Timezone:     envOr("TZ_NAME", "Europe/Moscow"),
	}
	var errs []error
	if c.VKToken == "" {
		errs = append(errs, errors.New("VK_TOKEN не задан"))
	}
	var err error
	if c.VKGroupID, err = envInt("VK_GROUP_ID"); err != nil {
		errs = append(errs, err)
	}
	if c.AdminVKID, err = envInt("ADMIN_VK_ID"); err != nil {
		errs = append(errs, err)
	}
	return c, errors.Join(errs...)
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, fmt.Errorf("%s не задан", key)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s должен быть положительным числом", key)
	}
	return n, nil
}
