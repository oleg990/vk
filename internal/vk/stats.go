package vk

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// WallItem — пост со стены сообщества с цифрами (для аналитики).
type WallItem struct {
	ID       int64
	Date     int64 // unixtime публикации
	Text     string
	Views    int
	Likes    int
	Comments int
	Reposts  int
}

// WallPosts возвращает последние count (до 100) опубликованных постов сообщества.
// Просмотры видны только с ключом администратора (VK_USER_TOKEN).
func (c *Client) WallPosts(ctx context.Context, groupID int64, count int) ([]WallItem, error) {
	if count <= 0 || count > 100 {
		count = 100
	}
	p := url.Values{}
	p.Set("owner_id", strconv.FormatInt(-groupID, 10))
	p.Set("filter", "owner")
	p.Set("count", strconv.Itoa(count))
	var res struct {
		Items []struct {
			ID       int64  `json:"id"`
			Date     int64  `json:"date"`
			Text     string `json:"text"`
			PostType string `json:"post_type"`
			Views    struct {
				Count int `json:"count"`
			} `json:"views"`
			Likes struct {
				Count int `json:"count"`
			} `json:"likes"`
			Comments struct {
				Count int `json:"count"`
			} `json:"comments"`
			Reposts struct {
				Count int `json:"count"`
			} `json:"reposts"`
		} `json:"items"`
	}
	if err := c.call(ctx, "wall.get", p, &res); err != nil {
		return nil, err
	}
	out := make([]WallItem, 0, len(res.Items))
	for _, it := range res.Items {
		if it.PostType != "" && it.PostType != "post" {
			continue
		}
		out = append(out, WallItem{
			ID: it.ID, Date: it.Date, Text: it.Text,
			Views: it.Views.Count, Likes: it.Likes.Count, Comments: it.Comments.Count, Reposts: it.Reposts.Count,
		})
	}
	return out, nil
}

// StatPeriod — итоги статистики сообщества за период.
type StatPeriod struct {
	Views        int // просмотры страницы
	Visitors     int // посетители
	Reach        int // охват
	Subscribed   int // подписались
	Unsubscribed int // отписались
}

// GroupStats — статистика сообщества за период [from, to] одной суммой (stats.get, interval=all).
// Нужен ключ администратора с правом stats; без него VK вернёт ошибку доступа.
func (c *Client) GroupStats(ctx context.Context, groupID int64, from, to time.Time) (StatPeriod, error) {
	p := url.Values{}
	p.Set("group_id", strconv.FormatInt(groupID, 10))
	p.Set("timestamp_from", strconv.FormatInt(from.Unix(), 10))
	p.Set("timestamp_to", strconv.FormatInt(to.Unix(), 10))
	p.Set("interval", "all")
	var res []struct {
		Visitors struct {
			Views    int `json:"views"`
			Visitors int `json:"visitors"`
		} `json:"visitors"`
		Reach struct {
			Reach int `json:"reach"`
		} `json:"reach"`
		Activity struct {
			Subscribed   int `json:"subscribed"`
			Unsubscribed int `json:"unsubscribed"`
		} `json:"activity"`
	}
	if err := c.call(ctx, "stats.get", p, &res); err != nil {
		return StatPeriod{}, err
	}
	var s StatPeriod
	for _, r := range res {
		s.Views += r.Visitors.Views
		s.Visitors += r.Visitors.Visitors
		s.Reach += r.Reach.Reach
		s.Subscribed += r.Activity.Subscribed
		s.Unsubscribed += r.Activity.Unsubscribed
	}
	return s, nil
}
