package xray

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/readeem/omihomo/internal/hosting"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/mux"
	xnet "github.com/xtls/xray-core/common/net"
)

type blockedReceiver struct {
	opened    atomic.Int32
	dropped   atomic.Int32
	oversized atomic.Int32
	local     net.Conn
}

func (r *blockedReceiver) Open(context.Context, hosting.Destination) (net.Conn, error) {
	r.opened.Add(1)
	return r.local, nil
}
func (r *blockedReceiver) Connected() {}
func (r *blockedReceiver) Drop(oversized bool) {
	if oversized {
		r.oversized.Add(1)
	} else {
		r.dropped.Add(1)
	}
}

func writeFrame(t *testing.T, w io.Writer, meta mux.FrameMetadata, p []byte) {
	t.Helper()
	b := buf.New()
	defer b.Release()
	if err := meta.WriteTo(b); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	if meta.Option.Has(mux.OptionData) {
		var size [2]byte
		binary.BigEndian.PutUint16(size[:], uint16(len(p)))
		if _, err := w.Write(size[:]); err != nil {
			t.Fatal(err)
		}
		if len(p) > 0 {
			if _, err := w.Write(p); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestNativeFramesBoundQueuesAndRejectOversize(t *testing.T) {
	for _, network := range []xnet.Network{xnet.Network_UDP, xnet.Network_TCP} {
		t.Run(network.SystemString(), func(t *testing.T) {
			peer, stream := net.Pipe()
			local, target := net.Pipe()
			defer target.Close()
			r := &blockedReceiver{local: local}
			c := &connection{conn: stream, output: &lockedWriter{conn: stream}, receiver: r, sessions: map[uint16]*session{}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			stop := context.AfterFunc(ctx, func() { stream.Close() })
			defer stop()
			go func() { err := c.serve(ctx); c.close(); done <- err }()
			defer peer.Close()
			peer.SetDeadline(time.Now().Add(4 * time.Second))
			go io.Copy(io.Discard, peer)
			dest := xnet.Destination{Network: network, Address: xnet.ParseAddress("10.77.0.2"), Port: 25565}
			writeFrame(t, peer, mux.FrameMetadata{SessionID: 1, SessionStatus: mux.SessionStatusNew, Target: dest, Option: mux.OptionData}, []byte("first"))
			for range 100 {
				writeFrame(t, peer, mux.FrameMetadata{SessionID: 1, SessionStatus: mux.SessionStatusKeep, Option: mux.OptionData}, []byte("queued"))
			}
			writeFrame(t, peer, mux.FrameMetadata{SessionID: 1, SessionStatus: mux.SessionStatusKeep, Option: mux.OptionData}, make([]byte, buf.Size+1))
			writeFrame(t, peer, mux.FrameMetadata{SessionID: 1, SessionStatus: mux.SessionStatusKeep, Option: mux.OptionData}, []byte("after oversized"))
			if network == xnet.Network_TCP {
				target.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := target.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("stalled TCP session was not closed: %v", err)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt blocked target writes")
			}
			if r.opened.Load() != 1 {
				t.Fatalf("opened %d sessions", r.opened.Load())
			}
			if network == xnet.Network_UDP && r.dropped.Load() == 0 {
				t.Fatal("blocked UDP target grew queue instead of dropping")
			}
			if r.oversized.Load() != 1 {
				t.Fatalf("oversized count %d", r.oversized.Load())
			}
		})
	}
}
func TestNativeFramesRejectDomainsAndTargetReplacement(t *testing.T) {
	peer, stream := net.Pipe()
	local, target := net.Pipe()
	defer target.Close()
	r := &blockedReceiver{local: local}
	c := &connection{conn: stream, output: &lockedWriter{conn: stream}, receiver: r, sessions: map[uint16]*session{}}
	done := make(chan error, 1)
	go func() { err := c.serve(context.Background()); c.close(); done <- err }()
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(4 * time.Second))
	writeFrame(t, peer, mux.FrameMetadata{SessionID: 1, SessionStatus: mux.SessionStatusNew, Target: xnet.UDPDestination(xnet.DomainAddress("localhost"), 25565)}, nil)
	var response mux.FrameMetadata
	if err := response.Unmarshal(peer, true); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != 1 || response.SessionStatus != mux.SessionStatusEnd || r.opened.Load() != 0 {
		t.Fatal("domain destination reached local admission")
	}
	dest := xnet.UDPDestination(xnet.ParseAddress("10.77.0.2"), 25565)
	writeFrame(t, peer, mux.FrameMetadata{SessionID: 2, SessionStatus: mux.SessionStatusNew, Target: dest}, nil)
	changed := buf.New()
	changed.UDP = &xnet.Destination{Network: xnet.Network_UDP, Address: xnet.ParseAddress("127.0.0.1"), Port: 22}
	if err := (mux.FrameMetadata{SessionID: 2, SessionStatus: mux.SessionStatusKeep}).WriteTo(changed); err != nil {
		t.Fatal(err)
	}
	_, err := peer.Write(changed.Bytes())
	changed.Release()
	if err != nil {
		t.Fatal(err)
	}
	target.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := target.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("replacement did not close target: %v", err)
	}
	peer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream shutdown blocked")
	}
	if r.opened.Load() != 1 {
		t.Fatal("replacement opened a second target")
	}
}
