"""Сборка поста-карусели из spec.json: рисует оверлеи слайдов и вносит пост в content/queue.json.

python3 tools/build_post.py content/posts/<id>/spec.json [--preview out_dir]

spec.json:
{
  "id": "...", "publish_at": "2026-10-11T19:00:00+03:00", "text": "...",
  "slides": [
    {"prompt": "english FLUX prompt", "cover": {"kicker": "", "title": ["", ""], "sub": "", "cta": ""}},
    {"list": {"kicker": "", "title": ["", ""], "rows": [{"n": "1", "t": "Имя", "s": "пояснение", "v": "от 5,1 млн"}], "note": ""}},
    {"cta": {"kicker": "", "title": ["", ""], "points": ["", ""], "button": ""}}
  ]
}
Слайды без prompt бот кладёт на фирменный градиент; первый слайд с prompt — фото FLUX.
--preview дополнительно собирает PNG как их увидит пользователь (обложка — на заглушке вместо фото).
"""
import json
import os
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, os.path.dirname(__file__))
import overlay as cover_overlay  # noqa: E402

F = "/usr/share/fonts/opentype/inter/"
W, H, K = 1080, 1350, 2
NAVY, NAVY2 = (20, 40, 68), (31, 62, 102)
GOLD, WHITE, MUTED, LINE = (226, 184, 104), (255, 255, 255), (190, 204, 222), (255, 255, 255, 38)
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
    spaced(d, L, 92, kicker, font("Inter-SemiBold.otf", 28), GOLD)
    for i, ln in enumerate(title):
        f = fit(d, ln, "Inter-ExtraBold.otf", 64, W - 2 * L)
        d.text((L * K, (138 + i * 76) * K), ln, font=f, fill=WHITE)
    return 138 + len(title) * 76 + 34


def footer(d, page, note=""):
    if note:
        d.text((L * K, 1216 * K), note, font=fit(d, note, "Inter-Regular.otf", 22, W - 2 * L), fill=MUTED)
    d.line((L * K, 1258 * K, (W - L) * K, 1258 * K), fill=LINE, width=2 * K)
    d.text((L * K, 1276 * K), BRAND, font=font("Inter-SemiBold.otf", 24), fill=WHITE)
    fp = font("Inter-Medium.otf", 24)
    d.text(((W - L) * K - d.textlength(page, font=fp), 1276 * K), page, font=fp, fill=GOLD)


def list_slide(path, s, page):
    img = Image.new("RGBA", (W * K, H * K), (0, 0, 0, 0))
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
            d.ellipse((L * K, (y + 4) * K, (L + 56) * K, (y + 60) * K), fill=GOLD)
            fn = font("Inter-Bold.otf", 28)
            d.text(((L + 28) * K - d.textlength(r["n"], font=fn) / 2, (y + 14) * K), r["n"], font=fn, fill=NAVY)
            x = L + 80
        right = W - L
        if r.get("v"):
            fv = font("Inter-ExtraBold.otf", tv)
            vw = d.textlength(r["v"], font=fv) / K
            d.text(((right - vw) * K, (y + 6) * K), r["v"], font=fv, fill=GOLD)
            right -= vw + 24
        d.text((x * K, y * K), r["t"], font=fit(d, r["t"], "Inter-Bold.otf", tn, right - x), fill=WHITE)
        if r.get("s"):
            d.text((x * K, (y + tn + 18) * K), r["s"], font=fit(d, r["s"], "Inter-Regular.otf", ts, W - L - x), fill=MUTED)
        y += step
        if r is not rows[-1]:
            d.line((x * K, (y - 34) * K, (W - L) * K, (y - 34) * K), fill=LINE, width=K)
    footer(d, page, s.get("note", ""))
    img.resize((W, H), Image.LANCZOS).save(path)


def cta_slide(path, s, page):
    img = Image.new("RGBA", (W * K, H * K), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    y = header(d, s["kicker"], s["title"]) + 10
    fp = font("Inter-Medium.otf", 34)
    for p in s["points"]:
        d.rounded_rectangle((L * K, (y + 14) * K, (L + 24) * K, (y + 38) * K), radius=6 * K, fill=GOLD)
        d.text(((L + 50) * K, y * K), p, font=fit(d, p, "Inter-Medium.otf", 40, W - 2 * L - 50), fill=WHITE)
        y += 100
    fc = font("Inter-Bold.otf", 34)
    tw = d.textlength(s["button"], font=fc) / K
    py = y + 60
    d.rounded_rectangle((L * K, py * K, (L + tw + 72) * K, (py + 88) * K), radius=44 * K, fill=GOLD + (255,))
    d.text(((L + 36) * K, (py + 22) * K), s["button"], font=fc, fill=NAVY)
    footer(d, page, s.get("note", ""))
    img.resize((W, H), Image.LANCZOS).save(path)


def gradient():
    g = Image.new("RGB", (1, H))
    for y in range(H):
        t = y / (H - 1)
        g.putpixel((0, y), tuple(int(a + t * (b - a)) for a, b in zip(NAVY, NAVY2)))
    return g.resize((W, H))


def stand_in_photo():
    """Заглушка вместо фото FLUX для предпросмотра обложки."""
    g = Image.new("RGB", (1, H))
    for y in range(H):
        t = y / (H - 1)
        g.putpixel((0, y), (int(150 - 60 * t), int(175 - 70 * t), int(205 - 80 * t)))
    return g.resize((W, H))


def build(spec_path, preview=None):
    spec = json.load(open(spec_path, encoding="utf-8"))
    pid, base = spec["id"], os.path.dirname(spec_path)
    n = len(spec["slides"])
    slides = []
    for i, s in enumerate(spec["slides"]):
        name = f"{i + 1:02d}.png"
        out = os.path.join(base, name)
        page = f"{i + 1}/{n}"
        if "cover" in s:
            c = s["cover"]
            cover_overlay.render(out, c["kicker"], c["title"], c["sub"], c["cta"])
        elif "list" in s:
            list_slide(out, s["list"], page)
        else:
            cta_slide(out, s["cta"], page)
        item = {"overlay": f"posts/{pid}/{name}"}
        if s.get("prompt"):
            item = {"prompt": s["prompt"], **item}
        slides.append(item)
        if preview:
            bg = stand_in_photo() if s.get("prompt") else gradient()
            bg = bg.convert("RGBA")
            bg.alpha_composite(Image.open(out).convert("RGBA"))
            os.makedirs(preview, exist_ok=True)
            bg.convert("RGB").save(os.path.join(preview, f"{pid}-{name}"))
    qp = os.path.join(os.path.dirname(base.rstrip("/")), "..", "queue.json")
    qp = os.path.normpath(qp)
    queue = json.load(open(qp, encoding="utf-8"))
    entry = {"id": pid, "publish_at": spec["publish_at"], "slides": slides, "text": spec["text"]}
    queue = [q for q in queue if q["id"] != pid] + [entry]
    queue.sort(key=lambda q: q["publish_at"])
    with open(qp, "w", encoding="utf-8") as f:
        json.dump(queue, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(pid, n, "slides")


if __name__ == "__main__":
    args = sys.argv[1:]
    prev = None
    if "--preview" in args:
        i = args.index("--preview")
        prev = args[i + 1]
        del args[i:i + 2]
    for a in args:
        build(a, prev)
