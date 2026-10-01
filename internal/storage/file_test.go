package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStorePersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "bot.json")
	s, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, &Session{UserID: 7, Flow: "buy", Step: 2, Answers: map[string]string{"city": "Воронеж"}}); err != nil {
		t.Fatal(err)
	}
	id, err := s.SaveLead(ctx, &Lead{Kind: "sell", UserID: 7, Phone: "+79001234567"})
	if err != nil || id != 1 {
		t.Fatalf("SaveLead = %d, %v", id, err)
	}
	if err := s.SetSetting(ctx, "rates", json.RawMessage(`{"Семейная":6}`)); err != nil {
		t.Fatal(err)
	}

	// Перезапуск: всё на месте.
	s2, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s2.GetSession(ctx, 7)
	if sess == nil || sess.Answers["city"] != "Воронеж" || sess.Step != 2 {
		t.Fatalf("session not restored: %+v", sess)
	}
	leads, _ := s2.ListLeads(ctx, "sell", time.Now().Add(-time.Hour))
	if len(leads) != 1 || leads[0].Phone != "+79001234567" {
		t.Fatalf("leads = %+v", leads)
	}
	if id, _ := s2.SaveLead(ctx, &Lead{Kind: "buy"}); id != 2 {
		t.Fatalf("next id = %d, want 2", id)
	}
	raw, ok, _ := s2.GetSetting(ctx, "rates")
	var rates map[string]float64
	if !ok || json.Unmarshal(raw, &rates) != nil || rates["Семейная"] != 6 {
		t.Fatalf("setting = %s %v", raw, ok)
	}
}

func TestSessionIsCopied(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	sess := &Session{UserID: 1, Answers: map[string]string{"a": "1"}}
	_ = s.SaveSession(ctx, sess)
	sess.Answers["a"] = "changed"
	got, _ := s.GetSession(ctx, 1)
	if got.Answers["a"] != "1" {
		t.Fatal("store must not share maps with caller")
	}
	_ = s.DeleteSession(ctx, 1)
	if got, _ := s.GetSession(ctx, 1); got != nil {
		t.Fatal("session not deleted")
	}
}

func TestListLeadsFilter(t *testing.T) {
	ctx := context.Background()
	s := NewMemory()
	old := time.Now().Add(-48 * time.Hour)
	_, _ = s.SaveLead(ctx, &Lead{Kind: "buy", CreatedAt: old})
	_, _ = s.SaveLead(ctx, &Lead{Kind: "buy"})
	_, _ = s.SaveLead(ctx, &Lead{Kind: "sell"})
	all, _ := s.ListLeads(ctx, "", time.Now().Add(-24*time.Hour))
	if len(all) != 2 || all[0].ID != 3 {
		t.Fatalf("all = %+v", all)
	}
	buy, _ := s.ListLeads(ctx, "buy", time.Time{})
	if len(buy) != 2 {
		t.Fatalf("buy = %d", len(buy))
	}
}
