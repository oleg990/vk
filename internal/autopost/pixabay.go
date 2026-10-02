package autopost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
)

// Pixabay — бесплатный фотосток (pixabay.com/api/docs): фото можно в коммерческих постах без подписи автора.
type Pixabay struct {
	URL  string // https://pixabay.com
	Key  string
	HTTP *http.Client
}

func (p Pixabay) Search(ctx context.Context, query string) ([]byte, error) {
	if p.Key == "" {
		return nil, ErrNoToken
	}
	img, err := p.search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("Pixabay: %w", err)
	}
	return img, nil
}

func (p Pixabay) search(ctx context.Context, query string) ([]byte, error) {
	q := url.Values{
		"key": {p.Key}, "q": {query}, "image_type": {"photo"}, "orientation": {"vertical"},
		"safesearch": {"true"}, "per_page": {"30"}, "min_width": {"800"},
	}
	// Pexels.get умеет GET без заголовка авторизации — переиспользуем
	g := Pexels{HTTP: p.HTTP}
	raw, err := g.get(ctx, strings.TrimRight(p.URL, "/")+"/api/?"+q.Encode(), false)
	if err != nil {
		return nil, err
	}
	var out struct {
		Hits []struct {
			Large string `json:"largeImageURL"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ответ: %w", err)
	}
	if len(out.Hits) == 0 {
		return nil, fmt.Errorf("по запросу «%s» ничего не нашлось", query)
	}
	return g.get(ctx, out.Hits[rand.IntN(min(len(out.Hits), 15))].Large, false)
}

// Stocks пробует фотостоки по порядку.
type Stocks []PhotoSearch

func (s Stocks) Search(ctx context.Context, query string) ([]byte, error) {
	var errs []string
	for _, st := range s {
		img, err := st.Search(ctx, query)
		if err == nil {
			return img, nil
		}
		if !errors.Is(err, ErrNoToken) {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil, ErrNoToken
	}
	return nil, errors.New(strings.Join(errs, "; "))
}
