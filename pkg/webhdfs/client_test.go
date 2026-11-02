package webhdfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type cancelTransport func(*http.Request) (*http.Response, error)

func (f cancelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCancellationAtNameNodeAndDataNode(t *testing.T) {
	for _, op := range []string{"MKDIRS", "CREATE", "APPEND", "OPEN"} {
		for _, stage := range []string{"namenode", "datanode"} {
			if op == "MKDIRS" && stage == "datanode" {
				continue
			}
			t.Run(op+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started := make(chan struct{})
				transport := cancelTransport(func(req *http.Request) (*http.Response, error) {
					if stage == "datanode" && req.URL.Host == "namenode:9870" {
						return &http.Response{
							StatusCode: http.StatusTemporaryRedirect,
							Header:     http.Header{"Location": {"http://datanode:9864/file"}},
							Body:       io.NopCloser(strings.NewReader("")),
						}, nil
					}
					close(started)
					<-req.Context().Done()
					return nil, req.Context().Err()
				})
				base, err := NewWithBaseURL("http://namenode:9870", "test", &http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				client := base.WithContext(ctx)
				done := make(chan error, 1)
				go func() {
					switch op {
					case "MKDIRS":
						done <- client.Mkdir("/dir")
					case "CREATE":
						done <- client.Create("/file", strings.NewReader("data"))
					case "APPEND":
						done <- client.Append("/file", strings.NewReader("data"))
					case "OPEN":
						done <- client.Open("/file", io.Discard)
					}
				}()
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("request did not reach target")
				}
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation, got %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("request ignored cancellation")
				}
				if base.ctx.Err() != nil {
					t.Fatal("canceling the copy also canceled the original client")
				}
			})
		}
	}
}

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
