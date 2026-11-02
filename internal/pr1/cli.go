package pr1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/SkyGreenxd/bigdata/pkg/webhdfs"
)

const (
	historyLimit = 100
	commandLimit = 4096
)

type shell struct {
	client   *webhdfs.Client
	hdfsDir  string
	localDir string
	in       io.Reader
	out      io.Writer
	errOut   io.Writer
}

type commandSpec struct {
	usage       string
	names       int
	allowParent bool
}

var commands = []commandSpec{
	{"help", 0, false},
	{"mkdir <dir>", 1, false},
	{"lmkdir <dir>", 1, false},
	{"put <local-file>", 1, false},
	{"get <hdfs-file>", 1, false},
	{"append <local-file> <hdfs-file>", 2, false},
	{"lappend <source-file> <target-file>", 2, false},
	{"cat <hdfs-file>", 1, false},
	{"lcat <local-file>", 1, false},
	{"delete <hdfs-file>", 1, false},
	{"ldelete <local-file>", 1, false},
	{"ls", 0, false},
	{"cd <dir|..>", 1, true},
	{"lls", 0, false},
	{"lcd <dir|..>", 1, true},
	{"exit", 0, false},
	{"quit", 0, false},
}

// Run starts the WebHDFS shell and returns a process exit code.
func Run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(errOut, "Usage: pr1 <server> <port> <user>")
		return 2
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintln(errOut, "error: port must be an integer")
		return 2
	}
	client, err := webhdfs.New(args[0], port, args[2])
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	localDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s := shell{client: client, hdfsDir: "/", localDir: localDir, in: in, out: out, errOut: errOut}
	if err := s.run(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(errOut, "interrupted")
			return 130
		}
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

func (s *shell) run(ctx context.Context) error {
	s.client = s.client.WithContext(ctx)
	fmt.Fprintln(s.out, "WebHDFS client. Type 'help' for commands, 'exit' to quit.")
	var history []string
	readLine := newLineReader(s.in, s.out)
	defer readLine.close()
	for {
		printcli := fmt.Sprintf("hdfs:%s local:%s> ", s.hdfsDir, s.localDir)
		line, err := readLine.readContext(ctx, printcli, history)
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(s.out)
			return nil
		}
		if errors.Is(err, errCommandTooLong) {
			fmt.Fprintln(s.errOut, "error:", err)
			continue
		}
		if err != nil {
			return err
		}
		if len(line) > commandLimit {
			fmt.Fprintf(s.errOut, "error: command exceeds %d bytes\n", commandLimit)
			continue
		}
		if strings.TrimSpace(line) != "" {
			history = append(history, line)
			if len(history) > historyLimit {
				history = history[1:]
			}
		}
		args, err := parseCommand(line)
		if err != nil {
			fmt.Fprintln(s.errOut, "error:", err)
			continue
		}
		if len(args) == 0 {
			continue
		}
		if args[0] == "exit" || args[0] == "quit" {
			return nil
		}
		err = s.execute(args)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			fmt.Fprintln(s.errOut, "error:", err)
		}
	}
}

func (s *shell) execute(args []string) error {
	if err := validateCommand(args); err != nil {
		return err
	}
	switch args[0] {
	case "help":
		for _, command := range commands {
			fmt.Fprintln(s.out, command.usage)
		}
		return nil
	case "mkdir":
		return s.client.Mkdir(path.Join(s.hdfsDir, args[1]))
	case "lmkdir":
		return os.Mkdir(filepath.Join(s.localDir, args[1]), 0o755) // x is required to access directory contents.
	case "put":
		file, err := openRegularFile(filepath.Join(s.localDir, args[1]))
		if err != nil {
			return err
		}
		defer file.Close()
		return s.client.Create(path.Join(s.hdfsDir, args[1]), file)
	case "get":
		return s.download(args[1])
	case "append":
		file, err := openRegularFile(filepath.Join(s.localDir, args[1]))
		if err != nil {
			return err
		}
		defer file.Close()
		return s.client.Append(path.Join(s.hdfsDir, args[2]), file)
	case "lappend":
		return s.appendLocal(args[1], args[2])
	case "cat":
		return s.client.Open(path.Join(s.hdfsDir, args[1]), s.out)
	case "lcat":
		file, err := os.Open(filepath.Join(s.localDir, args[1]))
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(s.out, file)
		return err
	case "delete":
		return s.client.Delete(path.Join(s.hdfsDir, args[1]))
	case "ldelete":
		target := filepath.Join(s.localDir, args[1])
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%q is not a regular file", args[1])
		}
		return os.Remove(target)
	case "ls":
		items, err := s.client.List(s.hdfsDir)
		if err != nil {
			return err
		}
		printHDFSList(s.out, items)
		return nil
	case "lls":
		return printLocalList(s.out, s.localDir)
	case "cd":
		return s.changeHDFSDir(args[1])
	case "lcd":
		return s.changeLocalDir(args[1])
	default:
		return fmt.Errorf("unknown command %q; type 'help'", args[0])
	}
}

func openRegularFile(name string) (*os.File, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("%q is not a regular file", name)
	}
	return file, nil
}

func (s *shell) appendLocal(sourceName, targetName string) error {
	source, err := os.Open(filepath.Join(s.localDir, sourceName))
	if err != nil {
		return err
	}
	defer source.Close()
	sourceInfo, err := source.Stat()
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", sourceName)
	}
	targetPath := filepath.Join(s.localDir, targetName)
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return err
	}
	if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", targetName)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return errors.New("source and target must be different files")
	}
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = io.Copy(target, source)
	return err
}

func (s *shell) download(name string) (err error) {
	target := filepath.Join(s.localDir, name)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		file.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if err := s.client.Open(path.Join(s.hdfsDir, name), file); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *shell) changeHDFSDir(name string) error {
	target := path.Join(s.hdfsDir, name)
	status, err := s.client.Status(target)
	if err != nil {
		return err
	}
	if status.Type != "DIRECTORY" {
		return fmt.Errorf("%q is not a directory", name)
	}
	s.hdfsDir = target
	return nil
}

func (s *shell) changeLocalDir(name string) error {
	target := filepath.Join(s.localDir, name)
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", name)
	}
	s.localDir = target
	return nil
}

func validateCommand(args []string) error {
	for _, command := range commands {
		if strings.Fields(command.usage)[0] != args[0] {
			continue
		}
		if len(args) != command.names+1 {
			return usage(command.usage)
		}
		for _, name := range args[1:] {
			if err := validName(name, command.allowParent); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown command %q; type 'help'", args[0])
}

func validName(name string, allowParent bool) error {
	if allowParent && name == ".." {
		return nil
	}
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return errors.New("name must be a single file or directory name without '/'")
	}
	return nil
}

func parseCommand(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	tokenStarted := false
	flush := func() {
		if tokenStarted {
			args = append(args, current.String())
			current.Reset()
			tokenStarted = false
		}
	}
	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			tokenStarted = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			tokenStarted = true
		} else if unicode.IsSpace(r) {
			flush()
		} else {
			current.WriteRune(r)
			tokenStarted = true
		}
	}
	if escaped {
		return nil, errors.New("unfinished escape at end of command")
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return args, nil
}

func usage(value string) error { return fmt.Errorf("usage: %s", value) }

func printHDFSList(out io.Writer, items []webhdfs.FileStatus) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].PathSuffix < items[j].PathSuffix
	})
	fmt.Fprintln(out, "Directories:")
	for _, item := range items {
		if item.Type == "DIRECTORY" {
			fmt.Fprintf(out, "  %s/\n", item.PathSuffix)
		}
	}
	fmt.Fprintln(out, "Files:")
	for _, item := range items {
		if item.Type != "DIRECTORY" {
			fmt.Fprintf(out, "  %s\t%d bytes\n", item.PathSuffix, item.Length)
		}
	}
}

func printLocalList(out io.Writer, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Directories:")
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Fprintf(out, "  %s/\n", entry.Name())
		}
	}
	fmt.Fprintln(out, "Files:")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s\t%d bytes\n", entry.Name(), info.Size())
	}
	return nil
}
