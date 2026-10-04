package engine

import "github.com/Enzord758/archmaid/internal/actions"

// What the external tests (session_test.go) need from inside the package.

// ErrNotImplemented is what a part of the Session that isn't built yet
// panics with.
var ErrNotImplemented = errNotImplemented

// SplitQuestion is splitQuestion.
var SplitQuestion = splitQuestion

// SetMenu makes s look actions up in menu instead of actions.Menu.
func SetMenu(s *Session, menu []actions.Action) {
	s.lookup = func(id string) (actions.Action, bool) {
		for _, a := range menu {
			if a.ID == id {
				return a, true
			}
		}
		return actions.Action{}, false
	}
}
