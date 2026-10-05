package realdriver

import (
	"context"
	"errors"
	"os"
	"os/user"

	"github.com/Enzord758/archmaid/gui/internal/polkitagent"
	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// helper is the Driver's Helper for one run: helperclient.Run with
// polkit's text agent registered for archmaid-gui around it, its prompts
// passed to the Driver on agents (Deps.Agent), so the run's one password
// answers polkit too. Without an agent of ours (pkttyagent missing, or
// it can't register) polkit asks through the desktop's agent, as before:
// the status line says so (SetNotice), and the step's output.
func (w *Wiring) helper(agents chan<- driver.AgentPrompt) driver.Helper {
	return func(ctx context.Context, seq uint64, st steps.Step, emit func(engine.Input)) helperclient.Outcome {
		fallback := func(err error) {
			why := "polkit's own dialog asks: " + err.Error()
			w.notice(why)
			emit(engine.StepOutput{Seq: seq, Lines: []string{"archmaid: " + why}})
		}
		a, err := w.startAgent()
		if err != nil {
			fallback(err)
			return helperclient.Run(ctx, seq, st, emit)
		}
		w.notice("")
		stop := make(chan struct{})
		forwarded := make(chan struct{})
		go func() {
			defer close(forwarded)
			defer func() {
				if a != nil {
					a.Stop()
				}
			}()
			restarts := 0
			for {
				var prompts <-chan driver.AgentPrompt
				var exited <-chan struct{}
				if a != nil {
					prompts, exited = a.Prompts(), a.Exited()
				}
				select {
				case p := <-prompts:
					select {
					case agents <- p:
					case <-ctx.Done():
						p.Cancel()
						return
					case <-stop:
						p.Cancel()
						return
					}
				case <-exited:
					// It ended while the call still waits (an end of file
					// spent it, or it died): register a new one, so a
					// request of ours still to come has it.
					a.Stop()
					a = nil
					if restarts++; restarts > maxAgentRestarts {
						fallback(errors.New("pkttyagent keeps ending"))
						continue
					}
					if a, err = w.startAgent(); err != nil {
						a = nil
						fallback(err)
					}
				case <-stop:
					return
				}
			}
		}()
		out := helperclient.Run(ctx, seq, st, emit)
		close(stop)
		<-forwarded
		return out
	}
}

// maxAgentRestarts bounds how often one helper call registers a new agent.
const maxAgentRestarts = 3

// startAgent registers pkttyagent for this process (Config.TTYAgent).
func (w *Wiring) startAgent() (*polkitagent.Agent, error) {
	if w.cfg.TTYAgent == "-" {
		return nil, errors.New("no polkit agent of archmaid's")
	}
	self, err := proc.Read(os.Getpid())
	if err != nil {
		return nil, err
	}
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	return polkitagent.Start(polkitagent.Options{Path: w.cfg.TTYAgent, Pid: os.Getpid(), Start: self.Start, User: u.Username})
}

// SetNotice sets what the window is told when polkit's own dialog has to
// ask (no agent of archmaid's): f gets the reason, or "" once an agent of
// ours runs. It may be called from any goroutine.
func (w *Wiring) SetNotice(f func(string)) {
	w.mu.Lock()
	w.onNotice = f
	w.mu.Unlock()
}

func (w *Wiring) notice(s string) {
	w.mu.Lock()
	f := w.onNotice
	w.mu.Unlock()
	if f != nil {
		f(s)
	}
}
