package helper

import (
	"log"

	"github.com/falcosecurity/event-generator/events"
)

var _ = events.Register(DoNothing)

// DoNothing does nothing.
// It can be used for rules that only need an execve event and/or command line arguments.
func DoNothing(h events.Helper) error {
	log.Println("doing nothing")
	return nil
}
