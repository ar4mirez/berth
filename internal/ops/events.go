package ops

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// Event is one thing a long operation reports while it runs (#54): `--output json` on up, restart,
// build, pull, backup, restore, logs and remote logs prints one per line (docs/json.md).
type Event struct {
	Schema string `json:"schema"` // "berth.event/v1"
	// Time is when it happened (RFC 3339, UTC).
	Time string `json:"time"`
	// Op is the operation ("up", "backup", …), and Org the org it is on ("" when it has none).
	Op  string `json:"op"`
	Org string `json:"org"`
	// Type is "start", "step", "output", "done" or "failed".
	Type string `json:"type"`
	// Step names the step that begins ("image", "container", …), for a "step" event.
	Step string `json:"step"`
	// Stream is "stdout" or "stderr", for an "output" event: a line the operation, or a command it
	// ran, printed.
	Stream string `json:"stream"`
	// Message is the step's description, or the line.
	Message string `json:"message"`
	// Error is why it failed, for a "failed" event (null otherwise).
	Error *ErrorDoc `json:"error"`
}

// Event types.
const (
	EventStart  = "start"
	EventStep   = "step"
	EventOutput = "output"
	EventDone   = "done"
	EventFailed = "failed"
)

// Sink receives an operation's events, in order.
type Sink func(Event)

// Progress is one running operation's events. A nil *Progress reports nothing, so an operation
// calls it without asking whether anyone listens.
type Progress struct {
	sink    Sink
	op, org string
	now     func() time.Time
	mu      sync.Mutex
	writers []*lineWriter
}

// NewProgress starts reporting op on org to sink, with a "start" event.
func NewProgress(sink Sink, op, org string) *Progress {
	p := &Progress{sink: sink, op: op, org: org, now: time.Now}
	p.emit(Event{Type: EventStart})
	return p
}

func (p *Progress) emit(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.Schema, e.Op, e.Org = "berth.event/v1", p.op, p.org
	e.Time = p.now().UTC().Format(time.RFC3339Nano)
	p.sink(e)
}

// Step says a named step of the operation begins.
func (p *Progress) Step(name, message string) {
	if p == nil {
		return
	}
	p.flush()
	p.emit(Event{Type: EventStep, Step: name, Message: message})
}

// Writer is where the operation's output goes while it reports: each line written is an "output"
// event on the stream ("stdout" or "stderr").
func (p *Progress) Writer(stream string) io.Writer {
	w := &lineWriter{p: p, stream: stream}
	p.mu.Lock()
	p.writers = append(p.writers, w)
	p.mu.Unlock()
	return w
}

func (p *Progress) flush() {
	p.mu.Lock()
	ws := append([]*lineWriter(nil), p.writers...)
	p.mu.Unlock()
	for _, w := range ws {
		w.flush()
	}
}

// Done ends the operation: "done", or "failed" with err as an Error. It returns the exit code.
func (p *Progress) Done(err error) int {
	p.flush()
	if e := AsError(err); e != nil {
		doc := e.Doc()
		p.emit(Event{Type: EventFailed, Message: e.Msg, Error: &doc})
		return e.Code
	}
	p.emit(Event{Type: EventDone})
	return 0
}

// lineWriter turns what is written into one "output" event per line. A line without its newline
// waits for the rest, and goes out at the next step or at the end.
type lineWriter struct {
	p      *Progress
	stream string
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			return len(b), nil
		}
		line := string(bytes.TrimRight(w.buf.Next(i+1), "\r\n"))
		w.p.emit(Event{Type: EventOutput, Stream: w.stream, Message: line})
	}
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.p.emit(Event{Type: EventOutput, Stream: w.stream, Message: w.buf.String()})
		w.buf.Reset()
	}
}
