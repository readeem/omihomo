package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/readeem/omihomo/internal/protocol"
)

type session struct {
	d    *server
	conn net.Conn

	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
	inFlight  chan struct{}
	calls     sync.WaitGroup
	delays    sync.Map // request ID -> context.CancelFunc

	// streams is guarded by d.mu.
	streams map[string]bool
}

func newSession(d *server, conn net.Conn) *session {
	return &session{
		d:        d,
		conn:     conn,
		out:      make(chan []byte, sendQueue),
		done:     make(chan struct{}),
		inFlight: make(chan struct{}, maxInFlight),
		streams:  map[string]bool{},
	}
}

func (s *session) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.conn.Close()
	})
}

func (s *session) wants(stream string) bool { return s.streams[stream] }

func (s *session) send(v any) {
	b, err := protocol.Line(v)
	if err != nil {
		s.d.log.Error("encode message", "err", err)
		return
	}
	s.sendLine(b)
}

// sendLine disconnects a client that lets its queue fill rather than let it
// hold the daemon's memory.
func (s *session) sendLine(b []byte) {
	select {
	case <-s.done:
	case s.out <- b:
	default:
		s.d.log.Warn("disconnected a client that stopped reading")
		s.close()
	}
}

// A client that stops sending still gets the replies to what it asked.
func (s *session) serve(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.close()
	go func() {
		select {
		case <-ctx.Done():
			s.close()
		case <-s.done:
		}
	}()
	drain := make(chan struct{})
	written := make(chan struct{})
	go func() {
		defer close(written)
		s.write(drain)
	}()
	defer func() {
		s.calls.Wait()
		close(drain)
		<-written
	}()

	scanner := protocol.NewScanner(s.conn)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req protocol.Request
		if err := protocol.Decode(line, &req); err != nil {
			s.reply(recoverID(line), nil, fail(protocol.CodeBadRequest, "not a request: %v", err))
			continue
		}
		if req.ID == 0 || req.Op == "" {
			s.reply(req.ID, nil, &protocol.Error{Code: protocol.CodeBadRequest, Message: "a request needs an id and an op"})
			continue
		}
		if req.Op == protocol.OpDelayCancel {
			var args protocol.DelayCancelArgs
			if err := decode(req.Args, &args); err != nil {
				s.reply(req.ID, nil, err)
				continue
			}
			if cancelDelay, ok := s.delays.Load(args.ID); ok {
				cancelDelay.(context.CancelFunc)()
			}
			s.reply(req.ID, nil, nil)
			continue
		}
		select {
		case s.inFlight <- struct{}{}:
		default:
			s.reply(req.ID, nil, &protocol.Error{Code: protocol.CodeBusy, Message: "too many calls in flight"})
			continue
		}
		callCtx, callCancel := context.WithTimeout(ctx, callTimeout)
		if req.Op == protocol.OpDelayTest {
			if _, loaded := s.delays.LoadOrStore(req.ID, callCancel); loaded {
				callCancel()
				<-s.inFlight
				s.reply(req.ID, nil, fail(protocol.CodeBadRequest, "delay test id already in use"))
				continue
			}
		}
		s.calls.Go(func() {
			defer func() { <-s.inFlight }()
			defer callCancel()
			if req.Op == protocol.OpDelayTest {
				defer s.delays.Delete(req.ID)
			}
			result, perr := s.d.handle(callCtx, s, req)
			s.reply(req.ID, result, perr)
		})
	}
}

// recoverID is the id of a request that failed to decode, so its caller
// can match the error. It is 0 when the line is not a JSON object with a
// numeric id.
func recoverID(line []byte) uint64 {
	var probe struct {
		ID uint64 `json:"id"`
	}
	if json.Unmarshal(line, &probe) != nil {
		return 0
	}
	return probe.ID
}

// reply sends the one answer a request gets. A result too large to send
// becomes a too_large error, so the call fails without ending the session.
func (s *session) reply(id uint64, result any, perr *protocol.Error) {
	r := protocol.Reply{Type: protocol.TypeReply, ID: id, Error: perr}
	if perr == nil {
		if result == nil {
			result = protocol.Empty{}
		}
		r.Result = result
	}
	b, err := protocol.Line(r)
	if err != nil {
		s.d.log.Error("encode reply", "id", id, "err", err)
		code := protocol.CodeInternal
		if errors.Is(err, protocol.ErrTooLarge) {
			code = protocol.CodeTooLarge
		}
		b, _ = protocol.Line(protocol.Reply{Type: protocol.TypeReply, ID: id, Error: fail(code, "%v", err)})
	}
	s.sendLine(b)
}

// write returns when the session closes, or once drain is closed and the
// queue is empty.
func (s *session) write(drain <-chan struct{}) {
	put := func(b []byte) bool {
		s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := s.conn.Write(b); err != nil {
			s.close()
			return false
		}
		return true
	}
	for {
		select {
		case <-s.done:
			return
		case b := <-s.out:
			if !put(b) {
				return
			}
		case <-drain:
			for {
				select {
				case b := <-s.out:
					if !put(b) {
						return
					}
				default:
					return
				}
			}
		}
	}
}
