package stats

import (
	"context"
	"strings"
	"testing"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

var msk = time.FixedZone("MSK", 3*3600)

func at(day, hour int) int64 { return time.Date(2026, 10, day, hour, 0, 0, 0, msk).Unix() }

func samplePosts() []vk.WallItem {
	return []vk.WallItem{
		{ID: 1, Date: at(1, 19), Text: "Никитинские сады: цены\n#новостройки #Воронеж", Views: 100, Likes: 2},
		{ID: 2, Date: at(2, 19), Text: "ЖК А101 акции\n#новостройки", Views: 120, Likes: 3},
		{ID: 3, Date: at(3, 9), Text: "Что важнее: район или метраж?", Views: 400, Likes: 20, Comments: 15},
		{ID: 4, Date: at(4, 19), Text: "Вопрос клиента: можно ли торговаться?", Views: 380, Likes: 18, Comments: 5, Reposts: 4},
		{ID: 5, Date: at(5, 19), Text: "Ещё один пост про застройщика #новостройки", Views: 90, Likes: 1},
		{ID: 6, Date: at(6, 19), Text: "Ипотека: что со ставками", Views: 150, Likes: 5},
		{ID: 7, Date: at(7, 19), Text: "Совсем новый, просмотров ещё нет", Views: 0},
	}
}

// bigSample — три «недели» одного и того же набора постов: достаточно для выводов о темах и времени.
func bigSample() []vk.WallItem {
	var out []vk.WallItem
	for k := int64(0); k < 3; k++ {
		for _, p := range samplePosts() {
			p.ID += 10 * k
			p.Date -= 7 * 24 * 3600 * k
			out = append(out, p)
		}
	}
	return out
}

func TestAnalyze(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, msk)
	a := Analyze(bigSample(), now, msk, 30)
	if len(a.Posts) != 18 {
		t.Fatalf("posts with views = %d, want 18", len(a.Posts))
	}
	if a.Top[0].Views != 400 {
		t.Fatalf("top post views = %d, want 400", a.Top[0].Views)
	}
	if got := TopicOf(samplePosts()[2].Text); got != "Опросы" {
		t.Fatalf("topic = %q", got)
	}
	if a.Topics[0].Name != "Опросы" && a.Topics[0].Name != "Вопрос–ответ" {
		t.Fatalf("best topic = %q, topics %+v", a.Topics[0].Name, a.Topics)
	}
	var dev Topic
	for _, tp := range a.Topics {
		if tp.Name == "Новостройки и застройщики" {
			dev = tp
		}
	}
	if dev.Posts != 9 || dev.AvgViews > 110 {
		t.Fatalf("developer topic = %+v", dev)
	}
	notes := strings.Join(a.Notes, "\n")
	if !strings.Contains(notes, "слабее") || !strings.Contains(notes, "Новостройки и застройщики") {
		t.Fatalf("weak-topic note missing:\n%s", notes)
	}
	if a.BestSlot == "" || a.BestDay == "" {
		t.Fatal("best day and slot expected with 18 posts")
	}
}

func TestSixPostsGiveNoTrends(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, msk)
	a := Analyze(samplePosts(), now, msk, 30)
	if len(a.Posts) != 6 || a.BestDay != "" || a.BestSlot != "" {
		t.Fatalf("no timing advice with 6 posts: %+v", a)
	}
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "мало") {
		t.Fatalf("only the 'too little data' note expected, got %q", a.Notes)
	}
	if firstLine("  ", 40) != "(без текста)" {
		t.Fatal("empty post text must be labelled")
	}
}

func TestAnalyzeFewPosts(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, msk)
	a := Analyze(samplePosts()[:2], now, msk, 30)
	if a.BestDay != "" || !strings.Contains(strings.Join(a.Notes, " "), "мало") {
		t.Fatalf("expected a preliminary-data note, got %+v", a)
	}
	empty := Analyze(nil, now, msk, 30)
	if !strings.Contains(Format(Data{Analysis: empty}), "пока нет") || Brief(Data{Analysis: empty}) != "" {
		t.Fatal("empty analysis must be handled")
	}
}

func TestFormatAndBrief(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, msk)
	d := Data{GroupID: 7, Analysis: Analyze(samplePosts(), now, msk, 30),
		Cur: &vk.StatPeriod{Reach: 200, Visitors: 80, Subscribed: 5, Unsubscribed: 1}, Prev: &vk.StatPeriod{Reach: 100}, Leads: 3, PrevLds: 2}
	out := Format(d)
	for _, want := range []string{"Охват за 7 дней: 200 (+100%", "Заявок за 7 дней: 3 (+50%", "vk.com/wall-7_3", "По темам"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}
	if b := Brief(d); !strings.Contains(b, "Темы по средним просмотрам") || len(b) > 1500 {
		t.Fatalf("brief = %q", b)
	}
}

type fakeSrc struct {
	posts []vk.WallItem
	err   error
}

func (f fakeSrc) WallPosts(context.Context, int64, int) ([]vk.WallItem, error) { return f.posts, f.err }
func (f fakeSrc) GroupStats(context.Context, int64, time.Time, time.Time) (vk.StatPeriod, error) {
	return vk.StatPeriod{}, &vk.APIError{Code: 15, Msg: "access denied"}
}

type fakeMsg struct{ texts []string }

func (f *fakeMsg) Send(_ context.Context, _ int64, text string, _ *vk.Keyboard) error {
	f.texts = append(f.texts, text)
	return nil
}

type fakeInner struct{ got string }

func (f *fakeInner) Fire(_ context.Context, wish string) (string, error) {
	f.got = wish
	return "ok", nil
}

func newReporter(now time.Time, src Source) (*Reporter, *fakeMsg) {
	m := &fakeMsg{}
	return &Reporter{Src: src, Msg: m, Store: storage.NewMemory(), GroupID: 7, AdminID: 1, Loc: msk, Now: func() time.Time { return now }}, m
}

func TestTickSendsOncePerWeek(t *testing.T) {
	ctx := context.Background()
	mon := time.Date(2026, 10, 12, 10, 5, 0, 0, msk) // понедельник
	r, m := newReporter(mon, fakeSrc{posts: samplePosts()})
	r.Tick(ctx)
	r.Tick(ctx)
	if len(m.texts) != 1 || !strings.Contains(m.texts[0], "Недельный разбор") {
		t.Fatalf("messages = %q", m.texts)
	}
	early, m2 := newReporter(time.Date(2026, 10, 12, 9, 0, 0, 0, msk), fakeSrc{})
	early.Tick(ctx)
	tue, m3 := newReporter(time.Date(2026, 10, 13, 12, 0, 0, 0, msk), fakeSrc{})
	tue.Tick(ctx)
	if len(m2.texts)+len(m3.texts) != 0 {
		t.Fatal("report only on Monday after 10:00")
	}
}

func TestWriterAddsBriefAndSurvivesErrors(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, msk)
	r, _ := newReporter(now, fakeSrc{posts: samplePosts()})
	in := &fakeInner{}
	if _, err := (Writer{Inner: in, R: r}).Fire(ctx, "про ДСК"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(in.got, "про ДСК\n\n"+Marker+"\n") {
		t.Fatalf("wish = %q", in.got)
	}
	bad, _ := newReporter(now, fakeSrc{err: &vk.APIError{Code: 5, Msg: "bad token"}})
	in2 := &fakeInner{}
	if _, err := (Writer{Inner: in2, R: bad}).Fire(ctx, "x"); err != nil || in2.got != "x" {
		t.Fatalf("stats failure must not block posts: %q %v", in2.got, err)
	}
}
