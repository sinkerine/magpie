//go:build darwin || linux

// Package claudetui runs Claude Code's interactive mode for magpie's Claude
// subscription bridge, as `magpie claude-tui-helper`: the bridge talks to
// it as it talks to `claude -p --input-format stream-json --output-format
// stream-json`, and it runs the real interactive Claude Code in a
// pseudo-terminal of its own. A user message is pasted into Claude Code's
// prompt and sent with Enter, as a person does; what Claude Code answers is
// read from the session's transcript, which Claude Code writes as it goes,
// and told back as stream-json, one whole message at a time.
//
// Anthropic counts a request by its entrypoint: the interactive mode's
// "cli", where -p's is "sdk-cli", and an sdk-cli request costs about
// twice as much of the plan's allowance (anthropics/claude-code#97074).
// The run is the real interactive Claude Code: nothing sets its
// entrypoint.
package claudetui

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yetone/magpie/internal/proc"
	"golang.org/x/sys/unix"
)

// how long Claude Code may take to show its prompt, a pasted message to
// show in it, and a sent one to reach its transcript
var (
	readyWait = 45 * time.Second
	pasteWait = 10 * time.Second
	sentWait  = 3 * time.Second
	// rows of one message are written together: a message is whole once
	// none of its rows came for this long, or anything else did
	messageQuiet = 150 * time.Millisecond
)

type shim struct {
	outMu sync.Mutex
	out   io.Writer

	pty *os.File
	cmd *exec.Cmd
	sid string
	// projects is Claude Code's projects folder, where it keeps the
	// session's transcript (<projects>/<folder>/<sid>.jsonl)
	projects string

	screenMu sync.Mutex
	screen   []byte
	changed  chan struct{}

	// rows ticks when a user message (or one queued mid-turn) reaches the
	// transcript: a pasted message was sent
	rowsMu sync.Mutex
	rows   int
	rowsCh chan struct{}
	// a turn is going: from a message's row to the turn's end
	busy bool

	exited chan struct{}
	exit   error
}

// Run runs Claude Code at path, args being the bridge's `claude -p` ones,
// with stream-json on standard input and output: interactive, with the
// same model, tools and settings (interactiveArgs). One that doesn't come
// up becomes the -p run it stands for (printMode), reading the input
// still unread.
func Run(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: magpie " + Command + " <claude> [claude -p's arguments]")
	}
	path, pargs := args[0], args[1:]
	if since, ok := brokenSince(); ok {
		fmt.Fprintf(os.Stderr, "claude-tui: Claude Code's interactive mode failed to start %s ago; this run is -p\n", time.Since(since).Round(time.Second))
		return printMode(path, pargs)
	}
	args = interactiveArgs(pargs)
	s := &shim{out: os.Stdout, changed: make(chan struct{}, 1), rowsCh: make(chan struct{}, 1), exited: make(chan struct{})}
	resume := ""
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) {
			resume = args[i+1]
		}
	}
	if resume != "" {
		s.sid = resume
	} else {
		s.sid = newUUID()
		args = append(args, "--session-id", s.sid)
	}
	s.projects = filepath.Join(configDir(), "projects")
	// a resumed session's file has its earlier turns: only what is
	// written after the start is read
	var from int64
	if p := s.transcript(); p != "" {
		if fi, err := os.Stat(p); err == nil {
			from = fi.Size()
		}
	}
	if err := s.start(path, args); err != nil {
		markBroken()
		fmt.Fprintf(os.Stderr, "claude-tui: Claude Code's interactive mode could not start (%v); this run is -p\n", err)
		return printMode(path, pargs)
	}
	defer s.stop()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sigs
		s.stop()
		os.Exit(1)
	}()
	if err := s.ready(); err != nil {
		if !errors.Is(err, errUntrusted) {
			markBroken()
		}
		signal.Reset()
		s.stop()
		fmt.Fprintf(os.Stderr, "claude-tui: %v; this run is -p\n", err)
		return printMode(path, pargs)
	}
	s.emit(map[string]any{"type": "system", "subtype": "init", "session_id": s.sid})
	go s.tail(from)
	return s.input(os.Stdin)
}

// configDir is the folder Claude Code keeps its state in.
func configDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// childEnv is the environment for Claude Code: the shim's, without what a
// Claude Code above it marks its own children with (one started from a
// Claude Code session keeps no transcript, "inherited
// CLAUDE_CODE_CHILD_SESSION marker"), in a terminal it can draw in.
func childEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch {
		case k == "CLAUDECODE", k == "CLAUDE_CODE_ENTRYPOINT", k == "CLAUDE_CODE_CHILD_SESSION",
			k == "CLAUDE_CODE_SESSION_ID", k == "CLAUDE_CODE_SESSION_ATTENDED", k == "CLAUDE_CODE_EXECPATH",
			k == "CLAUDE_PID", k == "CLAUDE_EFFORT", k == "TERM", k == "COLUMNS", k == "LINES",
			strings.HasPrefix(k, "CLAUDE_CODE_MESSAGING_"):
			continue
		}
		env = append(env, e)
	}
	return append(env, "TERM=xterm-256color")
}

func (s *shim) start(path string, args []string) error {
	m, name, err := openPTY()
	if err != nil {
		return err
	}
	tty, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return err
	}
	// wide, so a line of Claude Code's screen is a line
	_ = unix.IoctlSetWinsize(int(tty.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 50, Col: 200})
	cmd := proc.Command(path, args...)
	cmd.Env = childEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// its own session, the terminal its controlling one: the shim ending
	// closes the terminal, and Claude Code is hung up on with it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		m.Close()
		tty.Close()
		return err
	}
	tty.Close()
	s.pty, s.cmd = m, cmd
	go func() {
		err := cmd.Wait()
		s.exit = err
		close(s.exited)
	}()
	go s.read()
	return nil
}

// stop ends Claude Code: asked, then made to.
func (s *shim) stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	select {
	case <-s.exited:
		return
	default:
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// read keeps the tail of what Claude Code draws, and answers what it asks
// of the terminal: where the cursor is, and what terminal it is.
func (s *shim) read() {
	buf := make([]byte, 64<<10)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			d := buf[:n]
			if bytes.Contains(d, []byte("\x1b[6n")) {
				_, _ = s.pty.Write([]byte("\x1b[1;1R"))
			}
			if bytes.Contains(d, []byte("\x1b[c")) || bytes.Contains(d, []byte("\x1b[0c")) {
				_, _ = s.pty.Write([]byte("\x1b[?62;c"))
			}
			s.screenMu.Lock()
			s.screen = append(s.screen, d...)
			if len(s.screen) > 256<<10 {
				s.screen = append([]byte(nil), s.screen[len(s.screen)-128<<10:]...)
			}
			s.screenMu.Unlock()
			select {
			case s.changed <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[P^_][^\x1b]*\x1b\\|\x1b.`)

// plain is what Claude Code drew since the screen was last cleared
// (clearScreen), its escapes taken out.
func (s *shim) plain() string {
	s.screenMu.Lock()
	b := string(s.screen)
	s.screenMu.Unlock()
	return ansi.ReplaceAllString(b, " ")
}

func (s *shim) clearScreen() {
	s.screenMu.Lock()
	s.screen = s.screen[:0]
	s.screenMu.Unlock()
}

// lastScreen is the end of what Claude Code drew, for an error to say.
func (s *shim) lastScreen() string {
	p := strings.Join(strings.Fields(s.plain()), " ")
	if len(p) > 600 {
		p = p[len(p)-600:]
	}
	return p
}

var (
	trustNo  = regexp.MustCompile(`❯\s*No,\s*exit`)
	trustYes = regexp.MustCompile(`❯\s*Yes,\s*I trust`)
)

var errUntrusted = errors.New("Claude Code asks whether to trust the folder, which only its user answers")

// ready waits for Claude Code's prompt. Asked whether to trust the folder
// it runs in, it is told yes, as a person would, when TrustEnv says so.
func (s *shim) ready() error {
	deadline := time.After(readyWait)
	answered := 0
	for {
		p := s.plain()
		switch {
		case strings.Contains(p, "trust this folder") && os.Getenv(TrustEnv) != "1":
			return errUntrusted
		case strings.Contains(p, "trust this folder") && answered < 3:
			keys := ""
			switch {
			case trustNo.MatchString(p):
				keys = "\x1b[B"
			case trustYes.MatchString(p):
			default:
				goto wait
			}
			answered++
			s.clearScreen()
			if keys != "" {
				_, _ = s.pty.WriteString(keys)
				time.Sleep(300 * time.Millisecond)
			}
			_, _ = s.pty.WriteString("\r")
		case strings.Contains(p, "keep browser tools off"):
			// Claude in Chrome found: kept off, as the bridge runs it
			// --no-chrome
			s.clearScreen()
			_, _ = s.pty.WriteString("\x1b")
		case strings.Contains(p, "Choose the text style"):
			s.clearScreen()
			_, _ = s.pty.WriteString("\r")
		case strings.Contains(p, "❯ "):
			// drawn: a moment for its input to be listened to
			time.Sleep(300 * time.Millisecond)
			return nil
		}
	wait:
		select {
		case <-s.changed:
		case <-time.After(200 * time.Millisecond):
		case <-s.exited:
			return fmt.Errorf("Claude Code ended before it showed its prompt (%v): %s", s.exit, s.lastScreen())
		case <-deadline:
			return fmt.Errorf("Claude Code showed no prompt in %s: %s", readyWait, s.lastScreen())
		}
	}
}

func (s *shim) emit(v any) {
	b, _ := json.Marshal(v)
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}

// input reads the bridge's stream-json: user messages, sent to Claude Code,
// and control requests, of which interactive mode takes none but initialize.
func (s *shim) input(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		var m struct {
			Type      string `json:"type"`
			UUID      string `json:"uuid"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "control_request":
			resp := map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{}}
			if m.Request.Subtype != "initialize" {
				resp = map[string]any{"subtype": "error", "request_id": m.RequestID,
					"error": "Claude Code's interactive mode takes no " + m.Request.Subtype + " request"}
			}
			s.emit(map[string]any{"type": "control_response", "response": resp})
		case "user":
			text, ok := userText(m.Message.Content)
			if !ok {
				s.emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": s.sid,
					"result": "an image can't be pasted into Claude Code's interactive mode"})
				continue
			}
			if err := s.send(text); err != nil {
				return err
			}
			if m.UUID != "" {
				s.emit(map[string]any{"type": "command_lifecycle", "command_uuid": m.UUID, "state": "queued"})
			}
		}
	}
	return sc.Err()
}

// userText is a user message's text, its blocks one after another; false
// for one with anything but text.
func userText(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	var b strings.Builder
	for _, bl := range blocks {
		if bl.Type != "text" {
			return "", false
		}
		if bl.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(bl.Text)
	}
	return b.String(), true
}

// pasteable is text as a paste can carry it: no escape that would end the
// paste early, nor other control characters, its lines ended by \n; and
// not opening with what Claude Code's prompt takes as a command (/) or a
// shell line (!), a memory (#) or a background task (&). busy is for a
// message sent while a turn is going, which Claude Code queues.
func pasteable(text string, busy bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\n', r == '\t':
			return r
		case r < 0x20, r == 0x7f:
			return -1
		}
		return r
	}, text)
	// After a line break the prompt takes it as words: a space before it
	// is trimmed, and a zero-width one taken out of the paste. Claude Code
	// trims a queued message, though, and runs one opening with / as a
	// command even so, or drops it: that one is marked as the user's
	// words, as the bridge marks them after a tool's result.
	if t := strings.TrimLeft(text, " \t\n"); t != "" && strings.ContainsRune("/!#&", rune(t[0])) {
		if busy {
			text = userMark + t
		} else {
			text = "\n" + t
		}
	}
	return text
}

// send pastes text into Claude Code's prompt and presses Enter, again
// while the message hasn't reached the transcript: an Enter that lands
// while the paste is still being taken in is part of it.
func (s *shim) send(text string) error {
	s.rowsMu.Lock()
	before, busy := s.rows, s.busy
	s.rowsMu.Unlock()
	text = pasteable(text, busy)
	if strings.TrimSpace(text) == "" {
		text = "."
	}
	s.clearScreen()
	if _, err := s.pty.WriteString("\x1b[200~" + text + "\x1b[201~"); err != nil {
		return err
	}
	// shown in the prompt: its last words, or Claude Code's "[Pasted text"
	// for a long one
	tail := strings.TrimSpace(text)
	if i := strings.LastIndexByte(tail, '\n'); i >= 0 {
		tail = strings.TrimSpace(tail[i+1:])
	}
	if r := []rune(tail); len(r) > 16 {
		tail = string(r[len(r)-16:])
	}
	shownBy := time.After(pasteWait)
shown:
	for {
		p := s.plain()
		if strings.Contains(p, "Pasted text") || tail != "" && strings.Contains(strings.Join(strings.Fields(p), " "), strings.Join(strings.Fields(tail), " ")) {
			break
		}
		select {
		case <-s.changed:
		case <-time.After(100 * time.Millisecond):
		case <-shownBy:
			break shown
		case <-s.exited:
			return fmt.Errorf("Claude Code ended (%v): %s", s.exit, s.lastScreen())
		}
	}
	time.Sleep(150 * time.Millisecond)
	for try := 0; try < 4; try++ {
		if _, err := s.pty.WriteString("\r"); err != nil {
			return err
		}
		by := time.After(sentWait)
		for {
			s.rowsMu.Lock()
			sent := s.rows > before
			s.rowsMu.Unlock()
			if sent {
				return nil
			}
			select {
			case <-s.rowsCh:
				continue
			case <-by:
			case <-s.exited:
				return fmt.Errorf("Claude Code ended (%v): %s", s.exit, s.lastScreen())
			}
			break
		}
	}
	return fmt.Errorf("Claude Code did not take the message: %s", s.lastScreen())
}

// transcript is the session's transcript, "" before Claude Code writes it.
func (s *shim) transcript() string {
	m, _ := filepath.Glob(filepath.Join(s.projects, "*", s.sid+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// userMark is what the bridge marks the user's words with after a tool's
// result (renderParts).
const userMark = "[user message]\n"

func (s *shim) sawRow() {
	s.rowsMu.Lock()
	s.rows++
	s.busy = true
	s.rowsMu.Unlock()
	select {
	case s.rowsCh <- struct{}{}:
	default:
	}
}

// tail reads the transcript from offset from on, as Claude Code writes it,
// and tells the bridge what it says.
func (s *shim) tail(from int64) {
	var path string
	for path == "" {
		if path = s.transcript(); path == "" {
			select {
			case <-s.exited:
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return
	}
	t := newTranslator(s)
	rd := bufio.NewReaderSize(f, 1<<20)
	var part []byte
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			part = append(part, line...)
			if part[len(part)-1] == '\n' {
				t.row(part)
				part = nil
			}
			continue
		}
		if err != nil && err != io.EOF {
			return
		}
		t.quiet()
		select {
		case <-s.exited:
			// what it wrote last
			if line, _ := rd.ReadBytes('\n'); len(line) > 0 {
				t.row(append(part, line...))
			}
			t.flush()
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *shim) turnOver() {
	s.rowsMu.Lock()
	s.busy = false
	s.rowsMu.Unlock()
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// interactiveArgs are `claude -p` arguments made the interactive mode's:
// the same model, tools, MCP servers, settings, effort, system prompt and
// session, without what only -p takes; its permission prompts kept, and
// magpie's MCP tools (and the web search -p was given) allowed without
// one, where -p skipped them all.
func interactiveArgs(pargs []string) []string {
	var out []string
	allowed := []string{"mcp__magpie"}
	for i := 0; i < len(pargs); i++ {
		a := pargs[i]
		switch a {
		case "-p", "--print", "--verbose", "--include-partial-messages", "--dangerously-skip-permissions",
			"--no-session-persistence", "--replay-user-messages":
			continue
		case "--output-format", "--input-format", "--thinking-display", "--json-schema", "--permission-mode":
			i++
			continue
		case "--tools":
			if i+1 < len(pargs) {
				for _, t := range strings.FieldsFunc(pargs[i+1], func(r rune) bool { return r == ',' || r == ' ' }) {
					allowed = append(allowed, t)
				}
			}
		}
		out = append(out, a)
	}
	return append(out, "--permission-mode", "default", "--allowedTools", strings.Join(allowed, ","), "--no-chrome")
}

// printMode becomes Claude Code at path, run with the -p arguments pargs:
// the same process, so the bridge's input, output and process tree go on
// as they were.
func printMode(path string, pargs []string) error {
	return unix.Exec(path, append([]string{path}, pargs...), os.Environ())
}

// broken is where a failed start is kept: for brokenFor after it, runs go
// straight to -p, rather than each waiting out readyWait.
func broken() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "magpie", "claude-tui-broken")
}

const brokenFor = 30 * time.Minute

func brokenSince() (time.Time, bool) {
	p := broken()
	if p == "" {
		return time.Time{}, false
	}
	fi, err := os.Stat(p)
	if err != nil || time.Since(fi.ModTime()) > brokenFor {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

func markBroken() {
	p := broken()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, nil, 0o600)
	now := time.Now()
	_ = os.Chtimes(p, now, now)
}
