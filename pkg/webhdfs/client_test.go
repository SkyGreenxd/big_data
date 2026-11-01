package webhdfs

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise http.Client redirect handling without opening listening sockets.
type handlerTransport struct{ handler http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	response := w.Result()
	response.Request = r
	return response, nil
}

func TestWebHDFSRedirectedReadAndUpload(t *testing.T) {
	var uploaded string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "localhost:9864" {
			if r.URL.Path != "/webhdfs/v1/shop/заказы.jsonl" {
				t.Errorf("unexpected path %q", r.URL.Path)
			}
			if r.URL.Query().Get("token") != "preserved" {
				t.Error("redirect query lost")
			}
			switch r.Method {
			case http.MethodGet:
				fmt.Fprint(w, "order data")
			case http.MethodPut:
				body, _ := io.ReadAll(r.Body)
				uploaded = string(body)
				w.WriteHeader(http.StatusCreated)
			default:
				t.Errorf("unexpected method %s", r.Method)
			}
			return
		}
		if r.URL.Host != "namenode:9870" {
			t.Fatalf("unrewritten or unexpected host: %s", r.URL.Host)
		}
		if r.URL.Query().Get("user.name") != "student" {
			t.Error("missing user")
		}
		switch r.URL.Query().Get("op") {
		case "OPEN", "CREATE":
			http.Redirect(w, r, "http://unresolvable-datanode:9864"+r.URL.EscapedPath()+"?token=preserved", http.StatusTemporaryRedirect)
		case "LISTSTATUS":
			fmt.Fprint(w, `{"FileStatuses":{"FileStatus":[{"pathSuffix":"part-00000","type":"FILE","length":10}]}}`)
		default:
			t.Errorf("unexpected operation")
		}
	})
	client, err := NewWithEndpoints("http://namenode:9870", "http://localhost:9864", "student")
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = handlerTransport{handler}
	var result strings.Builder
	if err := client.Open("/shop/заказы.jsonl", &result); err != nil {
		t.Fatal(err)
	}
	if result.String() != "order data" {
		t.Fatal("wrong downloaded data")
	}
	if err := client.Create("/shop/заказы.jsonl", strings.NewReader("new orders")); err != nil {
		t.Fatal(err)
	}
	if uploaded != "new orders" {
		t.Fatalf("upload: %q", uploaded)
	}
	files, err := client.List("/shop")
	if err != nil || len(files) != 1 || files[0].PathSuffix != "part-00000" {
		t.Fatalf("list: %v %v", files, err)
	}
}

func TestRemoteErrors(t *testing.T) {
	transport := handlerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"RemoteException":{"exception":"FileNotFoundException","message":"missing result"}}`)
	})}
	client, err := NewWithBaseURL("http://namenode:9870", "student", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.OpenReader("/missing"); err == nil || !strings.Contains(err.Error(), "missing result") {
		t.Fatalf("unexpected error: %v", err)
	}
}
