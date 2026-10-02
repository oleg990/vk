package vk

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadWallPhotoAndPost(t *testing.T) {
	var gotPost map[string]string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(10 << 20)
		switch {
		case strings.HasSuffix(r.URL.Path, "/photos.getWallUploadServer"):
			if r.Form.Get("access_token") != "user-token" || r.Form.Get("group_id") != "7" {
				t.Errorf("bad getWallUploadServer form: %v", r.Form)
			}
			fmt.Fprintf(w, `{"response":{"upload_url":"%s/upload"}}`, srv.URL)
		case r.URL.Path == "/upload":
			f, fh, err := r.FormFile("photo")
			if err != nil {
				t.Errorf("no photo field: %v", err)
			} else {
				head := make([]byte, 3)
				_, _ = f.Read(head)
				f.Close()
				if fh.Filename != "post.jpg" || fh.Header.Get("Content-Type") != "image/jpeg" || string(head) != "\xff\xd8\xff" {
					t.Errorf("upload not JPEG: %s %v % x", fh.Filename, fh.Header, head)
				}
			}
			fmt.Fprint(w, `{"server":11,"photo":"[{\"x\":1}]","hash":"h"}`)
		case strings.HasSuffix(r.URL.Path, "/photos.saveWallPhoto"):
			if r.Form.Get("server") != "11" || r.Form.Get("hash") != "h" {
				t.Errorf("bad save form: %v", r.Form)
			}
			fmt.Fprint(w, `{"response":[{"id":555,"owner_id":-7}]}`)
		case strings.HasSuffix(r.URL.Path, "/wall.post"):
			gotPost = map[string]string{}
			for k := range r.Form {
				gotPost[k] = r.Form.Get(k)
			}
			fmt.Fprint(w, `{"response":{"post_id":42}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New("group-token", 7, srv.URL+"/method/", nil).WithToken("user-token")
	ctx := context.Background()
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	att, err := c.UploadWallPhoto(ctx, 7, pngBuf.Bytes())
	if err != nil || att != "photo-7_555" {
		t.Fatalf("att=%q err=%v", att, err)
	}
	id, err := c.WallPost(ctx, 7, "привет", att, 1790000000)
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	want := map[string]string{"owner_id": "-7", "from_group": "1", "message": "привет", "attachments": "photo-7_555", "publish_date": "1790000000"}
	for k, v := range want {
		if gotPost[k] != v {
			t.Errorf("wall.post %s = %q, want %q", k, gotPost[k], v)
		}
	}
}

func TestAPIErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"error_code":15,"error_msg":"Access denied"}}`)
	}))
	defer srv.Close()
	_, err := New("t", 7, srv.URL+"/", nil).WallPost(context.Background(), 7, "x", "", 0)
	if err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestAttachmentWithAccessKey(t *testing.T) {
	att, err := attachmentOf([]savedPhoto{{ID: 5, OwnerID: -7, AccessKey: "abc"}})
	if err != nil || att != "photo-7_5_abc" {
		t.Fatalf("att=%q err=%v", att, err)
	}
}

func TestWallUploadRetriesEmptyPhoto(t *testing.T) {
	uploads := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(10 << 20)
		switch {
		case strings.HasSuffix(r.URL.Path, "/photos.getWallUploadServer"):
			fmt.Fprintf(w, `{"response":{"upload_url":"%s/upload"}}`, srv.URL)
		case r.URL.Path == "/upload":
			uploads++
			if uploads < 3 {
				fmt.Fprint(w, `{"server":1,"photo":"","hash":"h"}`)
				return
			}
			fmt.Fprint(w, `{"server":1,"photo":"[{}]","hash":"h"}`)
		case strings.HasSuffix(r.URL.Path, "/photos.saveWallPhoto"):
			fmt.Fprint(w, `{"response":[{"id":9,"owner_id":-7,"access_key":"k"}]}`)
		}
	}))
	defer srv.Close()
	c := New("g", 7, srv.URL+"/method/", nil).WithToken("u")
	c.retryWait = time.Millisecond
	att, err := c.UploadWallPhoto(context.Background(), 7, []byte("x"))
	if err != nil || att != "photo-7_9_k" || uploads != 3 {
		t.Fatalf("att=%q err=%v uploads=%d", att, err, uploads)
	}
}

func TestWallPostsAndGroupStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/wall.get"):
			if r.Form.Get("owner_id") != "-7" || r.Form.Get("access_token") != "user-token" {
				t.Errorf("bad wall.get form: %v", r.Form)
			}
			fmt.Fprint(w, `{"response":{"count":2,"items":[
				{"id":5,"date":1790000000,"text":"Пост","post_type":"post","views":{"count":120},"likes":{"count":4},"comments":{"count":1},"reposts":{"count":2}},
				{"id":6,"date":1790000100,"text":"Реклама","post_type":"reply"}]}}`)
		case strings.HasSuffix(r.URL.Path, "/stats.get"):
			if r.Form.Get("group_id") != "7" || r.Form.Get("interval") != "all" {
				t.Errorf("bad stats.get form: %v", r.Form)
			}
			fmt.Fprint(w, `{"response":[{"visitors":{"views":300,"visitors":90},"reach":{"reach":210},"activity":{"subscribed":6,"unsubscribed":1}}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New("user-token", 7, srv.URL, nil)
	posts, err := c.WallPosts(context.Background(), 7, 50)
	if err != nil || len(posts) != 1 || posts[0].Views != 120 || posts[0].Likes != 4 || posts[0].Reposts != 2 {
		t.Fatalf("posts = %+v, err %v", posts, err)
	}
	st, err := c.GroupStats(context.Background(), 7, time.Unix(1, 0), time.Unix(2, 0))
	if err != nil || st.Reach != 210 || st.Subscribed != 6 || st.Unsubscribed != 1 || st.Visitors != 90 {
		t.Fatalf("stats = %+v, err %v", st, err)
	}
}
