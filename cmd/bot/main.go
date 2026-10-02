// Бот сообщества VK «Олег Маханько | Недвижимость».
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"realty-bot/internal/autopost"
	"realty-bot/internal/bot"
	"realty-bot/internal/config"
	"realty-bot/internal/routine"
	"realty-bot/internal/stats"
	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

// Стартовая цена за м² для оценки продавцам: средняя по внешнему прайсу
// агентства в Старом Осколе на 01.10.2026. Меняется командой /цена.
var defaultPrices = map[string]float64{"Старый Оскол": 114700}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("bot stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.OpenFile(cfg.DataFile)
	if err != nil {
		return err
	}
	client := vk.New(cfg.VKToken, cfg.VKGroupID, cfg.VKAPIURL, log)
	b, err := bot.New(ctx, client, store, bot.Options{
		AdminID: cfg.AdminVKID, PrivacyURL: cfg.PrivacyURL, CallPhone: cfg.CallPhone, Location: loc, Log: log,
	})
	if err != nil {
		return err
	}
	if err := b.SeedPrices(ctx, defaultPrices); err != nil {
		return err
	}

	ap := &autopost.Manager{
		GroupID: cfg.VKGroupID, AdminID: cfg.AdminVKID, Msg: client,
		Content:   autopost.Content{BaseURL: cfg.ContentURL, Extra: cfg.ContentExtra, HTTP: &http.Client{Timeout: 30 * time.Second}},
		UploadGap: 3 * time.Second,
		Photos: autopost.Stocks{
			autopost.Pixabay{URL: "https://pixabay.com", Key: cfg.PixabayKey, HTTP: &http.Client{Timeout: 60 * time.Second}},
			autopost.Pexels{URL: "https://api.pexels.com", Key: cfg.PexelsKey, HTTP: &http.Client{Timeout: 60 * time.Second}},
			autopost.Openverse{URL: "https://api.openverse.org", HTTP: &http.Client{Timeout: 60 * time.Second}}, // без ключа
		},
		Gen: autopost.Chain{
			// бесплатный Kandinsky первым, FLUX через Hugging Face — запасной
			autopost.Kandinsky{URL: cfg.FBURL, Key: cfg.FBKey, Secret: cfg.FBSecret, HTTP: &http.Client{Timeout: 60 * time.Second}},
			autopost.HF{URL: cfg.HFRouterURL, Providers: cfg.HFProviders, Token: cfg.HFToken, HTTP: &http.Client{Timeout: 120 * time.Second}},
		},
		Store: store, DataDir: filepath.Dir(cfg.DataFile), Loc: loc, Log: log,
		HTTP: &http.Client{Timeout: 60 * time.Second},
	}
	// просмотры и охват видны только с ключом администратора
	var statSrc stats.Source = client
	if cfg.VKUserToken != "" {
		statSrc = client.WithToken(cfg.VKUserToken)
	}
	rep := &stats.Reporter{Src: statSrc, Msg: client, Store: store, GroupID: cfg.VKGroupID, AdminID: cfg.AdminVKID, Loc: loc, Log: log}
	if cfg.VKUserToken != "" {
		ap.Wall = client.WithToken(cfg.VKUserToken)
	} else {
		log.Warn("VK_USER_TOKEN не задан: превью постов придут, но публикация будет недоступна")
	}
	if rc := (routine.Client{URL: "https://api.anthropic.com", ID: cfg.RoutineID, Token: cfg.RoutineToken,
		HTTP: &http.Client{Timeout: 30 * time.Second}}); rc.Enabled() {
		ap.Writer = stats.Writer{Inner: rc, R: rep}
	}
	if cfg.VKUserToken != "" {
		go rep.Run(ctx) // недельный разбор по понедельникам
	}
	go ap.Run(ctx, 5*time.Minute)

	return client.Listen(ctx, func(u vk.Update) {
		if u.Type != "message_new" {
			return
		}
		var m vk.MessageNew
		if err := json.Unmarshal(u.Object, &m); err != nil {
			log.Warn("bad message_new", "err", err)
			return
		}
		msg := m.Message
		if msg.PeerID != msg.FromID || msg.FromID <= 0 { // только личные сообщения от людей
			return
		}
		if msg.FromID == cfg.AdminVKID && isAnalytics(msg.Text, msg.PayloadMap()) {
			if err := client.Send(ctx, msg.FromID, rep.Report(ctx), bot.AdminKeyboard()); err != nil {
				log.Error("send analytics", "err", err)
			}
			return
		}
		if ap.HandleAdmin(ctx, msg.FromID, msg.Text, msg.PayloadMap(), msg.PhotoURLs()...) {
			return
		}
		b.Handle(ctx, bot.Incoming{
			UserID: msg.FromID, Text: msg.Text,
			Payload: msg.PayloadMap(), Photos: msg.PhotoURLs(),
		})
	})
}

// isAnalytics — кнопка «📈 Аналитика» или команда /аналитика.
func isAnalytics(text string, payload map[string]string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return payload["cmd"] == bot.AdmAnalytics || t == "/аналитика" || t == "аналитика"
}
