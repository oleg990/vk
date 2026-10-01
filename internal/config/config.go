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
	VKAPIURL   string // по умолчанию https://api.vk.com/method/
	Timezone   string
}

func Load() (Config, error) {
	c := Config{
		VKToken:    strings.TrimSpace(os.Getenv("VK_TOKEN")),
		DataFile:   envOr("DATA_FILE", "data/bot.json"),
		PrivacyURL: os.Getenv("PRIVACY_URL"),
		VKAPIURL:   os.Getenv("VK_API_URL"),
		Timezone:   envOr("TZ_NAME", "Europe/Moscow"),
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
