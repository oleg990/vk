package autopost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
)

// Openverse — открытый каталог фото (api.openverse.org) без ключа и регистрации.
// Берём только CC0 и общественное достояние — их можно публиковать без подписи автора.
type Openverse struct {
	URL  string // https://api.openverse.org
	HTTP *http.Client
}

const userAgent = "realty-bot/1.0 (+https://vk.com/makhanko_nedvizhimost)"

func (o Openverse) Search(ctx context.Context, query string) ([]byte, error) {
	img, err := o.search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("Openverse: %w", err)
	}
	return img, nil
}

type ovImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (o Openverse) find(ctx context.Context, query string, tall bool) ([]ovImage, error) {
	q := url.Values{"q": {query}, "license": {"cc0,pdm"}, "size": {"large"}, "page_size": {"20"}, "mature": {"false"}, "extension": {"jpg,jpeg,png"}}
	if tall {
		q.Set("aspect_ratio", "tall")
	}
	raw, err := o.get(ctx, strings.TrimRight(o.URL, "/")+"/v1/images/?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []ovImage `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ответ: %w", err)
	}
	var ok []ovImage
	for _, r := range out.Results {
		// неизвестный размер пропускаем: мелкая картинка на обложке будет мыльной
		if r.URL != "" && r.Width >= 800 && r.Height >= 800 {
			ok = append(ok, r)
		}
	}
	return ok, nil
}

func (o Openverse) search(ctx context.Context, query string) ([]byte, error) {
	imgs, err := o.find(ctx, query, true)
	if err != nil {
		return nil, err
	}
	if len(imgs) == 0 { // вертикальных нет — берём любые, Compose обрежет по центру
		if imgs, err = o.find(ctx, query, false); err != nil {
			return nil, err
		}
	}
	if len(imgs) == 0 {
		return nil, fmt.Errorf("по запросу «%s» ничего не нашлось", query)
	}
	// пробуем до 3 случайных: часть исходных сайтов бывает недоступна
	var lastErr error
	for _, i := range rand.Perm(len(imgs))[:min(5, len(imgs))] {
		img, err := o.get(ctx, imgs[i].URL)
		if err == nil {
			// формат, который не читается (WebP, SVG…), пропускаем
			if _, _, derr := image.DecodeConfig(bytes.NewReader(img)); derr == nil {
				return img, nil
			}
			err = fmt.Errorf("неподдерживаемый формат: %s", imgs[i].URL)
		}
		lastErr = err
	}
	return nil, lastErr
}

func (o Openverse) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readOK(resp)
}
