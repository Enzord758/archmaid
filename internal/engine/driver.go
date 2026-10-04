package engine

// Driver runs a Session for a front end that can't call it synchronously
// (the GUI): it owns the Session, carries out its Effects, feeds their
// results back, runs the one status ticker (Tick → StatusUpdated), and
// streams the Events.
//
// Events delivers every Event in order (see the package doc for what a
// Driver may coalesce or drop: OutputChunk lines, nothing else); it is the
// same channel on every call and is closed once Close has returned. Send
// hands the Session a Command and never blocks: it returns false if the
// Command could not be queued (the Driver is closed, or its queue is full;
// a front end may retry or report it), true once it is queued. A queued
// Command is applied in order; one for a stale PromptID is ignored, as by
// the Session. Close stops the Driver, cancelling what runs (as Abort
// would) and releasing every goroutine and child it started; it is safe to
// call more than once and from any goroutine.
type Driver interface {
	Events() <-chan Event
	Send(Command) bool
	Close()
}

// Command is a Session call sent through a Driver; each type is the method
// of the same name. The set is closed.
type Command interface{ isCommand() }

// Start is Session.Start.
type Start struct{ Action string }

// Confirm is Session.Confirm.
type Confirm struct {
	Prompt PromptID
	Yes    bool
}

// SubmitPassword is Session.SubmitPassword. The Driver passes Password on
// and keeps no copy.
type SubmitPassword struct {
	Prompt   PromptID
	Password Secret
}

// CancelPrompt is Session.CancelPrompt.
type CancelPrompt struct{ Prompt PromptID }

// Select is Session.Select.
type Select struct {
	Prompt PromptID
	Picked []string
}

// Abort is Session.Abort.
type Abort struct{}

func (Start) isCommand()          {}
func (Confirm) isCommand()        {}
func (SubmitPassword) isCommand() {}
func (CancelPrompt) isCommand()   {}
func (Select) isCommand()         {}
func (Abort) isCommand()          {}
