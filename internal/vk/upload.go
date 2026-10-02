package vk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
)

// WithToken — копия клиента с другим ключом (например, пользовательским для публикации на стене).
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// SendAttachment — сообщение с вложением (например, photo123_456) и клавиатурой.
func (c *Client) SendAttachment(ctx context.Context, peerID int64, text, attachment string, kb *Keyboard) error {
	p := url.Values{}
	p.Set("peer_id", strconv.FormatInt(peerID, 10))
	p.Set("message", text)
	p.Set("random_id", strconv.FormatInt(int64(randID()), 10))
	p.Set("dont_parse_links", "1")
	if attachment != "" {
		p.Set("attachment", attachment)
	}
	if kb != nil {
		raw, err := json.Marshal(kb)
		if err != nil {
			return err
		}
		p.Set("keyboard", string(raw))
	}
	return c.call(ctx, "messages.send", p, nil)
}

type uploadResult struct {
	Server int    `json:"server"`
	Photo  string `json:"photo"`
	Hash   string `json:"hash"`
}

// toJPEG перекодирует PNG в JPEG: сервер загрузки VK надёжнее принимает JPEG
// (на некоторые PNG он отвечает пустым полем photo).
func toJPEG(img []byte) []byte {
	if !bytes.HasPrefix(img, []byte("\x89PNG")) {
		return img
	}
	src, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		return img
	}
	// JPEG без прозрачности: кладём на белый
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, rgba, &jpeg.Options{Quality: 92}); err != nil {
		return img
	}
	return out.Bytes()
}

// uploadFile отправляет картинку на адрес загрузки VK (поле photo, JPEG).
func (c *Client) uploadFile(ctx context.Context, uploadURL string, img []byte) (uploadResult, error) {
	img = toJPEG(img)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="photo"; filename="post.jpg"`)
	h.Set("Content-Type", "image/jpeg")
	fw, err := w.CreatePart(h)
	if err != nil {
		return uploadResult{}, err
	}
	if _, err := fw.Write(img); err != nil {
		return uploadResult{}, err
	}
	if err := w.Close(); err != nil {
		return uploadResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return uploadResult{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.api.Do(req)
	if err != nil {
		return uploadResult{}, fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var res uploadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return uploadResult{}, fmt.Errorf("upload decode: %w", err)
	}
	if res.Photo == "" || res.Photo == "[]" {
		host := uploadURL
		if u, err := url.Parse(uploadURL); err == nil {
			host = u.Host
		}
		return uploadResult{}, fmt.Errorf("upload: пустой ответ VK (%s, %d байт): %s", host, len(img), truncate(string(raw), 200))
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type savedPhoto struct {
	ID        int64  `json:"id"`
	OwnerID   int64  `json:"owner_id"`
	AccessKey string `json:"access_key"`
}

func attachmentOf(ph []savedPhoto) (string, error) {
	if len(ph) == 0 {
		return "", fmt.Errorf("VK не вернул сохранённое фото")
	}
	att := fmt.Sprintf("photo%d_%d", ph[0].OwnerID, ph[0].ID)
	if ph[0].AccessKey != "" {
		// фото ещё не опубликовано на стене — без ключа доступа VK не покажет его в сообщении
		att += "_" + ph[0].AccessKey
	}
	return att, nil
}

// UploadMessagePhoto загружает фото для личного сообщения (работает с ключом группы).
func (c *Client) UploadMessagePhoto(ctx context.Context, peerID int64, png []byte) (string, error) {
	p := url.Values{}
	p.Set("peer_id", strconv.FormatInt(peerID, 10))
	var srv struct {
		UploadURL string `json:"upload_url"`
	}
	if err := c.call(ctx, "photos.getMessagesUploadServer", p, &srv); err != nil {
		return "", err
	}
	up, err := c.uploadFile(ctx, srv.UploadURL, png)
	if err != nil {
		return "", err
	}
	s := url.Values{}
	s.Set("server", strconv.Itoa(up.Server))
	s.Set("photo", up.Photo)
	s.Set("hash", up.Hash)
	var saved []savedPhoto
	if err := c.call(ctx, "photos.saveMessagesPhoto", s, &saved); err != nil {
		return "", err
	}
	return attachmentOf(saved)
}

// UploadWallPhoto загружает фото для поста на стене группы (нужен пользовательский ключ с правом photos).
func (c *Client) UploadWallPhoto(ctx context.Context, groupID int64, png []byte) (string, error) {
	p := url.Values{}
	p.Set("group_id", strconv.FormatInt(groupID, 10))
	var srv struct {
		UploadURL string `json:"upload_url"`
	}
	if err := c.call(ctx, "photos.getWallUploadServer", p, &srv); err != nil {
		return "", err
	}
	up, err := c.uploadFile(ctx, srv.UploadURL, png)
	if err != nil {
		return "", err
	}
	s := url.Values{}
	s.Set("group_id", strconv.FormatInt(groupID, 10))
	s.Set("server", strconv.Itoa(up.Server))
	s.Set("photo", up.Photo)
	s.Set("hash", up.Hash)
	var saved []savedPhoto
	if err := c.call(ctx, "photos.saveWallPhoto", s, &saved); err != nil {
		return "", err
	}
	return attachmentOf(saved)
}

// WallPost публикует пост от имени группы. publishDate > 0 — отложенная запись (VK опубликует сам).
func (c *Client) WallPost(ctx context.Context, groupID int64, message, attachments string, publishDate int64) (int64, error) {
	p := url.Values{}
	p.Set("owner_id", strconv.FormatInt(-groupID, 10))
	p.Set("from_group", "1")
	p.Set("message", message)
	if attachments != "" {
		p.Set("attachments", attachments)
	}
	if publishDate > 0 {
		p.Set("publish_date", strconv.FormatInt(publishDate, 10))
	}
	var res struct {
		PostID int64 `json:"post_id"`
	}
	if err := c.call(ctx, "wall.post", p, &res); err != nil {
		return 0, err
	}
	return res.PostID, nil
}
