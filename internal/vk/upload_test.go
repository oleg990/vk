package vk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Errorf("no photo field: %v", err)
			} else {
				f.Close()
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
	att, err := c.UploadWallPhoto(ctx, 7, []byte("png"))
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
