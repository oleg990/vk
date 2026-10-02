package autopost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
)

// Pexels — бесплатный фотосток (pexels.com/api): настоящие фото, можно в коммерческих постах без подписи автора.
type Pexels struct {
	URL  string // https://api.pexels.com
	Key  string
	HTTP *http.Client
}

func (p Pexels) Search(ctx context.Context, query string) ([]byte, error) {
	if p.Key == "" {
		return nil, ErrNoToken
	}
	img, err := p.search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("Pexels: %w", err)
	}
	return img, nil
}

func (p Pexels) search(ctx context.Context, query string) ([]byte, error) {
	q := url.Values{"query": {query}, "orientation": {"portrait"}, "size": {"large"}, "per_page": {"30"}}
	var out struct {
		Photos []struct {
			Src struct {
				Original string `json:"original"`
			} `json:"src"`
		} `json:"photos"`
	}
	raw, err := p.get(ctx, strings.TrimRight(p.URL, "/")+"/v1/search?"+q.Encode(), true)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ответ: %w", err)
	}
	if len(out.Photos) == 0 {
		return nil, fmt.Errorf("по запросу «%s» ничего не нашлось", query)
	}
	// случайное из первых результатов — «🔁 Другое фото» даст другое
	src := out.Photos[rand.IntN(min(len(out.Photos), 15))].Src.Original
	sep := "?"
	if strings.Contains(src, "?") {
		sep = "&"
	}
	return p.get(ctx, src+sep+"auto=compress&cs=tinysrgb&fit=crop&w=1080&h=1350", false)
}

func (p Pexels) get(ctx context.Context, u string, auth bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if auth {
		req.Header.Set("Authorization", p.Key)
	}
	hc := p.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("%s: %s", resp.Status, msg)
	}
	return raw, nil
}
