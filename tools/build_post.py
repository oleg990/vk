"""Сборка поста-карусели из spec.json: рисует оверлеи слайдов и вносит пост в content/queue.json.

python3 tools/build_post.py content/posts/<id>/spec.json [--preview out_dir]

spec.json:
{
  "id": "...", "publish_at": "2026-10-11T19:00:00+03:00", "text": "...",
  "slides": [
    {"query": "stock photo search, 2-5 english words", "prompt": "english generator prompt", "cover": {"kicker": "", "title": ["", ""], "sub": "", "cta": ""}},
    {"list": {"kicker": "", "title": ["", ""], "rows": [{"n": "1", "t": "Имя", "s": "пояснение", "v": "от 5,1 млн"}], "note": ""}},
    {"cta": {"kicker": "", "title": ["", ""], "points": ["", ""], "button": ""}}
  ]
}
"style" (необязательно): premium, grunge, editorial, bold, clean — см. tools/design.py; без него — по id.
"theme" (для clean и editorial): navy, terracotta, forest, charcoal, burgundy, cream, sky, mint — см. tools/themes.py;
без неё тема выбирается по id. Внутренние слайды рисуются с непрозрачным фоном темы, обложка — поверх фото.
--preview дополнительно собирает PNG как их увидит пользователь (обложка — на заглушке вместо фото).
"""
import hashlib
import json
import os
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, os.path.dirname(__file__))
import overlay as cover_overlay  # noqa: E402
import themes  # noqa: E402
import design  # noqa: E402

F = "/usr/share/fonts/opentype/inter/"
W, H, K = 1080, 1350, 2
# цвета текущей темы — выставляет build() перед отрисовкой поста
T = themes.THEMES["navy"]
L = 72
BRAND = "Олег Маханько | Недвижимость"


def font(name, size):
    return ImageFont.truetype(F + name, size * K)


def fit(d, text, name, size, width):
    """Уменьшает шрифт, пока строка не влезет по ширине."""
    while size > 18 and d.textlength(text, font=font(name, size)) > width * K:
        size -= 1
    return font(name, size)


def spaced(d, x, y, text, f, fill):
    for ch in text:
        d.text((x * K, y * K), ch, font=f, fill=fill)
        x += d.textlength(ch, font=f) / K + 3


def header(d, kicker, title):
    spaced(d, L, 92, kicker, font("Inter-SemiBold.otf", 28), T["accent"])
    for i, ln in enumerate(title):
        f = fit(d, ln, "Inter-ExtraBold.otf", 64, W - 2 * L)
        d.text((L * K, (138 + i * 76) * K), ln, font=f, fill=T["text"])
    return 138 + len(title) * 76 + 34


def footer(d, page, note=""):
    if note:
        d.text((L * K, 1216 * K), note, font=fit(d, note, "Inter-Regular.otf", 22, W - 2 * L), fill=T["muted"])
    d.line((L * K, 1258 * K, (W - L) * K, 1258 * K), fill=themes.line(T), width=2 * K)
    d.text((L * K, 1276 * K), BRAND, font=font("Inter-SemiBold.otf", 24), fill=T["text"])
    fp = font("Inter-Medium.otf", 24)
    d.text(((W - L) * K - d.textlength(page, font=fp), 1276 * K), page, font=fp, fill=T["accent"])


def background():
    """Непрозрачный фон внутреннего слайда: градиент темы и крупный полупрозрачный круг-акцент."""
    top, bottom = T["bg"]
    g = Image.new("RGB", (1, H * K))
    for y in range(H * K):
        p = y / (H * K - 1)
        g.putpixel((0, y), tuple(int(a + p * (b - a)) for a, b in zip(top, bottom)))
    img = g.resize((W * K, H * K)).convert("RGBA")
    deco = Image.new("RGBA", img.size, (0, 0, 0, 0))
    r = 340 * K
    cx, cy = (W + 60) * K, -40 * K
    ImageDraw.Draw(deco).ellipse((cx - r, cy - r, cx + r, cy + r), fill=T["accent"] + (34,))
    img.alpha_composite(deco)
    return img


def list_slide(path, s, page):
    img = background()
    d = ImageDraw.Draw(img)
    y = header(d, s["kicker"], s["title"])
    rows = s["rows"]
    bottom = 1196 if s.get("note") else 1236
    big = len(rows) <= 3
    step = min(250 if big else 200, (bottom - y) // len(rows))
    tn, ts, tv = (50, 33, 44) if big else (42, 29, 40)
    for r in rows:
        x = L
        if r.get("n"):
            d.ellipse((L * K, (y + 4) * K, (L + 56) * K, (y + 60) * K), fill=T["accent"])
            fn = font("Inter-Bold.otf", 28)
            d.text(((L + 28) * K - d.textlength(r["n"], font=fn) / 2, (y + 14) * K), r["n"], font=fn, fill=T["on_accent"])
            x = L + 80
        right = W - L
        if r.get("v"):
            fv = font("Inter-ExtraBold.otf", tv)
            vw = d.textlength(r["v"], font=fv) / K
            d.text(((right - vw) * K, (y + 6) * K), r["v"], font=fv, fill=T["accent"])
            right -= vw + 24
        d.text((x * K, y * K), r["t"], font=fit(d, r["t"], "Inter-Bold.otf", tn, right - x), fill=T["text"])
        if r.get("s"):
            d.text((x * K, (y + tn + 18) * K), r["s"], font=fit(d, r["s"], "Inter-Regular.otf", ts, W - L - x), fill=T["muted"])
        y += step
        if r is not rows[-1]:
            d.line((x * K, (y - 34) * K, (W - L) * K, (y - 34) * K), fill=themes.line(T), width=K)
    footer(d, page, s.get("note", ""))
    img.resize((W, H), Image.LANCZOS).save(path)


def cta_slide(path, s, page):
    img = background()
    d = ImageDraw.Draw(img)
    y = header(d, s["kicker"], s["title"]) + 10
    fp = font("Inter-Medium.otf", 34)
    for p in s["points"]:
        d.rounded_rectangle((L * K, (y + 14) * K, (L + 24) * K, (y + 38) * K), radius=6 * K, fill=T["accent"])
        d.text(((L + 50) * K, y * K), p, font=fit(d, p, "Inter-Medium.otf", 40, W - 2 * L - 50), fill=T["text"])
        y += 100
    fc = font("Inter-Bold.otf", 34)
    tw = d.textlength(s["button"], font=fc) / K
    py = y + 60
    d.rounded_rectangle((L * K, py * K, (L + tw + 72) * K, (py + 88) * K), radius=44 * K, fill=T["accent"] + (255,))
    d.text(((L + 36) * K, (py + 22) * K), s["button"], font=fc, fill=T["on_accent"])
    footer(d, page, s.get("note", ""))
    img.resize((W, H), Image.LANCZOS).save(path)


def gradient():
    top, bottom = T["bg"]
    g = Image.new("RGB", (1, H))
    for y in range(H):
        p = y / (H - 1)
        g.putpixel((0, y), tuple(int(a + p * (b - a)) for a, b in zip(top, bottom)))
    return g.resize((W, H))


def stand_in_photo():
    """Заглушка вместо фото для предпросмотра: закатное небо и силуэты домов с окнами."""
    import random
    rnd = random.Random(3)
    img = Image.new("RGB", (W, H))
    d = ImageDraw.Draw(img)
    for y in range(H):
        p = y / H
        d.line((0, y, W, y), fill=(int(70 + 170 * p), int(90 + 80 * p), int(140 - 60 * p)))
    x = -40
    while x < W:
        bw, bh = rnd.randint(110, 220), rnd.randint(380, 1050)
        d.rectangle((x, H - bh, x + bw, H), fill=(28, 30, 40))
        for wy in range(H - bh + 30, H, 46):
            for wx in range(x + 16, x + bw - 20, 34):
                if rnd.random() < 0.45:
                    d.rectangle((wx, wy, wx + 16, wy + 24), fill=(255, 196, 110))
        x += bw + rnd.randint(10, 40)
    return img


def build(spec_path, preview=None):
    global T
    spec = json.load(open(spec_path, encoding="utf-8"))
    pid, base = spec["id"], os.path.dirname(spec_path)
    T = themes.pick(spec.get("theme", ""), pid)
    seed = int(hashlib.md5(pid.encode()).hexdigest(), 16) % 10000
    style, paths = design.render_post(spec, base, seed)
    slides = []
    for i, (s, out) in enumerate(zip(spec["slides"], paths)):
        name = os.path.basename(out)
        # ?v=<хеш> — бот видит, что картинка поменялась, и пересобирает превью (GitHub параметр игнорирует)
        ver = hashlib.md5(open(out, "rb").read()).hexdigest()[:8]
        item = {"overlay": f"posts/{pid}/{name}?v={ver}"}
        if s.get("prompt"):
            item = {"prompt": s["prompt"], **item}
        if s.get("query"):
            item = {"query": s["query"], **item}
        if s.get("photo"):
            # своё фото (рендер ЖК и т.п.), путь относительно content/ — бот возьмёт его вместо стока
            item = {"photo": s["photo"], **item}
        slides.append(item)
        if preview:
            own = os.path.join(os.path.dirname(os.path.dirname(base)), s["photo"]) if s.get("photo") else ""
            if own and os.path.exists(own):
                ph = Image.open(own).convert("RGB")
                sc = max(W / ph.width, H / ph.height)
                ph = ph.resize((int(ph.width * sc) + 1, int(ph.height * sc) + 1), Image.LANCZOS)
                l, t = (ph.width - W) // 2, (ph.height - H) // 2
                bg = ph.crop((l, t, l + W, t + H)).convert("RGBA")
            else:
                bg = stand_in_photo().convert("RGBA")
            bg.alpha_composite(Image.open(out).convert("RGBA"))
            os.makedirs(preview, exist_ok=True)
            bg.convert("RGB").save(os.path.join(preview, f"{pid}-{name}"))
    qp = os.path.join(os.path.dirname(base.rstrip("/")), "..", "queue.json")
    qp = os.path.normpath(qp)
    queue = json.load(open(qp, encoding="utf-8"))
    entry = {"id": pid, "slides": slides, "text": spec["text"]}
    if spec.get("draft"):
        entry["draft"] = True  # запас: дату назначит бот, когда Олег нажмёт «сделай пост»
    else:
        entry["publish_at"] = spec["publish_at"]
    queue = [q for q in queue if q["id"] != pid] + [entry]
    queue.sort(key=lambda q: (q.get("draft", False), q.get("publish_at", ""), q["id"]))
    with open(qp, "w", encoding="utf-8") as f:
        json.dump(queue, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(pid, style, len(paths), "slides")


if __name__ == "__main__":
    args = sys.argv[1:]
    prev = None
    if "--preview" in args:
        i = args.index("--preview")
        prev = args[i + 1]
        del args[i:i + 2]
    for a in args:
        build(a, prev)
