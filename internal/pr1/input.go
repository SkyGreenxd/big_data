package pr1

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type lineReader struct {
	in      *bufio.Reader
	out     io.Writer
	file    *os.File
	stty    string
	modeRaw bool
}

func newLineReader(in io.Reader, out io.Writer) *lineReader {
	r := &lineReader{in: bufio.NewReader(in), out: out}
	file, ok := in.(*os.File)
	if !ok || out != os.Stdout {
		return r
	}
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = file
	state, err := cmd.Output()
	if err != nil {
		return r
	}
	cmd = exec.Command(
		"stty",     // Настроить терминал
		"-echo",    // Не показывать ввод автоматически
		"-icanon",  // Читать клавиши без ожидания Enter
		"-isig",    // Передавать управляющие клавиши как ввод
		"min", "1", // Ждать хотя бы один байт
		"time", "0", // Не использовать таймер ожидания
	)
	cmd.Stdin = file
	if err := cmd.Run(); err != nil {
		return r
	}
	r.file = file
	r.stty = strings.TrimSpace(string(state))
	r.modeRaw = true
	return r
}

func (r *lineReader) close() {
	if !r.modeRaw {
		return
	}
	cmd := exec.Command("stty", r.stty)
	cmd.Stdin = r.file
	_ = cmd.Run()
}

func (r *lineReader) read(prompt string, history []string) (string, error) {
	fmt.Fprint(r.out, prompt)
	if !r.modeRaw {
		line, err := r.in.ReadString('\n')
		if errors.Is(err, io.EOF) && line != "" {
			return strings.TrimSuffix(line, "\r"), nil
		}
		return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), err
	}
	return r.readKeys(prompt, history)
}

func (r *lineReader) readKeys(prompt string, history []string) (string, error) {
	var line, draft string
	position := len(history)
	redraw := func() {
		fmt.Fprintf(r.out, "\r\x1b[2K%s%s", prompt, line) // to-start, clear, print
	}
	for {
		key, _, err := r.in.ReadRune()
		if err != nil {
			return "", err
		}
		switch key {
		case '\r', '\n':
			fmt.Fprintln(r.out)
			return line, nil
		case 3: // Ctrl-C
			return "", io.EOF
		case 127, '\b': // del last rune
			if line != "" {
				runes := []rune(line)
				line = string(runes[:len(runes)-1])
				redraw()
			}
		case 27: // escape for -> <-
			next, _, err := r.in.ReadRune()
			if err != nil {
				return "", err
			}
			if next != '[' {
				continue
			}
			arrow, _, err := r.in.ReadRune()
			if err != nil {
				return "", err
			}
			switch arrow {
			case 'A': // up
				if position > 0 {
					if position == len(history) {
						draft = line
					}
					position--
					line = history[position]
					redraw()
				}
			case 'B': // down
				if position < len(history) {
					position++
					if position == len(history) {
						line = draft
					} else {
						line = history[position]
					}
					redraw()
				}
			}
		default:
			if key >= ' ' && len(line)+len(string(key)) <= commandLimit {
				line += string(key)
				fmt.Fprint(r.out, string(key))
			}
		}
	}
}
