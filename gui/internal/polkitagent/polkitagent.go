// Package polkitagent runs polkit's own text agent, pkttyagent, for
// archmaid-gui's helper calls, so their polkit prompt reaches the GUI (and
// its kept password, internal/driver's Deps.KeepPassword) instead of the
// desktop's agent.
//
// Before a helper step runs pkexec, Start registers pkttyagent for the
// GUI's own process (--process <pid>,<start time>): pkexec's subject is its
// caller, archmaid-gui, and polkitd prefers an agent registered for that
// process over the session's (polkit-gnome). Start returns once pkttyagent
// says it is registered (--notify-fd), so the registration always comes
// before pkexec's request. pkttyagent runs on a pty that this package owns
// (as its controlling terminal), and this package reads what it writes
// there: polkit's text listener's banner, identity menu and PAM prompt
// (polkitagenttextlistener.c, polkit 127):
//
//	==== AUTHENTICATING FOR <action id> ====
//	<message>
//	Authenticating as: <name> | <gecos> (<name>)
//	Password:                       (the PAM prompt, no newline)
//	==== AUTHENTICATION COMPLETE ==== | ==== AUTHENTICATION FAILED ====
//
// or, with several admin identities, "Multiple identities can be used for
// authentication:", " N.  <identity>" lines and "Choose identity to
// authenticate as (1-N): " instead of "Authenticating as".
//
// A prompt is passed on (Prompts, as a driver.AgentPrompt) only when it
// authenticates one of archmaid's helper actions (ActionPrefix) as the
// current user: the menu's choice is the current user's entry, or none.
// Anything else is cancelled, and no password is ever written for it: an
// invalid choice at its identity menu, on which the listener cancels the
// request and stays up for the next one, or an end of file at its password
// prompt. The listener reads with stdio, whose end-of-file flag is sticky,
// so after one every later read fails: that pkttyagent is stopped once the
// request has ended (Exited), and the caller registers a new one for a
// request of archmaid's own still to come. A second password prompt in one
// request (PAM wanting more than the password) is cancelled the same way,
// never left waiting. If a cancel can't be written, pkttyagent is stopped. The password, when the
// Driver answers, is written to the pty only once pkttyagent has turned
// echo off, and only while no other process holds its terminal.
package polkitagent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/proc"
)

// ActionPrefix starts every polkit action of archmaid's root helper.
const ActionPrefix = "org.archmaid.helper."

// Options configure Start.
type Options struct {
	// Path is pkttyagent's; "" looks it up on PATH.
	Path string
	// Pid and Start are the subject the agent registers for: the GUI's own
	// process and its start time (proc.Info.Start, clock ticks since
	// boot, as polkit reads it).
	Pid   int
	Start uint64
	// User is the current user's name: the only identity it answers for.
	User string
	// Register bounds the wait for the registration (default 5 s).
	Register time.Duration
}

// ErrRefused is a prompt's Err when the agent refused it (not archmaid's
// action, not the current user) or it was cancelled.
var ErrRefused = errors.New("polkit agent: request refused")

// Agent is one running pkttyagent.
type Agent struct {
	opts    Options
	cmd     *exec.Cmd
	master  *os.File
	prompts chan driver.AgentPrompt
	exited  chan struct{} // closed once pkttyagent has exited and is reaped
	stop    chan struct{} // closed by Stop
	once    sync.Once
	read    chan struct{} // closed once the reader has ended

	tty string // the terminal's path (/dev/pts/N)

	mu  sync.Mutex
	cur *Prompt // the prompt answered, awaiting polkit's verdict
}

// lockTTY makes the agent's terminal exclusive (TIOCEXCL): any new open of
// it, by name or as /dev/tty, fails with EBUSY for a process without
// CAP_SYS_ADMIN. The pty belongs to the user, so without this any process
// of the same uid (something an AUR build left running, say) could open
// /dev/pts/N and race pkttyagent for the password line written to it.
func lockTTY(slave *os.File) error { return ioctl(slave, syscall.TIOCEXCL, nil) }

// inAgentSession reports whether pid is in pkttyagent's session, whose
// controlling terminal is the agent's (pkttyagent leads it: Setsid).
func (a *Agent) inAgentSession(pid int) bool {
	info, err := proc.Read(pid)
	return err != nil || info.Session == a.cmd.Process.Pid // unreadable: assume so
}

// ErrTTYShared: a process other than pkttyagent holds the agent's
// terminal, so a password written to it could be read by that process.
var ErrTTYShared = errors.New("polkit agent: another process holds its terminal")

// otherHolders lists the processes, besides pkttyagent, that hold the
// agent's terminal open, as far as /proc shows (our uid's processes; root's
// fd tables are unreadable, and root is trusted). After lockTTY nobody can
// open it any more, so one that holds it now opened it before the lock and
// still does.
func (a *Agent) otherHolders() []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return []int{-1} // can't tell: assume the worst
	}
	var pids []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == a.cmd.Process.Pid {
			continue
		}
		fds, err := os.ReadDir("/proc/" + e.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			l, err := os.Readlink("/proc/" + e.Name() + "/fd/" + fd.Name())
			if err != nil {
				continue
			}
			// By name, or as /dev/tty from inside pkttyagent's session
			// (the terminal is that session's controlling terminal).
			if l == a.tty || l == "/dev/tty" && a.inAgentSession(pid) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// Start registers pkttyagent for opts' process and returns once it is
// registered. An error means there is no agent of ours (pkttyagent is
// missing, can't register, or took too long): polkit then asks through
// the desktop's agent, as before.
func Start(opts Options) (*Agent, error) {
	path := opts.Path
	if path == "" {
		var err error
		if path, err = exec.LookPath("pkttyagent"); err != nil {
			return nil, fmt.Errorf("pkttyagent isn't installed: %w", err)
		}
	}
	if opts.Register <= 0 {
		opts.Register = 5 * time.Second
	}
	if opts.Pid <= 0 || opts.Start == 0 || opts.User == "" {
		return nil, errors.New("polkit agent: no subject or user")
	}
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	notifyR, notifyW, err := os.Pipe()
	if err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	cmd := exec.Command(path, "--process", fmt.Sprintf("%d,%d", opts.Pid, opts.Start), "--notify-fd", "3")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.ExtraFiles = []*os.File{notifyW}
	// The listener's strings are not translated, PAM's prompt is: keep
	// it all in C.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	// A session of its own whose controlling terminal is our pty: the text
	// listener reads and writes ctermid().
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = cmd.Start()
	notifyW.Close()
	if err != nil {
		slave.Close()
		master.Close()
		notifyR.Close()
		return nil, fmt.Errorf("can't start pkttyagent: %w", err)
	}
	// Our copy of the terminal stays open until the registration: then it
	// is made exclusive and closed (lockTTY).
	a := &Agent{opts: opts, cmd: cmd, master: master, tty: slave.Name(), prompts: make(chan driver.AgentPrompt),
		exited: make(chan struct{}), stop: make(chan struct{}), read: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(a.exited)
	}()
	var tail strings.Builder // what it said before registering (errors)
	lines := make(chan string, 16)
	go a.reader(lines)

	registered := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, notifyR) // EOF: closed (registered) or exited
		notifyR.Close()
		close(registered)
	}()
	timeout := time.NewTimer(opts.Register)
	defer timeout.Stop()
	for {
		select {
		case l := <-lines:
			if tail.Len() < 400 {
				tail.WriteString(l + " ")
			}
			continue
		case <-registered:
		case <-timeout.C:
			go discard(lines)
			slave.Close()
			a.Stop()
			return nil, errors.New("pkttyagent didn't register in time")
		}
		break
	}
	// The text listener opened its terminal before it registered and never
	// opens it again: from now on nobody else may (lockTTY).
	err = lockTTY(slave)
	slave.Close()
	if err != nil {
		go discard(lines)
		a.Stop()
		return nil, fmt.Errorf("polkit agent: can't make its terminal exclusive: %w", err)
	}
	// A failed registration closes the fd by exiting.
	select {
	case <-a.exited:
		for l := range lines { // what it said, up to its end
			if tail.Len() < 400 {
				tail.WriteString(l + " ")
			}
		}
		a.Stop()
		return nil, fmt.Errorf("pkttyagent couldn't register: %s", strings.TrimSpace(tail.String()))
	case <-time.After(100 * time.Millisecond):
	}
	go a.drain(lines)
	return a, nil
}

func discard(lines <-chan string) {
	for range lines {
	}
}

// Exited is closed once pkttyagent has exited: stopped (Stop, or after an
// end of file spent it), or on its own. While a helper call still waits,
// the caller registers a new agent.
func (a *Agent) Exited() <-chan struct{} { return a.exited }

// Prompts delivers the prompts the agent passes on.
func (a *Agent) Prompts() <-chan driver.AgentPrompt { return a.prompts }

// Stop ends pkttyagent (SIGTERM, SIGKILL a second later) and waits for it;
// an open prompt ends with ErrRefused. Safe to call more than once.
func (a *Agent) Stop() {
	a.once.Do(func() {
		close(a.stop)
		_ = a.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-a.exited:
		case <-time.After(time.Second):
			_ = a.cmd.Process.Kill()
			<-a.exited
		}
		a.master.Close()
		<-a.read
		a.finish(ErrRefused)
	})
}

// refuse ends pkttyagent so polkit's request fails.
func (a *Agent) refuse() { go a.Stop() }

// finish ends the answered prompt with err, if one waits.
func (a *Agent) finish(err error) {
	a.mu.Lock()
	p := a.cur
	a.cur = nil
	a.mu.Unlock()
	if p != nil {
		p.end(err)
	}
}

// escapes are the terminal control sequences the listener colours with.
var escapes = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// promptQuiet is how long a partial line must stand, with nothing more
// coming, to count as a prompt (the listener writes and flushes a prompt
// at once, and then waits).
const promptQuiet = 50 * time.Millisecond

// reader splits pkttyagent's output into lines, plus a partial line that
// stood for promptQuiet with nothing more coming (a prompt), marked with a
// leading \x00.
func (a *Agent) reader(out chan<- string) {
	defer close(a.read)
	defer close(out)
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		defer clear(buf)
		for {
			n, err := a.master.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
				clear(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	var line []byte
	quiet := time.NewTimer(time.Hour)
	quiet.Stop()
	defer quiet.Stop()
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return
			}
			for _, b := range chunk {
				switch b {
				case '\r':
				case '\n':
					out <- escapes.ReplaceAllString(string(line), "")
					line = line[:0]
				default:
					line = append(line, b)
				}
			}
			clear(chunk)
			quiet.Stop()
			if len(line) > 0 {
				quiet.Reset(promptQuiet)
			}
		case <-quiet.C:
			if s := escapes.ReplaceAllString(string(line), ""); s != "" {
				out <- "\x00" + s
			}
			line = line[:0]
		}
	}
}

var menuLine = regexp.MustCompile(`^ (\d+)\.  (.*)$`)

// drain reads the lines after registration: one request at a time.
func (a *Agent) drain(lines <-chan string) {
	var (
		action   string
		identity bool // the identity is settled: the current user's
		menu     map[string]int
		prompted bool // this request's password prompt was passed on
		// cancelling: this request is not ours to answer (not archmaid's
		// action, not the current user, or a second prompt): it is
		// cancelled at its next question, and pkttyagent stays up for the
		// next request (ours, say).
		cancelling bool
		// spent: an end of file was sent. The listener reads with stdio,
		// whose end-of-file flag is sticky (never cleared): every later
		// read fails at once, so this pkttyagent can't serve another
		// request. It is stopped once this one has ended (Exited); the
		// caller starts a new one.
		spent   bool
		refused bool // pkttyagent is being stopped: ignore the rest
	)
	reset := func() { action, identity, menu, prompted, cancelling = "", false, nil, false, false }
	refuse := func() {
		refused = true
		a.refuse()
	}
	for l := range lines {
		partial := strings.HasPrefix(l, "\x00")
		l = strings.TrimPrefix(l, "\x00")
		switch {
		case refused:
		case strings.HasPrefix(l, "==== AUTHENTICATING FOR ") && strings.HasSuffix(l, " ===="):
			reset()
			action = strings.TrimSuffix(strings.TrimPrefix(l, "==== AUTHENTICATING FOR "), " ====")
			if !strings.HasPrefix(action, ActionPrefix) || strings.ContainsAny(action, " \t") {
				cancelling = true // not archmaid's: cancelled, never answered
			}
		case action == "":
			// before a request: nothing to do
		case l == "==== AUTHENTICATION COMPLETE ====":
			if !cancelling {
				a.finish(nil)
			}
			reset()
			if spent {
				refuse()
			}
		case l == "==== AUTHENTICATION FAILED ====", l == "==== AUTHENTICATION CANCELED ====", l == "Cancelled":
			a.finish(errors.New("polkit: authentication failed"))
			reset()
			if spent {
				refuse()
			}
		case l == "Multiple identities can be used for authentication:":
			menu = map[string]int{}
		case menu != nil && !identity && menuLine.MatchString(l):
			m := menuLine.FindStringSubmatch(l)
			n, _ := strconv.Atoi(m[1])
			menu[m[2]] = n
		case partial && menu != nil && !identity && strings.HasPrefix(l, "Choose identity to authenticate as"):
			n := 0
			for who, i := range menu {
				if a.isUser(who) {
					n = i
				}
			}
			if cancelling || n == 0 {
				// No valid choice: the listener cancels the request
				// ("Invalid response"), and asks for nothing.
				cancelling = true
				if _, err := a.master.WriteString("0\n"); err != nil {
					refuse()
				}
				continue
			}
			identity = true
			_, _ = a.master.WriteString(strconv.Itoa(n) + "\n")
		case strings.HasPrefix(l, "Authenticating as: "):
			if !cancelling && !a.isUser(strings.TrimPrefix(l, "Authenticating as: ")) {
				cancelling = true // someone else's password: never asked for
			}
			identity = !cancelling
		case partial && (cancelling || prompted):
			// A password prompt we won't answer (a second one in our
			// request, too: polkit's PAM asks for more than the
			// password): an end of file, so the listener cancels the
			// request instead of waiting until Stop.
			cancelling, spent = true, true
			if err := a.sendEOF(); err != nil {
				refuse()
			}
		case partial && identity:
			p := &Prompt{a: a, action: action, text: l, done: make(chan struct{})}
			identity, prompted = false, true
			select {
			case a.prompts <- p:
			case <-a.stop:
				refused = true
			}
		}
	}
	// pkttyagent is gone.
	a.finish(errors.New("polkit agent: pkttyagent exited"))
}

// sendEOF ends the listener's open prompt unanswered: once it has turned
// echo off (it flushes earlier input), an end of file (^D), on which it
// cancels the request.
func (a *Agent) sendEOF() error {
	if err := a.waitNoEcho(2 * time.Second); err != nil {
		return err
	}
	_, err := a.master.Write([]byte{4})
	return err
}

// isUser reports whether the listener's identity string names the current
// user: "<name>", or "<gecos> (<name>)".
func (a *Agent) isUser(who string) bool {
	return who == a.opts.User || strings.HasSuffix(who, " ("+a.opts.User+")")
}

// Prompt is pkttyagent asking for the password (driver.AgentPrompt).
type Prompt struct {
	a            *Agent
	action, text string
	answered     sync.Once
	done         chan struct{}
	endOnce      sync.Once
	err          error
}

var _ driver.AgentPrompt = (*Prompt)(nil)

func (p *Prompt) Action() string        { return p.action }
func (p *Prompt) Prompt() string        { return p.text }
func (p *Prompt) Done() <-chan struct{} { return p.done }

// Err is polkit's verdict, once Done is closed.
func (p *Prompt) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}

func (p *Prompt) end(err error) {
	p.endOnce.Do(func() {
		p.err = err
		close(p.done)
	})
}

// Cancel refuses the prompt: pkttyagent is stopped, polkit fails it.
func (p *Prompt) Cancel() {
	p.end(ErrRefused)
	p.a.refuse()
}

// Answer writes pw and a newline to pkttyagent, once it has turned echo
// off (the listener flushes what came before: TCSAFLUSH). Once only.
func (p *Prompt) Answer(pw []byte) error {
	err := errors.New("polkit agent: answered already")
	p.answered.Do(func() {
		a := p.a
		select {
		case <-a.stop:
			err = ErrRefused
			return
		default:
		}
		a.mu.Lock()
		a.cur = p
		a.mu.Unlock()
		if err = a.waitNoEcho(2 * time.Second); err != nil {
			a.refuse()
			return
		}
		if pids := a.otherHolders(); len(pids) > 0 {
			err = fmt.Errorf("%w (pid %v)", ErrTTYShared, pids)
			a.refuse()
			return
		}
		line := make([]byte, len(pw)+1)
		copy(line, pw)
		line[len(pw)] = '\n'
		_, err = a.master.Write(line)
		clear(line)
	})
	return err
}

// waitNoEcho waits until the pty's echo is off.
func (a *Agent) waitNoEcho(within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		var t syscall.Termios
		if err := ioctl(a.master, syscall.TCGETS, unsafe.Pointer(&t)); err != nil {
			return err
		}
		if t.Lflag&syscall.ECHO == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("polkit agent: the terminal never turned echo off")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ioctl runs an ioctl on f without f.Fd(): Fd puts the file in blocking
// mode, after which Close no longer interrupts a Read in progress, and
// Stop would wait for the reader until every holder of the terminal let
// go of it.
func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// openPTY opens a new pty pair: the master, and its terminal.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		master.Close()
		return nil, nil, err
	}
	var n uint32
	if err := ioctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		master.Close()
		return nil, nil, err
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
