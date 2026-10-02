"""Цветовые темы постов. Посты не обязаны быть в одном стиле: у каждого своя тема (spec.json → "theme"),
а если тема не указана, она выбирается по id поста.

bg      — фон внутренних слайдов (градиент сверху вниз)
plate   — цвет плашки на обложке
text    — основной текст, muted — пояснения, accent — акцент (кикер, цены, кнопка), on_accent — текст на кнопке
light   — светлая тема (тёмный текст): обложка рисуется карточкой, а не затемнением снизу
"""
import hashlib

THEMES = {
    "navy": dict(bg=((20, 40, 68), (31, 62, 102)), plate=(20, 40, 68), text=(255, 255, 255), muted=(190, 204, 222),
                 accent=(226, 184, 104), on_accent=(20, 40, 68), light=False),
    "terracotta": dict(bg=((178, 78, 54), (128, 48, 34)), plate=(150, 58, 40), text=(255, 246, 236), muted=(250, 214, 196),
                       accent=(255, 222, 170), on_accent=(120, 40, 26), light=False),
    "forest": dict(bg=((30, 72, 56), (16, 46, 36)), plate=(22, 56, 44), text=(255, 255, 255), muted=(196, 222, 206),
                   accent=(236, 200, 110), on_accent=(22, 56, 44), light=False),
    "charcoal": dict(bg=((38, 38, 42), (16, 16, 18)), plate=(22, 22, 24), text=(255, 255, 255), muted=(186, 186, 190),
                     accent=(255, 140, 64), on_accent=(22, 22, 24), light=False),
    "burgundy": dict(bg=((104, 30, 52), (66, 16, 32)), plate=(80, 20, 40), text=(255, 244, 246), muted=(236, 196, 206),
                     accent=(244, 204, 160), on_accent=(80, 20, 40), light=False),
    "cream": dict(bg=((250, 245, 236), (236, 226, 210)), plate=(250, 245, 236), text=(34, 30, 26), muted=(112, 100, 88),
                  accent=(212, 92, 50), on_accent=(255, 255, 255), light=True),
    "sky": dict(bg=((236, 244, 252), (210, 226, 244)), plate=(246, 250, 255), text=(14, 40, 66), muted=(84, 108, 134),
                accent=(0, 119, 255), on_accent=(255, 255, 255), light=True),
    "mint": dict(bg=((232, 246, 240), (206, 234, 222)), plate=(244, 252, 248), text=(18, 52, 42), muted=(78, 112, 100),
                 accent=(16, 150, 110), on_accent=(255, 255, 255), light=True),
}


def pick(name, post_id):
    if name in THEMES:
        return THEMES[name]
    keys = sorted(THEMES)
    return THEMES[keys[int(hashlib.md5(post_id.encode()).hexdigest(), 16) % len(keys)]]


def line(theme):
    """Цвет тонких разделителей."""
    return (0, 0, 0, 34) if theme["light"] else (255, 255, 255, 40)
