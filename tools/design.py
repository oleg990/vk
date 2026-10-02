"""Стили оформления постов: разные шрифты, цвета, фактуры и раскладки.

Каждый стиль умеет три вида слайдов:
  cover — обложка (PNG 1080×1350; прозрачное «окно» — туда бот кладёт фото);
  list  — список (строки с номером/названием/пояснением/ценой);
  cta   — призыв (пункты + кнопка).
Внутренние слайды непрозрачные.

Стили:
  premium   — чёрный + золото, Montserrat, золотой градиент в тексте, рамка с ценой (как «Рекомендуй меня»);
  grunge    — чёрный + красный, Oswald, мазки кистью и наклонные плашки (как «Ищу объекты»);
  editorial — журнальный: кремовый фон, Playfair Display, фото в арке;
  bold      — яркий цветной блок, Unbounded, крупные цифры;
  clean     — аккуратный Inter в одной из цветовых тем (tools/themes.py).

Шрифты (OFL) лежат в tools/fonts. Символа, которого нет в шрифте (например ₽), рисуется шрифтом Montserrat.
"""
import math
import os
import random

from fontTools.ttLib import TTFont
from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont

import themes

HERE = os.path.dirname(__file__)
FD = os.path.join(HERE, "fonts")
INTER = "/usr/share/fonts/opentype/inter/"
W, H, K = 1080, 1350, 2
BRAND = "Олег Маханько | Недвижимость"

# ---------- шрифты ----------

_FONTS = {
    "mont": (os.path.join(FD, "Montserrat.ttf"), True),
    "oswald": (os.path.join(FD, "Oswald.ttf"), True),
    "playfair": (os.path.join(FD, "PlayfairDisplay.ttf"), True),
    "cormorant": (os.path.join(FD, "CormorantGaramond.ttf"), True),
    "unbounded": (os.path.join(FD, "Unbounded.ttf"), True),
    "rubik": (os.path.join(FD, "Rubik.ttf"), True),
    "russo": (os.path.join(FD, "RussoOne.ttf"), False),
    "inter": (INTER + "Inter-Regular.otf", False),
}
_INTER_W = {300: "Light", 400: "Regular", 500: "Medium", 600: "SemiBold", 700: "Bold", 800: "ExtraBold", 900: "Black"}
_cache, _cmaps = {}, {}


def font(name, size, weight=400):
    """Шрифт в «пикселях макета» (×K внутри)."""
    key = (name, size, weight)
    if key in _cache:
        return _cache[key]
    if name == "inter":
        f = ImageFont.truetype(INTER + f"Inter-{_INTER_W.get(weight, 'Regular')}.otf", int(size * K))
    else:
        path, variable = _FONTS[name]
        f = ImageFont.truetype(path, int(size * K))
        if variable:
            try:
                f.set_variation_by_axes([weight])
            except OSError:
                pass
    f._om_name = name
    _cache[key] = f
    return f


def _has(name, ch):
    if name == "inter":
        return True
    if name not in _cmaps:
        _cmaps[name] = TTFont(_FONTS[name][0]).getBestCmap()
    return ord(ch) in _cmaps[name] or ch in " \n"


def _runs(f, text):
    """Делит текст на куски: свой шрифт / запасной Montserrat для отсутствующих символов."""
    name = getattr(f, "_om_name", "inter")
    size = f.size / K
    fb = font("mont", size, 700)
    out, cur, cur_f = [], "", f
    for ch in text:
        cf = f if _has(name, ch) else fb
        if cf is not cur_f and cur:
            out.append((cur, cur_f))
            cur = ""
        cur_f = cf
        cur += ch
    if cur:
        out.append((cur, cur_f))
    return out


def width(d, text, f, spacing=0):
    w = sum(d.textlength(t, font=ff) for t, ff in _runs(f, text))
    return w / K + spacing * max(len(text) - 1, 0)


def text(d, x, y, s, f, fill, spacing=0):
    """Пишет строку с разрядкой spacing (в px макета) и запасным шрифтом для отсутствующих символов."""
    cx = x * K
    for t, ff in _runs(f, s):
        if spacing:
            for ch in t:
                d.text((cx, y * K), ch, font=ff, fill=fill)
                cx += d.textlength(ch, font=ff) + spacing * K
        else:
            d.text((cx, y * K), t, font=ff, fill=fill)
            cx += d.textlength(t, font=ff)
    return cx / K


def fit(d, s, name, size, weight, max_w, spacing=0, min_size=18):
    while size > min_size and width(d, s, font(name, size, weight), spacing) > max_w:
        size -= 1
    return font(name, size, weight)


def gradient_text(img, x, y, s, f, top, bottom, spacing=0):
    """Текст с вертикальным градиентом (золото и т.п.)."""
    mask = Image.new("L", img.size, 0)
    md = ImageDraw.Draw(mask)
    text(md, x, y, s, f, 255, spacing)
    bbox = mask.getbbox()
    if not bbox:
        return
    y0, y1 = bbox[1], bbox[3]
    grad = Image.new("RGBA", img.size)
    gd = ImageDraw.Draw(grad)
    for yy in range(y0, y1 + 1):
        p = (yy - y0) / max(y1 - y0, 1)
        c = tuple(int(a + p * (b - a)) for a, b in zip(top, bottom))
        gd.line((bbox[0], yy, bbox[2], yy), fill=c + (255,))
    img.paste(grad, (0, 0), mask)


# ---------- фактуры ----------

def canvas(color=(0, 0, 0, 0)):
    return Image.new("RGBA", (W * K, H * K), color)


def vgrad(top, bottom, alpha=255):
    g = Image.new("RGBA", (1, H * K))
    for yy in range(H * K):
        p = yy / (H * K - 1)
        g.putpixel((0, yy), tuple(int(a + p * (b - a)) for a, b in zip(top, bottom)) + (alpha,))
    return g.resize((W * K, H * K))


def brush(img, rnd, x0, y0, x1, y1, color, angle=0, rough=18, alpha=255):
    """Мазок кистью: полоса с рваными краями и брызгами."""
    layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    pts_top, pts_bot = [], []
    steps = 40
    for i in range(steps + 1):
        x = x0 + (x1 - x0) * i / steps
        pts_top.append((x * K, (y0 + rnd.uniform(-rough, rough)) * K))
        pts_bot.append((x * K, (y1 + rnd.uniform(-rough, rough)) * K))
    # рваные концы
    left = [(x0 * K + rnd.uniform(-rough, rough) * K * 2, (y0 + (y1 - y0) * t / 6) * K) for t in range(7)]
    right = [(x1 * K + rnd.uniform(-rough, rough) * K * 2, (y1 - (y1 - y0) * t / 6) * K) for t in range(7)]
    d.polygon(pts_top + right + pts_bot[::-1] + left[::-1], fill=color + (alpha,))
    for _ in range(int((x1 - x0) / 6)):  # брызги
        cx = rnd.uniform(x0 - 30, x1 + 30) * K
        cy = rnd.choice([rnd.uniform(y0 - 50, y0), rnd.uniform(y1, y1 + 50)]) * K
        r = rnd.uniform(1, 5) * K
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=color + (alpha,))
    if angle:
        layer = layer.rotate(angle, resample=Image.BICUBIC, center=((x0 + x1) / 2 * K, (y0 + y1) / 2 * K))
    img.alpha_composite(layer)


def slant_bar(d, x, y, w, h, color, skew=14):
    d.polygon([((x + skew) * K, y * K), ((x + w + skew) * K, y * K), ((x + w) * K, (y + h) * K), (x * K, (y + h) * K)], fill=color)


def hexagon(d, cx, cy, r, outline, width_=2, fill=None):
    pts = [((cx + r * math.cos(math.radians(60 * i - 30))) * K, (cy + r * math.sin(math.radians(60 * i - 30))) * K) for i in range(6)]
    d.polygon(pts, outline=outline, fill=fill, width=width_ * K)


def noise(img, amount=14, seed=1):
    """Лёгкое «зерно», чтобы фон не был пластиковым."""
    rnd = random.Random(seed)
    small = Image.new("L", (W // 2, H // 2))
    small.putdata([rnd.randint(128 - amount, 128 + amount) for _ in range(small.width * small.height)])
    n = small.resize(img.size).convert("RGBA")
    n.putalpha(28)
    img.alpha_composite(n)


def window(img, shape, box, feather=0):
    """Вырезает прозрачное окно под фото: shape = rect | round | arch | ragged."""
    x0, y0, x1, y1 = [v * K for v in box]
    mask = Image.new("L", img.size, 0)
    d = ImageDraw.Draw(mask)
    if shape == "round":
        d.rounded_rectangle((x0, y0, x1, y1), radius=40 * K, fill=255)
    elif shape == "arch":
        r = (x1 - x0) / 2
        d.pieslice((x0, y0, x1, y0 + 2 * r), 180, 360, fill=255)
        d.rectangle((x0, y0 + r - 1, x1, y1), fill=255)
    elif shape == "ragged":
        rnd = random.Random(7)
        pts = [(x1, y0), (x1, y1)]
        for i in range(30, -1, -1):  # рваный левый и нижний край
            pts.append((x0 + (x1 - x0) * i / 30 + rnd.uniform(-10, 10) * K, y1 + rnd.uniform(-22, 22) * K))
        for i in range(30, -1, -1):
            pts.append((x0 + rnd.uniform(-24, 24) * K, y0 + (y1 - y0) * i / 30))
        d.polygon(pts, fill=255)
    else:
        d.rectangle((x0, y0, x1, y1), fill=255)
    if feather:
        mask = mask.filter(ImageFilter.GaussianBlur(feather * K))
    alpha = img.getchannel("A")
    img.putalpha(ImageChops.subtract(alpha, mask))


def soft(img, kind, xy, **kw):
    """Полупрозрачная линия/рамка поверх картинки (ImageDraw на RGBA заменил бы альфу, а не смешал)."""
    layer = Image.new("RGBA", img.size, (0, 0, 0, 0))
    getattr(ImageDraw.Draw(layer), kind)(xy, **kw)
    img.alpha_composite(layer)


def save(img, path):
    img.resize((W, H), Image.LANCZOS).save(path)


def pill(d, x, y, label, f, bg, fg, pad=34, h=80, outline=None):
    w = width(d, label, f) + 2 * pad
    if outline:
        d.rounded_rectangle((x * K, y * K, (x + w) * K, (y + h) * K), radius=h // 2 * K, outline=outline, width=3 * K)
    else:
        d.rounded_rectangle((x * K, y * K, (x + w) * K, (y + h) * K), radius=h // 2 * K, fill=bg)
    th = f.size / K
    text(d, x + pad, y + (h - th) / 2 - th * 0.12, label, f, fg)
    return w


# ---------- общий список и призыв (в цветах и шрифтах стиля) ----------

def list_body(img, st, s, page, y):
    d = ImageDraw.Draw(img)
    L = st["L"]
    rows = s["rows"]
    bottom = 1180 if s.get("note") else 1220
    big = len(rows) <= 3
    step = min(250 if big else 196, (bottom - y) // len(rows))
    tn, ts, tv = (48, 30, 44) if big else (40, 27, 38)
    for r in rows:
        x = L
        if r.get("n"):
            st["badge"](img, d, L, y, r["n"])
            x = L + 84
        right = W - L
        if r.get("v"):
            fv = font(st["num"], tv, st["num_w"])
            vw = width(d, r["v"], fv)
            text(d, right - vw, y + 4, r["v"], fv, st["accent"])
            right -= vw + 24
        text(d, x, y, r["t"], fit(d, r["t"], st["body"], tn, st["row_w"], right - x), st["text"])
        if r.get("s"):
            text(d, x, y + tn + 16, r["s"], fit(d, r["s"], st["small"], ts, 400, W - L - x), st["muted"])
        y += step
        if r is not rows[-1]:
            soft(img, "line", (x * K, (y - 36) * K, (W - L) * K, (y - 36) * K), fill=st["line"], width=K)
    footer(img, st, page, s.get("note", ""))


def cta_body(img, st, s, page, y):
    d = ImageDraw.Draw(img)
    L = st["L"]
    for p in s["points"]:
        st["bullet"](d, L, y)
        text(d, L + 54, y, p, fit(d, p, st["small"], 38, 500, W - 2 * L - 54), st["text"])
        y += 92
    st["button"](img, d, L, y + 50, s["button"])
    footer(img, st, page, s.get("note", ""))


def footer(img, st, page, note=""):
    d = ImageDraw.Draw(img)
    L = st["L"]
    if note:
        text(d, L, 1206, note, fit(d, note, st["small"], 21, 400, W - 2 * L), st["muted"])
    soft(img, "line", (L * K, 1250 * K, (W - L) * K, 1250 * K), fill=st["line"], width=2 * K)
    text(d, L, 1270, BRAND, font(st["small"], 23, 600), st["text"])
    fp = font(st["small"], 23, 600)
    text(d, W - L - width(d, page, fp), 1270, page, fp, st["accent"])


def header(img, st, kicker, title, y=96):
    d = ImageDraw.Draw(img)
    L = st["L"]
    text(d, L, y, kicker, font(st["small"], 26, 600), st["accent"], spacing=3)
    y += 48
    for ln in title:
        f = fit(d, ln, st["head"], st["head_size"], st["head_w"], W - 2 * L)
        if st.get("head_upper"):
            ln = ln.upper()
            f = fit(d, ln, st["head"], st["head_size"], st["head_w"], W - 2 * L)
        text(d, L, y, ln, f, st["text"])
        y += st["head_size"] * 1.18
    return y + 40


# ---------- стили ----------

def _circle_badge(fill, fg, name, weight):
    def badge(img, d, x, y, n):
        d.ellipse((x * K, (y + 2) * K, (x + 58) * K, (y + 60) * K), fill=fill)
        f = font(name, 28 if len(n) < 2 else 24, weight)
        text(d, x + 29 - width(d, n, f) / 2, y + 14, n, f, fg)
    return badge


# premium: чёрный + золото
GOLD_T, GOLD_B = (255, 226, 160), (196, 142, 62)


def premium_style():
    st = dict(L=76, text=(255, 255, 255), muted=(196, 190, 178), accent=(232, 192, 120), line=(232, 192, 120, 70),
              head="mont", head_size=66, head_w=800, head_upper=True, body="mont", row_w=700, small="mont",
              num="mont", num_w=800)

    def badge(img, d, x, y, n):
        hexagon(d, x + 29, y + 31, 31, (232, 192, 120), 2)
        f = font("mont", 26, 700)
        text(d, x + 29 - width(d, n, f) / 2, y + 15, n, f, (232, 192, 120))

    def bullet(d, x, y):
        hexagon(d, x + 14, y + 22, 12, (232, 192, 120), 2, fill=(232, 192, 120))

    def button(img, d, x, y, label):
        f = font("mont", 34, 800)
        w = width(d, label.upper(), f, 2) + 120
        d.rounded_rectangle((x * K, y * K, (x + w) * K, (y + 96) * K), radius=48 * K, outline=(232, 192, 120), width=3 * K)
        gradient_text(img, x + 60, y + 28, label.upper(), f, GOLD_T, GOLD_B, 2)

    def bg(seed):
        img = vgrad((14, 12, 10), (4, 4, 4))
        glow = Image.new("RGBA", img.size, (0, 0, 0, 0))
        ImageDraw.Draw(glow).ellipse(((W - 420) * K, -380 * K, (W + 380) * K, 420 * K), fill=(232, 170, 80, 60))
        img.alpha_composite(glow.filter(ImageFilter.GaussianBlur(120 * K)))
        noise(img, seed=seed)
        return img

    st.update(badge=badge, bullet=bullet, button=button, bg=bg)
    return st


def premium_cover(st, c, seed):
    img = canvas()
    # фото справа-сверху, слева и снизу — уход в чёрный
    shade = Image.new("RGBA", img.size, (0, 0, 0, 0))
    sd = ImageDraw.Draw(shade)
    for x in range(W * K):
        p = x / (W * K)
        a = 250 if p < 0.30 else int(250 * max(0, 1 - (p - 0.30) / 0.45))
        sd.line((x, 0, x, H * K), fill=(6, 5, 4, a))
    img.alpha_composite(shade)
    bottom = Image.new("RGBA", img.size, (0, 0, 0, 0))
    bd = ImageDraw.Draw(bottom)
    for yy in range(H * K):
        p = yy / (H * K)
        a = 0 if p < 0.45 else int(min(255, 255 * (p - 0.45) / 0.25))
        bd.line((0, yy, W * K, yy), fill=(6, 5, 4, a))
    img.alpha_composite(bottom)
    d = ImageDraw.Draw(img)
    L = st["L"]
    text(d, L, 110, c["kicker"].upper(), font("mont", 24, 500), (210, 204, 192), spacing=6)
    d.line((L * K, 160 * K, (L + 110) * K, 160 * K), fill=(232, 192, 120), width=2 * K)
    y = 400
    if not c.get("accent") and not c.get("badge") and len(c["title"]) > 1:
        c = dict(c, title=c["title"][:-1], accent=c["title"][-1:])  # последняя строка — золотом
    for ln in c["title"]:
        f = fit(d, ln.upper(), "mont", 92, 900, W * 0.62)
        text(d, L, y, ln.upper(), f, (255, 255, 255))
        y += f.size / K * 1.08
    for ln in c.get("accent", []):
        f = fit(d, ln.upper(), "mont", 100, 900, W * 0.66)
        gradient_text(img, L, y, ln.upper(), f, GOLD_T, GOLD_B)
        y += f.size / K * 1.08
    if c.get("badge"):
        f = fit(d, c["badge"], "mont", 104, 900, W - 2 * L - 80)
        bw = width(d, c["badge"], f) + 70
        bh = f.size / K * 1.25
        d.rounded_rectangle((L * K, (y + 16) * K, (L + bw) * K, (y + 16 + bh) * K), radius=28 * K, outline=(232, 192, 120), width=3 * K)
        gradient_text(img, L + 35, y + 16 + bh * 0.05, c["badge"], f, GOLD_T, GOLD_B)
        y += bh + 40
    d = ImageDraw.Draw(img)
    soft(img, "line", (L * K, (y + 20) * K, (W - L) * K, (y + 20) * K), fill=(232, 192, 120, 120), width=2 * K)
    text(d, L, y + 48, c["sub"], fit(d, c["sub"], "mont", 36, 500, W - 2 * L), (236, 232, 224))
    st["button"](img, d, L, max(y + 130, 1150), c["cta"])
    return img


# grunge: чёрный + красный, мазки
RED = (222, 22, 30)


def grunge_style():
    st = dict(L=70, text=(255, 255, 255), muted=(200, 200, 200), accent=RED, line=(255, 255, 255, 50),
              head="oswald", head_size=78, head_w=700, head_upper=True, body="oswald", row_w=600, small="oswald",
              num="oswald", num_w=700)

    def badge(img, d, x, y, n):
        d.rectangle((x * K, (y + 2) * K, (x + 58) * K, (y + 60) * K), fill=RED)
        f = font("oswald", 32, 700)
        text(d, x + 29 - width(d, n, f) / 2, y + 6, n, f, (255, 255, 255))

    def bullet(d, x, y):
        d.line(((x + 2) * K, (y + 14) * K, (x + 30) * K, (y + 42) * K), fill=RED, width=7 * K)
        d.line(((x + 30) * K, (y + 14) * K, (x + 2) * K, (y + 42) * K), fill=RED, width=7 * K)

    def button(img, d, x, y, label):
        f = font("oswald", 46, 700)
        w = width(d, label.upper(), f) + 70
        slant_bar(d, x, y, w, 92, RED, 18)
        text(d, x + 40, y + 12, label.upper(), f, (255, 255, 255))

    def bg(seed):
        img = canvas((10, 10, 10, 255))
        rnd = random.Random(seed)
        brush(img, rnd, 640, -40, 1160, 60, RED, angle=8, alpha=90)
        noise(img, 20, seed)
        return img

    st.update(badge=badge, bullet=bullet, button=button, bg=bg)
    return st


def grunge_cover(st, c, seed):
    img = canvas((10, 10, 10, 255))
    rnd = random.Random(seed)
    window(img, "ragged", (440, 0, 1080, 860))
    brush(img, rnd, 380, 760, 1120, 880, RED, angle=-6)
    brush(img, rnd, 900, 120, 1150, 190, RED, angle=-30, alpha=200)
    noise(img, 20, seed)
    d = ImageDraw.Draw(img)
    L = st["L"]
    y = 70
    lines = [(ln, False) for ln in c["title"]] + [(ln, True) for ln in c.get("accent", [])]
    if not c.get("accent") and len(lines) > 1:
        lines[-1] = (lines[-1][0], True)
    for ln, red in lines:
        f = fit(d, ln.upper(), "oswald", 150 if red else 120, 700, 560 if y < 860 else W - 2 * L)
        text(d, L, y, ln.upper(), f, RED if red else (255, 255, 255))
        y += f.size / K * 1.08
    y += 30
    f = fit(d, c["kicker"].upper(), "oswald", 44, 600, 560)
    slant_bar(d, L - 10, y, width(d, c["kicker"].upper(), f) + 40, 66, RED, 10)
    text(d, L + 10, y + 2, c["kicker"].upper(), f, (255, 255, 255))
    y = max(y + 110, 900)
    f = fit(d, c["sub"].upper(), "oswald", 46, 500, W - 2 * L)
    text(d, L, y, c["sub"].upper(), f, (255, 255, 255))
    if c.get("badge"):
        fb = fit(d, c["badge"].upper(), "oswald", 84, 700, W - 2 * L - 60)
        bw = width(d, c["badge"].upper(), fb) + 60
        d.rectangle(((W - L - bw) * K, 980 * K, (W - L) * K, (980 + fb.size / K * 1.3) * K), fill=(255, 255, 255))
        text(d, W - L - bw + 30, 984, c["badge"].upper(), fb, (10, 10, 10))
    st["button"](img, d, L, 1170, c["cta"])
    return img


# editorial: кремовый, засечки, арка
def editorial_style(theme):
    ink = (36, 30, 26)
    acc = theme["accent"] if theme["light"] else (176, 82, 52)
    st = dict(L=86, text=ink, muted=(118, 106, 94), accent=acc, line=(36, 30, 26, 40),
              head="playfair", head_size=70, head_w=700, body="playfair", row_w=600, small="mont",
              num="playfair", num_w=700)
    st["badge"] = _circle_badge(acc, (255, 255, 255), "playfair", 700)

    def bullet(d, x, y):
        d.line((x * K, (y + 26) * K, (x + 32) * K, (y + 26) * K), fill=acc, width=3 * K)

    def button(img, d, x, y, label):
        f = font("mont", 32, 600)
        w = width(d, label, f, 1) + 80
        d.rectangle((x * K, y * K, (x + w) * K, (y + 84) * K), fill=ink)
        text(d, x + 40, y + 24, label, f, (250, 245, 236), 1)

    def bg(seed):
        img = vgrad((250, 245, 236), (240, 231, 216))
        soft(img, "rectangle", (34 * K, 34 * K, (W - 34) * K, (H - 34) * K), outline=(36, 30, 26, 60), width=2 * K)
        noise(img, 10, seed)
        return img

    st.update(bullet=bullet, button=button, bg=bg)
    return st


def editorial_cover(st, c, seed):
    img = st["bg"](seed)
    window(img, "arch", (150, 80, 930, 790))
    d = ImageDraw.Draw(img)
    d.arc((136 * K, 66 * K, 944 * K, (80 + 780 + 14) * K), 180, 360, fill=st["accent"], width=2 * K)
    L = st["L"]
    f = font("mont", 24, 600)
    kw = width(d, c["kicker"].upper(), f, 5)
    text(d, (W - kw) / 2, 840, c["kicker"].upper(), f, st["accent"], 5)
    y = 900
    for ln in c["title"] + c.get("accent", []):
        ft = fit(d, ln, "playfair", 84, 800, W - 2 * L)
        tw = width(d, ln, ft)
        text(d, (W - tw) / 2, y, ln, ft, st["text"])
        y += ft.size / K * 1.12
    fs = fit(d, c["sub"], "cormorant", 44, 500, W - 2 * L)
    sw = width(d, c["sub"], fs)
    text(d, (W - sw) / 2, y + 6, c["sub"], fs, st["muted"])
    fb = font("mont", 30, 600)
    bw = width(d, c["cta"], fb, 1) + 80
    st["button"](img, d, (W - bw) / 2, max(y + 80, 1180), c["cta"])
    return img


# bold: яркий цветной блок, Unbounded
BOLD_THEMES = [
    dict(bg=(198, 255, 61), ink=(16, 16, 16), acc=(16, 16, 16), on=(198, 255, 61)),  # лайм
    dict(bg=(108, 76, 255), ink=(255, 255, 255), acc=(255, 214, 64), on=(30, 20, 80)),  # фиолетовый
    dict(bg=(255, 96, 54), ink=(255, 255, 255), acc=(30, 20, 18), on=(255, 255, 255)),  # коралловый
    dict(bg=(0, 156, 255), ink=(255, 255, 255), acc=(255, 236, 90), on=(0, 60, 110)),  # голубой
]


def bold_style(seed):
    t = BOLD_THEMES[seed % len(BOLD_THEMES)]
    st = dict(L=70, text=t["ink"], muted=t["ink"],
              accent=t["acc"], line=t["ink"] + (60,), head="unbounded", head_size=62, head_w=800, body="rubik",
              row_w=700, small="rubik", num="unbounded", num_w=800, t=t)
    st["badge"] = _circle_badge(t["acc"], t["on"], "unbounded", 800)

    def bullet(d, x, y):
        d.rectangle((x * K, (y + 12) * K, (x + 26) * K, (y + 38) * K), fill=t["acc"])

    def button(img, d, x, y, label):
        f = font("unbounded", 32, 800)
        w = width(d, label, f) + 80
        d.rounded_rectangle((x * K, y * K, (x + w) * K, (y + 92) * K), radius=18 * K, fill=t["acc"])
        text(d, x + 40, y + 26, label, f, t["on"])

    def bg(seed_):
        img = canvas(t["bg"] + (255,))
        d = ImageDraw.Draw(img)
        r = 200
        cx, cy = W + 60, -60
        d.ellipse(((cx - r) * K, (cy - r) * K, (cx + r) * K, (cy + r) * K), outline=t["acc"], width=6 * K)
        return img

    st.update(bullet=bullet, button=button, bg=bg)
    return st


def bold_cover(st, c, seed):
    t = st["t"]
    img = canvas(t["bg"] + (255,))
    window(img, "round", (56, 56, W - 56, 700))
    d = ImageDraw.Draw(img)
    L = st["L"]
    f = font("rubik", 28, 700)
    pw = width(d, c["kicker"].upper(), f, 2) + 50
    d.rounded_rectangle(((L - 6) * K, 640 * K, (L - 6 + pw) * K, 704 * K), radius=32 * K, fill=t["acc"])
    text(d, L + 19, 656, c["kicker"].upper(), f, t["on"], 2)
    y = 740
    for ln in c["title"] + c.get("accent", []):
        ft = fit(d, ln, "unbounded", 86, 900, W - 2 * L)
        text(d, L, y, ln, ft, t["ink"])
        y += ft.size / K * 1.15
    text(d, L, y + 10, c["sub"], fit(d, c["sub"], "rubik", 38, 500, W - 2 * L), t["ink"])
    st["button"](img, d, L, max(y + 90, 1180), c["cta"])
    return img


# clean: прежний аккуратный Inter в цветовой теме
def clean_style(theme):
    st = dict(L=72, text=theme["text"], muted=theme["muted"], accent=theme["accent"], line=themes.line(theme),
              head="inter", head_size=64, head_w=800, body="inter", row_w=700, small="inter", num="inter", num_w=800)
    st["badge"] = _circle_badge(theme["accent"], theme["on_accent"], "inter", 700)

    def bullet(d, x, y):
        d.rounded_rectangle((x * K, (y + 14) * K, (x + 24) * K, (y + 38) * K), radius=6 * K, fill=theme["accent"])

    def button(img, d, x, y, label):
        pill(d, x, y, label, font("inter", 34, 700), theme["accent"] + (255,), theme["on_accent"], pad=36, h=88)

    def bg(seed):
        img = vgrad(*theme["bg"])
        deco = Image.new("RGBA", img.size, (0, 0, 0, 0))
        r = 340 * K
        cx, cy = (W + 60) * K, -40 * K
        ImageDraw.Draw(deco).ellipse((cx - r, cy - r, cx + r, cy + r), fill=theme["accent"] + (34,))
        img.alpha_composite(deco)
        return img

    st.update(bullet=bullet, button=button, bg=bg)
    return st


def clean_cover(st, c, seed, theme):
    import overlay
    path = "/tmp/_om_cover.png"
    overlay.render(path, c["kicker"], c["title"] + c.get("accent", []), c["sub"], c["cta"], theme)
    return Image.open(path).convert("RGBA").resize((W * K, H * K))


STYLES = ["premium", "grunge", "editorial", "bold", "clean"]


def render_post(spec, out_dir, seed):
    """Рисует все слайды поста; возвращает список путей."""
    style = spec.get("style") or STYLES[seed % len(STYLES)]
    theme = themes.pick(spec.get("theme", ""), spec["id"])
    st = {"premium": premium_style, "grunge": grunge_style}.get(style, lambda: None)()
    if style == "editorial":
        st = editorial_style(theme)
    elif style == "bold":
        st = bold_style(seed)
    elif style == "clean":
        st = clean_style(theme)
    n = len(spec["slides"])
    paths = []
    for i, s in enumerate(spec["slides"]):
        page = f"{i + 1}/{n}"
        if "cover" in s:
            c = s["cover"]
            img = {"premium": lambda: premium_cover(st, c, seed), "grunge": lambda: grunge_cover(st, c, seed),
                   "editorial": lambda: editorial_cover(st, c, seed), "bold": lambda: bold_cover(st, c, seed),
                   "clean": lambda: clean_cover(st, c, seed, theme)}[style]()
        else:
            img = st["bg"](seed + i)
            body = s.get("list") or s.get("cta")
            y = header(img, st, body["kicker"], body["title"])
            if "list" in s:
                list_body(img, st, body, page, y)
            else:
                cta_body(img, st, body, page, y)
        p = os.path.join(out_dir, f"{i + 1:02d}.png")
        save(img, p)
        paths.append(p)
    return style, paths
