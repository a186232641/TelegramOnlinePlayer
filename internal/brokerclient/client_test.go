package brokerclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientAgainstFakeBroker(t *testing.T) {
	const token = "shared-token"
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		switch r.URL.Path {
		case "/tg/export":
			if r.URL.Query().Get("channel_id") != "100" || r.URL.Query().Get("since") != "5" {
				t.Errorf("export 参数不符: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"MessageID": 6, "FileName": "a-2024-01-01 00:00:00.mp4", "FileSize": 123,
					"UploadedAt": time.Unix(1_700_000_000, 0).UTC()},
			})
		case "/tg/range":
			_, _ = w.Write([]byte("RANGEDATA"))
		case "/tg/send-code":
			_ = json.NewEncoder(w).Encode(map[string]string{"step_token": "step-1"})
		case "/tg/sign-in":
			_ = json.NewEncoder(w).Encode(map[string]bool{"need_password": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, token, srv.Client())
	ctx := context.Background()

	msgs, err := c.ExportHistory(ctx, 100, 5)
	if err != nil {
		t.Fatalf("ExportHistory: %v", err)
	}
	if len(msgs) != 1 || msgs[0].MessageID != 6 || msgs[0].FileName != "a-2024-01-01 00:00:00.mp4" {
		t.Fatalf("export 结果不符: %+v", msgs)
	}

	data, err := c.ReadRange(ctx, 100, 6, 0, 9)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if string(data) != "RANGEDATA" {
		t.Fatalf("range 数据不符: %q", data)
	}

	tok, err := c.SendCode(ctx, "+8613800000000")
	if err != nil || tok != "step-1" {
		t.Fatalf("SendCode: %q %v", tok, err)
	}

	needPwd, err := c.SignIn(ctx, "step-1", "12345")
	if err != nil || !needPwd {
		t.Fatalf("SignIn: needPwd=%v err=%v", needPwd, err)
	}
}

func TestClientUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer srv.Close()

	c := New(srv.URL, "wrong", srv.Client())
	if _, err := c.ExportHistory(context.Background(), 1, 0); err == nil {
		t.Fatal("应返回鉴权错误")
	}
}
