// Бот сообщества VK «Олег Маханько | Недвижимость».
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"realty-bot/internal/bot"
	"realty-bot/internal/config"
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
		AdminID: cfg.AdminVKID, PrivacyURL: cfg.PrivacyURL, Location: loc, Log: log,
	})
	if err != nil {
		return err
	}
	if err := b.SeedPrices(ctx, defaultPrices); err != nil {
		return err
	}

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
		b.Handle(ctx, bot.Incoming{
			UserID: msg.FromID, Text: msg.Text,
			Payload: msg.PayloadMap(), Photos: msg.PhotoURLs(),
		})
	})
}
