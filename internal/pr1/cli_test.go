package pr1

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SkyGreenxd/bigdata/pkg/webhdfs"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stalledBody struct {
	ctx     context.Context
	started chan struct{}
}

func (b *stalledBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *stalledBody) Close() error { return nil }

func TestCancelDownloadRemovesPartialFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	transport := testTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(io.MultiReader(strings.NewReader("partial"), &stalledBody{
				ctx: req.Context(), started: started,
			})),
		}, nil
	})
	client, err := webhdfs.NewWithBaseURL("http://namenode:9870", "test", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := shell{client: client, localDir: dir, hdfsDir: "/", in: strings.NewReader("get target\n"), out: io.Discard, errOut: io.Discard}
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop download")
	}
	if _, err := os.Stat(filepath.Join(dir, "target")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file was not removed: %v", err)
	}
}

func TestUploadSourceValidation(t *testing.T) {
	for _, command := range []string{"put", "append"} {
		for _, source := range []string{"directory", "missing", "file"} {
			t.Run(command+"/"+source, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "directory"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "file"), []byte("contents"), 0o644); err != nil {
					t.Fatal(err)
				}
				calls := 0
				var uploaded string
				transport := testTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					response := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
					if calls == 1 {
						wantOp, wantMethod := "CREATE", http.MethodPut
						if command == "append" {
							wantOp, wantMethod = "APPEND", http.MethodPost
						}
						if req.URL.Query().Get("op") != wantOp || req.Method != wantMethod {
							t.Errorf("unexpected request: %s %s", req.Method, req.URL)
						}
						response.StatusCode = http.StatusTemporaryRedirect
						response.Header.Set("Location", "http://datanode:9864/upload")
						return response, nil
					}
					data, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					uploaded = string(data)
					response.StatusCode = http.StatusCreated
					return response, nil
				})
				client, err := webhdfs.NewWithBaseURL("http://namenode:9870", "test", &http.Client{Transport: transport})
				if err != nil {
					t.Fatal(err)
				}
				s := shell{client: client, localDir: dir, hdfsDir: "/"}
				args := []string{command, source}
				if command == "append" {
					args = append(args, "target")
				}
				err = s.execute(args)
				if source != "file" {
					if err == nil || calls != 0 {
						t.Fatalf("invalid source: error=%v, HTTP requests=%d", err, calls)
					}
					if source == "directory" && !strings.Contains(err.Error(), "not a regular file") {
						t.Fatalf("unexpected directory error: %v", err)
					}
					return
				}
				if err != nil || calls != 2 || uploaded != "contents" {
					t.Fatalf("regular file: error=%v, HTTP requests=%d, uploaded=%q", err, calls, uploaded)
				}
			})
		}
	}
}

func TestShellContinuesAfterOversizedCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	input := strings.Repeat("x", commandLimit+1) + "\nhelp\nexit\n"
	code := Run([]string{"localhost", "9870", "test"}, strings.NewReader(input), &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), fmt.Sprintf("command exceeds %d bytes", commandLimit)) {
		t.Fatalf("missing length error: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "put <local-file>") {
		t.Fatalf("help after oversized input was not executed: %s", out.String())
	}
}
