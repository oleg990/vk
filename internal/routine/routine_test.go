package routine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/v1/claude_code/routines/trig_1/fire" || r.Header.Get("Authorization") != "Bearer tok" ||
			r.Header.Get("anthropic-version") != "2023-06-01" || b["text"] != "про ДСК" {
			t.Errorf("bad request %s %v %v", r.URL.Path, r.Header, b)
		}
		fmt.Fprint(w, `{"type":"routine_fire","claude_code_session_url":"https://claude.ai/code/session_1"}`)
	}))
	defer srv.Close()
	u, err := Client{URL: srv.URL, ID: "trig_1", Token: "tok"}.Fire(context.Background(), "про ДСК")
	if err != nil || u != "https://claude.ai/code/session_1" {
		t.Fatalf("u=%q err=%v", u, err)
	}
	if _, err := (Client{}).Fire(context.Background(), ""); err == nil {
		t.Fatal("must fail without token")
	}
}
