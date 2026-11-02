package pr1

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type startedReader struct {
	io.Reader
	started chan struct{}
}

func (r startedReader) Read(p []byte) (int, error) {
	close(r.started)
	return r.Reader.Read(p)
}

func TestCancelWaitingForInput(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	started := make(chan struct{})
	r := newLineReader(startedReader{Reader: in, started: started}, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := r.readContext(ctx, "", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("input read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("input did not stop on cancellation")
	}
}

func TestRawCommandLength(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"at limit", strings.Repeat("a", commandLimit), strings.Repeat("a", commandLimit)},
		{"unicode at limit", strings.Repeat("я", commandLimit/2), strings.Repeat("я", commandLimit/2)},
		{"over limit", strings.Repeat("a", commandLimit+1), ""},
		{"unicode over limit", strings.Repeat("a", commandLimit-1) + "я", ""},
		{"delete with discarded argument", "delete victim" + strings.Repeat(" ", commandLimit-len("delete victim")) + " extra", ""},
		{"backspace after overflow", strings.Repeat("a", commandLimit+1) + "\b", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &lineReader{
				in:      bufio.NewReader(strings.NewReader(tc.input + "\nhelp\n")),
				out:     io.Discard,
				modeRaw: true,
			}
			got, err := r.read("", nil)
			if tc.want == "" {
				if !errors.Is(err, errCommandTooLong) || got != "" {
					t.Fatalf("oversized input: got %q, error=%v", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("valid input: got %d bytes, want %d, error=%v", len(got), len(tc.want), err)
			}
			got, err = r.read("", nil)
			if err != nil || got != "help" {
				t.Fatalf("next command: got %q, error=%v", got, err)
			}
		})
	}
}
