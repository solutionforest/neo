package ui

import (
	"fmt"
	"sync"
	"time"
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays a braille spinner with a message.
type Spinner struct {
	msg    string
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
}

// NewSpinner creates a spinner with the given message.
func NewSpinner(msg string) *Spinner {
	return &Spinner{
		msg:  msg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// Start begins spinning in a goroutine.
func (s *Spinner) Start() {
	go func() {
		defer close(s.done)
		frame := 0
		for {
			select {
			case <-s.stop:
				return
			default:
				s.mu.Lock()
				msg := s.msg
				s.mu.Unlock()
				fmt.Printf("\r  %s %s", Yellow.Render(frames[frame%len(frames)]), msg)
				frame++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// Update changes the spinner message.
func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop halts the spinner and clears the line.
func (s *Spinner) Stop() {
	close(s.stop)
	<-s.done
	fmt.Print("\r\033[K")
}
