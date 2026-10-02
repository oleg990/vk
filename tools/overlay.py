"""Плашка обложки 1080×1350 (PNG, верх прозрачный — там будет фото) для автопостинга.

Два вида:
- тёмная тема — затемнение снизу цветом темы и белый текст;
- светлая тема — светлая карточка со скруглёнными углами внизу и тёмный текст.

python3 tools/overlay.py out.png "КИКЕР" "Строка 1|Строка 2" "Подзаголовок" "Текст кнопки" [тема]
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, os.path.dirname(__file__))
import themes  # noqa: E402

F = "/usr/share/fonts/opentype/inter/"
W, H, K = 1080, 1350, 2


def font(name, size):
    return ImageFont.truetype(F + name, size * K)


def _fit(d, text, name, size, width):
    while size > 30 and d.textlength(text, font=font(name, size)) > width * K:
        size -= 2
    return font(name, size)


def _spaced(d, x, y, text, f, fill):
    for ch in text:
        d.text((x * K, y * K), ch, font=f, fill=fill)
        x += d.textlength(ch, font=f) / K + 3


def render(path, kicker, title_lines, sub, cta, theme=None):
    t = theme or themes.THEMES["navy"]
    img = Image.new("RGBA", (W * K, H * K), (0, 0, 0, 0))
    if t["light"]:
        _card(img, t)
        L, top = 104, 860
    else:
        _shade(img, t["plate"])
        L, top = 72, 870
    d = ImageDraw.Draw(img)
    _spaced(d, L, top, kicker, font("Inter-SemiBold.otf", 30), t["accent"])
    for i, ln in enumerate(title_lines):
        d.text((L * K, (top + 48 + i * 84) * K), ln, font=_fit(d, ln, "Inter-ExtraBold.otf", 74, W - 2 * L), fill=t["text"])
    sy = top + 48 + len(title_lines) * 84 + 12
    d.text((L * K, sy * K), sub, font=font("Inter-Medium.otf", 32), fill=t["muted"])
    fc = font("Inter-SemiBold.otf", 32)
    tw = d.textlength(cta, font=fc)
    ph, py = 76 * K, (sy + 66) * K
    d.rounded_rectangle((L * K, py, L * K + tw + 64 * K, py + ph), radius=ph // 2, fill=t["accent"] + (255,))
    d.text((L * K + 32 * K, py + 19 * K), cta, font=fc, fill=t["on_accent"])
    img.resize((W, H), Image.LANCZOS).save(path)


def _shade(img, color):
    """Затемнение снизу: прозрачно с 42% высоты → почти сплошной цвет темы."""
    top = int(H * 0.42) * K
    n = H * K - top
    grad = Image.new("RGBA", (1, n))
    for y in range(n):
        p = y / (n - 1)
        a = 0.84 * min(p / 0.45, 1) if p < 0.45 else 0.84 + 0.13 * (p - 0.45) / 0.55
        grad.putpixel((0, y), color + (int(255 * a),))
    img.paste(grad.resize((W * K, n)), (0, top))


def _card(img, t):
    """Светлая карточка с мягкой тенью внизу кадра."""
    x0, y0, x1, y1 = 48 * K, 800 * K, (W - 48) * K, (H - 48) * K
    shadow = Image.new("RGBA", img.size, (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rounded_rectangle((x0, y0 + 10 * K, x1, y1 + 10 * K), radius=36 * K, fill=(0, 0, 0, 70))
    img.alpha_composite(shadow)
    ImageDraw.Draw(img).rounded_rectangle((x0, y0, x1, y1), radius=36 * K, fill=t["plate"] + (248,))


if __name__ == "__main__":
    out, kicker, title, sub, cta = sys.argv[1:6]
    th = themes.THEMES.get(sys.argv[6]) if len(sys.argv) > 6 else None
    render(out, kicker, title.split("|"), sub, cta, th)
