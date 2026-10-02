package autopost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHFFallsBackToSecondProvider(t *testing.T) {
	png := []byte("\x89PNG-fake")
	var falBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hf_x" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/fal-ai/fal-ai/flux/schnell":
			_ = json.NewDecoder(r.Body).Decode(&falBody)
			http.Error(w, `{"error":"no credits"}`, http.StatusPaymentRequired)
		case "/nscale/v1/images/generations":
			fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, base64.StdEncoding.EncodeToString(png))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	img, err := HF{URL: srv.URL, Providers: []string{"fal-ai", "nscale"}, Token: "hf_x", HTTP: srv.Client()}.Generate(context.Background(), "room")
	if err != nil || string(img) != string(png) {
		t.Fatalf("img=%q err=%v", img, err)
	}
	if falBody["prompt"] != "room" || falBody["sync_mode"] != true {
		t.Fatalf("fal body = %v", falBody)
	}
}

func TestHFFalDataURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"images":[{"url":"data:image/jpeg;base64,%s"}]}`, base64.StdEncoding.EncodeToString([]byte("jpg")))
	}))
	defer srv.Close()
	img, err := HF{URL: srv.URL, Providers: []string{"fal-ai"}, Token: "t", HTTP: srv.Client()}.Generate(context.Background(), "x")
	if err != nil || string(img) != "jpg" {
		t.Fatalf("img=%q err=%v", img, err)
	}
}
