package autopost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestKandinskyPipeline(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Key") != "Key k" || r.Header.Get("X-Secret") != "Secret s" {
			t.Errorf("headers %v", r.Header)
		}
		switch r.URL.Path {
		case "/key/api/v1/pipelines":
			fmt.Fprint(w, `[{"id":"pipe-1","type":"TEXT2IMAGE"}]`)
		case "/key/api/v1/pipeline/run":
			if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("pipeline_id") != "pipe-1" {
				t.Errorf("run form: %v %v", err, r.MultipartForm)
			}
			var p map[string]any
			_ = json.Unmarshal([]byte(r.FormValue("params")), &p)
			if p["generateParams"].(map[string]any)["query"] != "room" {
				t.Errorf("params %v", p)
			}
			fmt.Fprint(w, `{"uuid":"u1","status":"INITIAL"}`)
		case "/key/api/v1/pipeline/status/u1":
			polls++
			if polls < 2 {
				fmt.Fprint(w, `{"status":"PROCESSING"}`)
				return
			}
			fmt.Fprintf(w, `{"status":"DONE","result":{"files":["%s"],"censored":false}}`, base64.StdEncoding.EncodeToString([]byte("img")))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	img, err := Kandinsky{URL: srv.URL, Key: "k", Secret: "s", HTTP: srv.Client(), Poll: time.Millisecond}.Generate(context.Background(), "room")
	if err != nil || string(img) != "img" {
		t.Fatalf("img=%q err=%v", img, err)
	}
}

func TestKandinskyLegacyModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/key/api/v1/pipelines":
			http.NotFound(w, r)
		case "/key/api/v1/models":
			fmt.Fprint(w, `[{"id":4}]`)
		case "/key/api/v1/text2image/run":
			_ = r.ParseMultipartForm(1 << 20)
			if r.FormValue("model_id") != "4" {
				t.Errorf("model_id %q", r.FormValue("model_id"))
			}
			fmt.Fprint(w, `{"uuid":"u2","status":"INITIAL"}`)
		case "/key/api/v1/text2image/status/u2":
			fmt.Fprintf(w, `{"status":"DONE","images":["%s"]}`, base64.StdEncoding.EncodeToString([]byte("old")))
		}
	}))
	defer srv.Close()
	img, err := Kandinsky{URL: srv.URL, Key: "k", Secret: "s", HTTP: srv.Client(), Poll: time.Millisecond}.Generate(context.Background(), "x")
	if err != nil || string(img) != "old" {
		t.Fatalf("img=%q err=%v", img, err)
	}
}
