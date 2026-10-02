package autopost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// Chain пробует генераторы по порядку и возвращает первую удачную картинку.
type Chain []Generator

func (c Chain) Generate(ctx context.Context, prompt string) ([]byte, error) {
	var errs []string
	for _, g := range c {
		img, err := g.Generate(ctx, prompt)
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

// Kandinsky — генерация через Fusion Brain (api-key.fusionbrain.ai), бесплатный ключ Сбера.
type Kandinsky struct {
	URL    string // https://api-key.fusionbrain.ai
	Key    string
	Secret string
	HTTP   *http.Client
	Poll   time.Duration // пауза между проверками статуса (по умолчанию 3 с)
}

func (k Kandinsky) Generate(ctx context.Context, prompt string) ([]byte, error) {
	if k.Key == "" || k.Secret == "" {
		return nil, ErrNoToken
	}
	img, err := k.generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("Kandinsky: %w", err)
	}
	return img, nil
}

func (k Kandinsky) generate(ctx context.Context, prompt string) ([]byte, error) {
	// Новый API — pipelines; если его нет, старый — models/text2image.
	idField, runPath, statusPath := "pipeline_id", "/key/api/v1/pipeline/run", "/key/api/v1/pipeline/status/"
	var pipes []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	id := ""
	code, err := k.getJSON(ctx, "/key/api/v1/pipelines", &pipes)
	switch {
	case err == nil && len(pipes) > 0:
		id = pipes[0].ID
		for _, p := range pipes {
			if p.Type == "TEXT2IMAGE" {
				id = p.ID
				break
			}
		}
	case code == http.StatusNotFound:
		var models []struct {
			ID int `json:"id"`
		}
		if _, err := k.getJSON(ctx, "/key/api/v1/models", &models); err != nil {
			return nil, err
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("нет доступных моделей")
		}
		id = fmt.Sprint(models[0].ID)
		idField, runPath, statusPath = "model_id", "/key/api/v1/text2image/run", "/key/api/v1/text2image/status/"
	case err != nil:
		return nil, err
	default:
		return nil, fmt.Errorf("нет доступных моделей")
	}

	params, _ := json.Marshal(map[string]any{
		"type": "GENERATE", "numImages": 1, "width": 832, "height": 1024,
		"generateParams": map[string]string{"query": prompt},
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(idField, id)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="params"`)
	h.Set("Content-Type", "application/json")
	pw, _ := mw.CreatePart(h)
	_, _ = pw.Write(params)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(k.URL, "/")+runPath, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var run struct {
		UUID         string `json:"uuid"`
		Status       string `json:"status"`
		ModelStatus  string `json:"model_status"`
		ErrorDetails string `json:"errorDescription"`
	}
	if _, err := k.do(req, &run); err != nil {
		return nil, err
	}
	if run.UUID == "" {
		return nil, fmt.Errorf("сервис не принял задачу: %s %s", run.Status, run.ModelStatus)
	}

	poll := k.Poll
	if poll <= 0 {
		poll = 3 * time.Second
	}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
		var st struct {
			Status   string   `json:"status"`
			Images   []string `json:"images"`
			Censored bool     `json:"censored"`
			Error    string   `json:"errorDescription"`
			Result   struct {
				Files    []string `json:"files"`
				Censored bool     `json:"censored"`
			} `json:"result"`
		}
		if _, err := k.getJSON(ctx, statusPath+run.UUID, &st); err != nil {
			return nil, err
		}
		switch st.Status {
		case "DONE":
			files := st.Result.Files
			if len(files) == 0 {
				files = st.Images
			}
			if st.Censored || st.Result.Censored {
				return nil, fmt.Errorf("картинка отклонена цензурой — поменяйте описание")
			}
			if len(files) == 0 {
				return nil, fmt.Errorf("пустой ответ")
			}
			return base64.StdEncoding.DecodeString(files[0])
		case "FAIL":
			return nil, fmt.Errorf("генерация не удалась: %s", st.Error)
		}
	}
	return nil, fmt.Errorf("не дождался картинки за 4 минуты")
}

func (k Kandinsky) getJSON(ctx context.Context, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(k.URL, "/")+path, nil)
	if err != nil {
		return 0, err
	}
	return k.do(req, out)
}

func (k Kandinsky) do(req *http.Request, out any) (int, error) {
	req.Header.Set("X-Key", "Key "+k.Key)
	req.Header.Set("X-Secret", "Secret "+k.Secret)
	hc := k.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 30<<20))
	if resp.StatusCode/100 != 2 {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return resp.StatusCode, fmt.Errorf("%s: %s", resp.Status, msg)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("ответ: %w", err)
	}
	return resp.StatusCode, nil
}
