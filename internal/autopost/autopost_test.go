package autopost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"realty-bot/internal/storage"
	"realty-bot/internal/vk"
)

func pngOf(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

type sentMsg struct {
	text, att string
	kb        *vk.Keyboard
}

type fakeMsg struct {
	sent       []sentMsg
	uploads    int
	failUpload bool // как ключ сообщества без права «фото»
}

func (f *fakeMsg) UploadMessagePhoto(context.Context, int64, []byte) (string, error) {
	if f.failUpload {
		return "", errors.New("vk api error 15: Access denied")
	}
	f.uploads++
	return "photo1_100", nil
}
func (f *fakeMsg) SendAttachment(_ context.Context, _ int64, text, att string, kb *vk.Keyboard) error {
	f.sent = append(f.sent, sentMsg{text, att, kb})
	return nil
}
func (f *fakeMsg) last() sentMsg { return f.sent[len(f.sent)-1] }

// previews — сколько превью на одобрение отправлено.
func (f *fakeMsg) previews() int {
	n := 0
	for _, m := range f.sent {
		if strings.Contains(m.text, "на одобрение") {
			n++
		}
	}
	return n
}

type fakeWall struct {
	posts       int
	publishDate int64
	message     string
	att         string
	uploads     int
}

func (f *fakeWall) UploadWallPhoto(context.Context, int64, []byte) (string, error) {
	f.uploads++
	return fmt.Sprintf("photo-1_%d", 200+f.uploads), nil
}
func (f *fakeWall) WallPost(_ context.Context, _ int64, msg, att string, pd int64) (int64, error) {
	f.posts++
	f.publishDate = pd
	f.message = msg
	f.att = att
	return 42, nil
}

type fakeGen struct {
	calls   int
	fail    bool
	noToken bool
}

func (g *fakeGen) Generate(context.Context, string) ([]byte, error) {
	g.calls++
	if g.noToken {
		return nil, ErrNoToken
	}
	if g.fail {
		return nil, errors.New("квота исчерпана")
	}
	return pngOf(1024, 1280, color.RGBA{200, 200, 200, 255}), nil
}

type fakeContent struct{ items []Item }

func (c *fakeContent) Queue(context.Context) ([]Item, error) { return c.items, nil }
func (c *fakeContent) Overlay(context.Context, string) ([]byte, error) {
	return pngOf(PostW, PostH, color.RGBA{0, 0, 0, 0}), nil
}

var now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newManager(t *testing.T, items ...Item) (*Manager, *fakeMsg, *fakeWall, *fakeGen) {
	t.Helper()
	msg, wall, gen := &fakeMsg{}, &fakeWall{}, &fakeGen{}
	m := &Manager{
		GroupID: 241936618, AdminID: 1, Msg: msg, Wall: wall, Gen: gen,
		Content: &fakeContent{items}, Store: storage.NewMemory(), DataDir: t.TempDir(),
		Loc: time.UTC, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now },
	}
	return m, msg, wall, gen
}

func btn(kb *vk.Keyboard, label string) map[string]string {
	for _, row := range kb.Buttons {
		for _, b := range row {
			if b.Action.Label == label {
				return vk.Message{Payload: b.Action.Payload}.PayloadMap()
			}
		}
	}
	return nil
}

func TestPreviewApproveSchedules(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, gen := newManager(t, Item{ID: "p1", PublishAt: now.Add(72 * time.Hour), Text: "Текст поста", Prompt: "kitchen", Overlay: "posts/p1/overlay.png"})
	m.Tick(ctx)
	if gen.calls != 1 || msg.previews() != 1 {
		t.Fatalf("gen=%d uploads=%d", gen.calls, msg.previews())
	}
	pv := msg.last()
	if !strings.Contains(pv.text, "Текст поста") || pv.att != "photo1_100" || !pv.kb.Inline {
		t.Fatalf("preview = %+v", pv)
	}
	// повторный цикл не шлёт превью снова
	m.Tick(ctx)
	if msg.previews() != 1 {
		t.Fatal("preview sent twice")
	}
	if btn(pv.kb, "⚡ Сейчас") == nil {
		t.Fatal("future post must have ⚡ Сейчас")
	}
	if !m.HandleAdmin(ctx, 1, "✅ По расписанию", btn(pv.kb, "✅ По расписанию")) {
		t.Fatal("button not handled")
	}
	if wall.posts != 1 || wall.publishDate != now.Add(72*time.Hour).Unix() || wall.message != "Текст поста" {
		t.Fatalf("wall = %+v", wall)
	}
	if !strings.Contains(msg.last().text, "Запланировано") {
		t.Fatalf("reply = %q", msg.last().text)
	}
	// повторное нажатие не публикует дважды
	m.HandleAdmin(ctx, 1, "", btn(pv.kb, "✅ По расписанию"))
	if wall.posts != 1 || !strings.Contains(msg.last().text, "уже") {
		t.Fatalf("double publish: posts=%d reply=%q", wall.posts, msg.last().text)
	}
}

func TestApprovePastDuePublishesNow(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, _ := newManager(t, Item{ID: "p1", PublishAt: now.Add(-time.Hour), Text: "x"})
	m.Tick(ctx)
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_ok", "id": "p1"})
	if wall.publishDate != 0 || !strings.Contains(msg.last().text, "vk.com/wall-241936618_42") {
		t.Fatalf("pd=%d reply=%q", wall.publishDate, msg.last().text)
	}
}

func TestRedoAndReject(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, gen := newManager(t, Item{ID: "p1", PublishAt: now.Add(time.Hour * 24), Text: "x", Prompt: "room"})
	m.Tick(ctx)
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_redo", "id": "p1"})
	if gen.calls != 2 || msg.previews() != 2 {
		t.Fatalf("redo: gen=%d uploads=%d", gen.calls, msg.previews())
	}
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_no", "id": "p1"})
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_ok", "id": "p1"})
	if wall.posts != 0 {
		t.Fatal("rejected post must not be published")
	}
}

func TestGeneratorFailureSilentBeforeThirdTry(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", Text: "x", Prompt: "room"})
	gen.fail = true
	m.Tick(ctx)
	m.Tick(ctx)
	if len(msg.sent) != 0 {
		t.Fatal("must not notify before 3 failures")
	}
}

func TestQueueEditResendsPreview(t *testing.T) {
	ctx := context.Background()
	m, msg, _, _ := newManager(t, Item{ID: "p1", Text: "старый"})
	m.Tick(ctx)
	m.Content.(*fakeContent).items[0].Text = "новый"
	m.Tick(ctx)
	if msg.previews() != 2 || !strings.Contains(msg.last().text, "новый") {
		t.Fatalf("uploads=%d last=%q", msg.previews(), msg.last().text)
	}
}

func TestNoUserTokenAndListAndNonAdmin(t *testing.T) {
	ctx := context.Background()
	m, msg, _, _ := newManager(t, Item{ID: "p1", PublishAt: now.Add(time.Hour), Text: "x"})
	m.Wall = nil
	m.Tick(ctx)
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_ok", "id": "p1"})
	if !strings.Contains(msg.last().text, "VK_USER_TOKEN") {
		t.Fatalf("reply = %q", msg.last().text)
	}
	m.HandleAdmin(ctx, 1, "/очередь", nil)
	if !strings.Contains(msg.last().text, "p1 · ⏳ ждёт одобрения") {
		t.Fatalf("list = %q", msg.last().text)
	}
	if m.HandleAdmin(ctx, 999, "/очередь", nil) {
		t.Fatal("non-admin must be ignored")
	}
}

func TestComposeSizes(t *testing.T) {
	out, err := Compose(pngOf(1024, 1280, color.White), pngOf(PostW, PostH, color.RGBA{0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != PostW || img.Bounds().Dy() != PostH {
		t.Fatalf("bounds %v err %v", img.Bounds(), err)
	}
	if _, err := Compose(nil, pngOf(100, 100, color.White)); err == nil {
		t.Fatal("wrong overlay size must fail")
	}
}

func TestNowButtonPublishesImmediately(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, _ := newManager(t, Item{ID: "p1", PublishAt: now.Add(72 * time.Hour), Text: "x"})
	m.Tick(ctx)
	m.HandleAdmin(ctx, 1, "", btn(msg.last().kb, "⚡ Сейчас"))
	if wall.posts != 1 || wall.publishDate != 0 || !strings.Contains(msg.last().text, "Опубликовано") {
		t.Fatalf("pd=%d reply=%q", wall.publishDate, msg.last().text)
	}
}

func TestUrgentCommand(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, gen := newManager(t)
	if !m.HandleAdmin(ctx, 1, "/срочно", nil) || !strings.Contains(msg.last().text, "Напишите текст") {
		t.Fatalf("empty urgent: %q", msg.last().text)
	}
	m.HandleAdmin(ctx, 1, "/срочно Снизили цену на 2-комнатную\nПишите!", nil)
	pv := msg.last()
	if pv.att == "" || !strings.Contains(pv.text, "Снизили цену на 2-комнатную\nПишите!") || gen.calls != 0 {
		t.Fatalf("urgent preview = %+v gen=%d", pv, gen.calls)
	}
	if btn(pv.kb, "⚡ Сейчас") != nil || btn(pv.kb, "✅ Опубликовать") == nil {
		t.Fatal("urgent post must have plain publish button")
	}
	m.HandleAdmin(ctx, 1, "", btn(pv.kb, "✅ Опубликовать"))
	if wall.posts != 1 || wall.publishDate != 0 {
		t.Fatalf("urgent publish: posts=%d pd=%d", wall.posts, wall.publishDate)
	}
	// очередь из репозитория не затирает срочные посты
	m.Tick(ctx)
	m.HandleAdmin(ctx, 1, "/очередь", nil)
	if !strings.Contains(msg.last().text, "srochno-") {
		t.Fatalf("list = %q", msg.last().text)
	}
}

func TestCarouselSlides(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, gen := newManager(t, Item{ID: "c1", PublishAt: now.Add(48 * time.Hour), Text: "Карусель", Slides: []Slide{
		{Prompt: "city", Overlay: "posts/c1/01.png"}, {Overlay: "posts/c1/02.png"}, {Overlay: "posts/c1/03.png"},
	}})
	m.Tick(ctx)
	pv := msg.last()
	if gen.calls != 1 || msg.uploads != 3 || wall.uploads != 0 || len(strings.Split(pv.att, ",")) != 3 || !strings.Contains(pv.text, "3 слайдов") {
		t.Fatalf("gen=%d uploads=%d preview=%+v", gen.calls, msg.uploads, pv)
	}
	m.HandleAdmin(ctx, 1, "", btn(pv.kb, "✅ По расписанию"))
	if wall.posts != 1 || wall.att != "photo-1_201,photo-1_202,photo-1_203" {
		t.Fatalf("wall = %+v", wall)
	}
}

func TestNoHFTokenWaitsWithoutErrors(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", Text: "x", Prompt: "room"})
	gen.noToken = true
	for i := 0; i < 5; i++ {
		m.Tick(ctx)
	}
	if len(msg.sent) != 0 {
		t.Fatalf("must not notify without token: %+v", msg.sent)
	}
	gen.noToken = false
	m.Tick(ctx)
	if msg.previews() != 1 {
		t.Fatal("preview must be sent once the token appears")
	}
}

func TestPreviewViaGroupAlbumReusedOnApprove(t *testing.T) {
	ctx := context.Background()
	m, msg, wall, _ := newManager(t, Item{ID: "p1", PublishAt: now.Add(48 * time.Hour), Text: "x", Prompt: "room"})
	msg.failUpload = true
	m.Tick(ctx)
	if msg.uploads != 0 || wall.uploads != 1 || msg.last().att != "photo-1_201" {
		t.Fatalf("msg uploads=%d wall uploads=%d att=%q", msg.uploads, wall.uploads, msg.last().att)
	}
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_ok", "id": "p1"})
	if wall.uploads != 1 || wall.att != "photo-1_201" {
		t.Fatalf("approve re-uploaded: uploads=%d att=%q", wall.uploads, wall.att)
	}
}

type failMsg struct{ fakeMsg }

func (f *failMsg) SendAttachment(context.Context, int64, string, string, *vk.Keyboard) error {
	return errors.New("vk api error 15")
}

func TestRetryReusesRenderedImage(t *testing.T) {
	ctx := context.Background()
	m, _, _, gen := newManager(t, Item{ID: "p1", Text: "x", Prompt: "room"})
	good := m.Msg
	m.Msg = &failMsg{}
	m.Tick(ctx)
	m.Msg = good
	m.Tick(ctx)
	if gen.calls != 1 {
		t.Fatalf("image regenerated: gen=%d", gen.calls)
	}
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_redo", "id": "p1"})
	if gen.calls != 2 {
		t.Fatalf("redo must regenerate: gen=%d", gen.calls)
	}
}

func TestGeneratorFailureFallsBackToBrandBackground(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", Text: "x", Prompt: "room"})
	gen.fail = true
	m.Tick(ctx)
	m.Tick(ctx)
	m.Tick(ctx)
	pv := msg.last()
	if pv.att == "" || !strings.Contains(pv.text, "фирменном фоне") || !strings.Contains(pv.text, "квота") || btn(pv.kb, "✅ Опубликовать") == nil {
		t.Fatalf("fallback preview = %+v", pv)
	}
}

func TestChainSkipsMissingKeys(t *testing.T) {
	g := &fakeGen{}
	img, err := Chain{&fakeGen{noToken: true}, g}.Generate(context.Background(), "x")
	if err != nil || len(img) == 0 || g.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, g.calls)
	}
	if _, err := (Chain{&fakeGen{noToken: true}}).Generate(context.Background(), "x"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err=%v", err)
	}
}

type fakePhotos struct {
	calls int
	fail  bool
}

func (f *fakePhotos) Search(context.Context, string) ([]byte, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("сток недоступен")
	}
	return pngOf(800, 1000, color.RGBA{10, 10, 10, 255}), nil
}

func TestStockPhotoFirstThenGenerator(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", Text: "x", Prompt: "room", Query: "cozy room"})
	ph := &fakePhotos{}
	m.Photos = ph
	m.Tick(ctx)
	if ph.calls != 1 || gen.calls != 0 || msg.previews() != 1 {
		t.Fatalf("stock=%d gen=%d previews=%d", ph.calls, gen.calls, msg.previews())
	}
	ph.fail = true
	m.HandleAdmin(ctx, 1, "", map[string]string{"cmd": "ap_redo", "id": "p1"})
	if gen.calls != 1 || msg.previews() != 2 {
		t.Fatalf("fallback to generator: gen=%d previews=%d", gen.calls, msg.previews())
	}
}

func TestStartupResendsPreviewsWithoutAccessKey(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", PublishAt: now.Add(48 * time.Hour), Text: "x", Prompt: "room"})
	msg.failUpload = true
	m.Tick(ctx) // превью ушло с photo-1_201 (без ключа)
	m.retryErrors(ctx)
	m.Tick(ctx)
	if msg.previews() != 2 || gen.calls != 1 {
		t.Fatalf("previews=%d gen=%d", msg.previews(), gen.calls)
	}
	m.retryErrors(ctx) // повторный запуск — уже не переотправляем
	m.Tick(ctx)
	if msg.previews() != 2 {
		t.Fatalf("resent twice: %d", msg.previews())
	}
}

func TestStripAccessKeys(t *testing.T) {
	if got := stripAccessKeys("photo-1_2_abc,photo-1_3"); got != "photo-1_2,photo-1_3" {
		t.Fatal(got)
	}
}

type fakeWriter struct {
	wishes []string
	err    error
}

func (f *fakeWriter) Fire(_ context.Context, wish string) (string, error) {
	f.wishes = append(f.wishes, wish)
	return "https://claude.ai/code/session_1", f.err
}

func TestMakePostCommand(t *testing.T) {
	ctx := context.Background()
	m, msg, _, _ := newManager(t)
	if !m.HandleAdmin(ctx, 1, "сделай пост", nil) || !strings.Contains(msg.last().text, "не настроен") {
		t.Fatalf("no writer: %q", msg.last().text)
	}
	w := &fakeWriter{}
	m.Writer = w
	m.HandleAdmin(ctx, 1, "Сделай посты про ДСК, 2 штуки", nil)
	if len(w.wishes) != 1 || w.wishes[0] != "про ДСК, 2 штуки" || !strings.Contains(msg.last().text, "Принял") {
		t.Fatalf("wishes=%q reply=%q", w.wishes, msg.last().text)
	}
	m.HandleAdmin(ctx, 1, "сделай пост", nil) // повтор сразу — не запускаем второй раз
	if len(w.wishes) != 1 || !strings.Contains(msg.last().text, "уже готовятся") {
		t.Fatalf("cooldown: %q", msg.last().text)
	}
	if m.HandleAdmin(ctx, 999, "сделай пост", nil) {
		t.Fatal("non-admin must be ignored")
	}
}

func TestDraftsReleasedInstantly(t *testing.T) {
	ctx := context.Background()
	m, msg, _, _ := newManager(t,
		Item{ID: "p1", PublishAt: now.Add(72 * time.Hour), Text: "плановый"},
		Item{ID: "d1", Text: "черновик 1", Draft: true},
		Item{ID: "d2", Text: "черновик 2", Draft: true},
		Item{ID: "d3", Text: "черновик 3", Draft: true},
		Item{ID: "d4", Text: "черновик 4", Draft: true},
	)
	m.Tick(ctx)
	if msg.previews() != 1 {
		t.Fatalf("drafts must wait for the button, previews=%d", msg.previews())
	}
	w := &fakeWriter{}
	m.Writer = w
	m.HandleAdmin(ctx, 1, "сделай пост", nil)
	if msg.previews() != 4 {
		t.Fatalf("3 drafts must be sent right away, previews=%d", msg.previews())
	}
	if len(w.wishes) != 1 || w.wishes[0] != "" {
		t.Fatalf("stock refill must be requested: %q", w.wishes)
	}
	states, _ := m.load(ctx)
	// плановый пост — через 3 дня; черновики — через день после него, в 19:00
	want := time.Date(2026, 10, 7, 19, 0, 0, 0, time.UTC)
	if !states["d1"].PublishAt.Equal(want) || !states["d2"].PublishAt.Equal(want.AddDate(0, 0, 2)) {
		t.Fatalf("slots: %v %v", states["d1"].PublishAt, states["d2"].PublishAt)
	}
	m.Tick(ctx) // синхронизация не сбивает назначенную дату
	states, _ = m.load(ctx)
	if !states["d1"].PublishAt.Equal(want) || states["d4"] != nil {
		t.Fatalf("after sync: %v, d4=%v", states["d1"].PublishAt, states["d4"])
	}
}

func TestSlideOwnPhoto(t *testing.T) {
	ctx := context.Background()
	m, msg, _, gen := newManager(t, Item{ID: "p1", Text: "x", Slides: []Slide{{Photo: "posts/p1/photo.jpg", Query: "city", Overlay: "posts/p1/01.png"}}})
	ph := &fakePhotos{}
	m.Photos = ph
	m.Tick(ctx)
	if gen.calls != 0 || ph.calls != 0 || msg.previews() != 1 {
		t.Fatalf("own photo must be used: gen=%d stock=%d previews=%d", gen.calls, ph.calls, msg.previews())
	}
}
