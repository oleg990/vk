package vk

import "encoding/json"

// MessageNew — объект события message_new.
type MessageNew struct {
	Message Message `json:"message"`
}

type Message struct {
	ID          int64        `json:"id"`
	Date        int64        `json:"date"`
	PeerID      int64        `json:"peer_id"`
	FromID      int64        `json:"from_id"`
	Text        string       `json:"text"`
	Payload     string       `json:"payload"`
	Attachments []Attachment `json:"attachments"`
}

type Attachment struct {
	Type  string `json:"type"`
	Photo *Photo `json:"photo"`
}

type Photo struct {
	Sizes []PhotoSize `json:"sizes"`
}

type PhotoSize struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// BestURL — ссылка на самый большой размер фото.
func (p *Photo) BestURL() string {
	best, area := "", -1
	for _, s := range p.Sizes {
		if a := s.Width * s.Height; a > area && s.URL != "" {
			best, area = s.URL, a
		}
	}
	return best
}

// PhotoURLs — ссылки на все фото во вложениях.
func (m Message) PhotoURLs() []string {
	var out []string
	for _, a := range m.Attachments {
		if a.Type == "photo" && a.Photo != nil {
			if u := a.Photo.BestURL(); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

// PayloadMap разбирает payload кнопки ({"cmd":"buy"} или {"command":"start"}).
func (m Message) PayloadMap() map[string]string {
	out := map[string]string{}
	if m.Payload == "" {
		return out
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(m.Payload), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// Keyboard — клавиатура бота VK.
type Keyboard struct {
	OneTime bool       `json:"one_time"`
	Inline  bool       `json:"inline,omitempty"`
	Buttons [][]Button `json:"buttons"`
}

type Button struct {
	Action Action `json:"action"`
	Color  string `json:"color,omitempty"`
}

type Action struct {
	Type    string `json:"type"`
	Label   string `json:"label,omitempty"`
	Payload string `json:"payload,omitempty"`
}

// Цвета кнопок VK.
const (
	ColorPrimary   = "primary"
	ColorSecondary = "secondary"
	ColorPositive  = "positive"
	ColorNegative  = "negative"
)

// MaxLabel — VK обрезает подпись кнопки до 40 символов.
const MaxLabel = 40

// TextButton — обычная текстовая кнопка. payload — JSON-строка или пусто.
func TextButton(label, payload, color string) Button {
	r := []rune(label)
	if len(r) > MaxLabel {
		label = string(r[:MaxLabel])
	}
	return Button{Action: Action{Type: "text", Label: label, Payload: payload}, Color: color}
}
