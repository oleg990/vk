"""Плашка поста 1080×1350 (PNG с прозрачным верхом) для автопостинга.
Бот кладёт её поверх фото FLUX. Стиль: brand-profile.md (тёмно-синий + золото, шрифт Inter).

python3 tools/overlay.py content/posts/<id>/overlay.png "КИКЕР" "Строка 1|Строка 2" "Подзаголовок" "Текст кнопки"
"""
import sys
from PIL import Image, ImageDraw, ImageFont

F = "/usr/share/fonts/opentype/inter/"
W, H, K = 1080, 1350, 2
NAVY = (20, 40, 68)
GOLD, WHITE, MUTED = (226, 184, 104), (255, 255, 255), (212, 222, 234)

def font(name, size): return ImageFont.truetype(F + name, size * K)

def render(path, kicker, title_lines, sub, cta):
    img = Image.new("RGBA", (W * K, H * K), (0, 0, 0, 0))
    # градиент снизу: прозрачный с 42% высоты → почти сплошной тёмно-синий
    top = int(H * 0.42) * K
    grad = Image.new("RGBA", (1, H * K - top))
    n = grad.height
    for y in range(n):
        t = y / (n - 1)
        a = 0.82 * min(t / 0.45, 1) if t < 0.45 else 0.82 + (0.97 - 0.82) * (t - 0.45) / 0.55
        r = NAVY[0] - int(6 * max(0, t - 0.45) / 0.55)
        g = NAVY[1] - int(10 * max(0, t - 0.45) / 0.55)
        b = NAVY[2] - int(16 * max(0, t - 0.45) / 0.55)
        grad.putpixel((0, y), (r, g, b, int(255 * a)))
    img.paste(grad.resize((W * K, n)), (0, top))
    d = ImageDraw.Draw(img)
    L = 72 * K
    fk = font("Inter-SemiBold.otf", 30)
    x = L
    for ch in kicker:  # разрядка
        d.text((x, 870 * K), ch, font=fk, fill=GOLD)
        x += d.textlength(ch, font=fk) + 3 * K
    ft = font("Inter-ExtraBold.otf", 74)
    for i, ln in enumerate(title_lines):
        d.text((L, (918 + i * 84) * K), ln, font=ft, fill=WHITE)
    d.text((L, 1098 * K), sub, font=font("Inter-Medium.otf", 34), fill=MUTED)
    fc = font("Inter-SemiBold.otf", 32)
    tw = d.textlength(cta, font=fc)
    ph, py = 78 * K, 1196 * K
    d.rounded_rectangle((L, py, L + tw + 64 * K, py + ph), radius=ph // 2, fill=GOLD + (255,))
    d.text((L + 32 * K, py + 20 * K), cta, font=fc, fill=NAVY)
    img.resize((W, H), Image.LANCZOS).save(path)

if __name__ == "__main__":
    out, kicker, title, sub, cta = sys.argv[1:6]
    render(out, kicker, title.split("|"), sub, cta)
