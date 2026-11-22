package connlog

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/metacubex/bbolt"
	dberrors "github.com/metacubex/bbolt/errors"

	"github.com/readeem/omihomo/internal/protocol"
)

const (
	pageSize       = 100
	memoryRows     = 10000
	memoryBytes    = 8 << 20
	pageBytes      = 512 << 10
	batchKeys      = 256
	spillBytes     = 256 << 20
	changesSize    = 4096
	flowKeys       = 4096
	maxRowKeyBytes = 16 << 10
	maxFlowName    = 1024
)

var (
	rowsBucket        = []byte("rows")
	keysBucket        = []byte("keys")
	ErrSessionChanged = errors.New("the proxy session changed")
)

type Key struct {
	Process, Destination, Network, Inbound, Outbound, Rule string
	ProcessStatus                                          protocol.ProcessStatus
	ProcessReason                                          protocol.ProcessReason
	Blocked                                                bool
}

func (k Key) size() int {
	return len(k.Process) + len(k.Destination) + len(k.Network) + len(k.Inbound) + len(k.Outbound) + len(k.Rule) + len(k.ProcessStatus) + len(k.ProcessReason)
}

type counts struct {
	n              uint64
	up, down, last int64
}

type row struct {
	key Key
	id  uint64
	counts
}

func (r row) entry() protocol.ConnectionHistoryRow {
	return protocol.ConnectionHistoryRow{ID: r.id, Process: r.key.Process, Destination: r.key.Destination, Network: r.key.Network,
		Inbound: r.key.Inbound, ProcessStatus: r.key.ProcessStatus, ProcessReason: r.key.ProcessReason,
		Outbound: r.key.Outbound, Rule: r.key.Rule, Blocked: r.key.Blocked, Count: r.n, Upload: r.up, Download: r.down, LastAt: r.last}
}

// History owns one proxy session. It retains the first rows in memory and
// batches the rest into a temporary database. All fields are protected by mu.
type History struct {
	mu                               sync.Mutex
	dir                              string
	id                               uint64
	revision, closed, blocked        uint64
	up, down                         int64
	flows                            map[flowKey]int64
	rejections                       map[flowKey]int64
	live                             map[string]liveConnection
	memory                           map[Key]*row
	rows                             []*row
	bytes                            int
	nextID                           uint64
	pending                          map[Key]counts
	pendingOrder                     []Key
	timer                            *time.Timer
	db                               *bbolt.DB
	incomplete, spillFull, discarded bool
	changes                          [changesSize]uint64
}

type liveConnection struct {
	key   Key
	bytes func() (int64, int64)
}

func New(dir string, id uint64) *History {
	return &History{dir: dir, id: id, flows: make(map[flowKey]int64), rejections: make(map[flowKey]int64),
		live: make(map[string]liveConnection), memory: make(map[Key]*row)}
}

// Start records a rejection immediately. For a live connection, done must
// run once after both directions have finished updating the byte counters.
func (h *History) Start(id string, key Key, readBytes func() (int64, int64), multipleDestinations func() bool) (done func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discarded {
		return nil
	}
	if key.Blocked {
		h.blocked++
		key.Outbound = "REJECT"
		flow := connectionFlow(key)
		if _, found := h.rejections[flow]; !found && len(h.rejections) >= flowKeys {
			flow.process = ""
		}
		h.rejections[flow] = 0
		h.record(key, 0, 0)
		return nil
	}
	h.live[id] = liveConnection{key: key, bytes: readBytes}
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		live, ok := h.live[id]
		if !ok {
			return
		}
		delete(h.live, id)
		key := live.key
		up, down := readBytes()
		h.closed++
		h.up += up
		h.down += down
		if up+down > 0 {
			flow := connectionFlow(key)
			if _, found := h.flows[flow]; !found && len(h.flows) >= flowKeys {
				flow = flowKey{}
			}
			h.flows[flow] += up + down
		}
		if multipleDestinations != nil && multipleDestinations() {
			key.Destination = "Multiple destinations"
		}
		h.record(key, up, down)
	}
}

func (h *History) record(key Key, up, down int64) {
	if key.size() > maxRowKeyBytes {
		h.incomplete = true
		return
	}
	at := time.Now().UnixMilli()
	if r := h.memory[key]; r != nil {
		r.n++
		r.up += up
		r.down += down
		r.last = at
		h.changed(r.id)
		return
	}
	if h.db == nil && !h.spillFull && len(h.rows) < memoryRows && h.bytes+key.size()+256 <= memoryBytes {
		h.nextID++
		r := &row{key: key, id: h.nextID, n: 1, up: up, down: down, last: at}
		h.memory[key] = r
		h.rows = append(h.rows, r)
		h.bytes += key.size() + 256
		h.changed(r.id)
		return
	}
	if h.pending == nil {
		h.pending = make(map[Key]counts)
	}
	c := h.pending[key]
	if c.n == 0 {
		h.pendingOrder = append(h.pendingOrder, key)
	}
	h.pending[key] = counts{n: c.n + 1, up: c.up + up, down: c.down + down, last: at}
	if len(h.pendingOrder) >= batchKeys {
		h.flush()
	} else if h.timer == nil {
		h.timer = time.AfterFunc(100*time.Millisecond, func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.flush()
		})
	}
}

func (h *History) changed(id uint64) {
	h.revision++
	h.changes[h.revision%changesSize] = id
}

func (h *History) Discard() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.discarded = true
	if h.timer != nil {
		h.timer.Stop()
	}
	if h.db != nil {
		h.db.Close()
	}
	h.db = nil
	h.pending = nil
	h.pendingOrder = nil
	h.live = nil
	h.rows = nil
	h.memory = nil
	h.flows = nil
	h.rejections = nil
}

func (h *History) flush() {
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
	if len(h.pendingOrder) == 0 {
		return
	}
	err := h.write()
	if err != nil {
		h.incomplete = true
		if errors.Is(err, dberrors.ErrMaxSizeReached) && !h.spillFull {
			h.spillFull = true
			_ = h.write()
		}
	}
	h.pending = nil
	h.pendingOrder = nil
}

func (h *History) write() error {
	if h.db == nil {
		// Use an unlinked file so the session leaves nothing behind on daemon exit.
		file, err := os.CreateTemp(h.dir, "connection-history-*")
		if err != nil {
			return err
		}
		name := file.Name()
		defer func() {
			if file != nil {
				file.Close()
			}
		}()
		defer os.Remove(name)
		db, err := bbolt.Open(name, 0o600, &bbolt.Options{NoSync: true, MaxSize: spillBytes,
			OpenFile: func(string, int, os.FileMode) (*os.File, error) {
				if err := os.Remove(name); err != nil {
					return nil, err
				}
				opened := file
				file = nil
				return opened, nil
			},
		})
		if err != nil {
			return err
		}
		h.db = db
	}
	next := h.nextID
	changed := make([]uint64, 0, len(h.pendingOrder))
	err := h.db.Update(func(tx *bbolt.Tx) error {
		rows, err := tx.CreateBucketIfNotExists(rowsBucket)
		if err != nil {
			return err
		}
		keys, err := tx.CreateBucketIfNotExists(keysBucket)
		if err != nil {
			return err
		}
		for _, key := range h.pendingOrder {
			fields := [...]string{key.Process, key.Destination, key.Network, key.Inbound, key.Outbound, key.Rule, string(key.ProcessStatus), string(key.ProcessReason)}
			data := make([]byte, 0, key.size()+4*len(fields)+1)
			for _, field := range fields {
				data = binary.BigEndian.AppendUint32(data, uint32(len(field)))
				data = append(data, field...)
			}
			data = append(data, byte(0))
			if key.Blocked {
				data[len(data)-1] = 1
			}
			id := keys.Get(data)
			entry := protocol.ConnectionHistoryRow{}
			if id == nil {
				if h.spillFull {
					continue
				}
				next++
				id = binary.BigEndian.AppendUint64(nil, next)
				if err := keys.Put(data, id); err != nil {
					return err
				}
				entry = (row{key: key, id: next}).entry()
			} else if err := json.Unmarshal(rows.Get(id), &entry); err != nil {
				return err
			}
			c := h.pending[key]
			entry.Count += c.n
			entry.Upload += c.up
			entry.Download += c.down
			entry.LastAt = c.last
			encoded, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			if err := rows.Put(id, encoded); err != nil {
				return err
			}
			changed = append(changed, entry.ID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.nextID = next
	for _, id := range changed {
		h.changed(id)
	}
	return nil
}

func (h *History) Session() uint64 { return h.id }

func (h *History) Summary() (protocol.ConnectionSummary, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := protocol.ConnectionSummary{Session: h.id, Revision: h.revision, Closed: h.closed, Blocked: h.blocked, Upload: h.up, Download: h.down, Incomplete: h.incomplete}
	live := make(map[flowKey]int64)
	for _, c := range h.live {
		up, down := c.bytes()
		out.Upload += up
		out.Download += down
		if up+down > 0 {
			live[connectionFlow(c.key)] += up + down
		}
	}
	out.Flows = summarizeFlows(h.flows, live, h.rejections)
	return out, len(h.live)
}

func (h *History) Page(ctx context.Context, args protocol.ConnectionHistoryArgs) (protocol.ConnectionHistory, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discarded || args.Session != 0 && args.Session != h.id {
		return protocol.ConnectionHistory{}, ErrSessionChanged
	}
	h.flush()
	out := protocol.ConnectionHistory{Session: h.id, Revision: h.revision, Rows: []protocol.ConnectionHistoryRow{}}
	var ids []uint64
	if args.Since != nil {
		if *args.Since > h.revision || h.revision-*args.Since > changesSize {
			out.Reset = true
			return out, nil
		}
		seen := make(map[uint64]bool)
		for revision := *args.Since + 1; revision <= h.revision; revision++ {
			id := h.changes[revision%changesSize]
			if !seen[id] {
				if len(ids) == pageSize {
					out.Revision = revision - 1
					break
				}
				ids = append(ids, id)
				seen[id] = true
			}
		}
	} else {
		before := args.Before
		if before == 0 || before > h.nextID {
			before = h.nextID + 1
		}
		for id := before - 1; id > 0 && len(ids) < pageSize; id-- {
			ids = append(ids, id)
		}
	}
	var bytes int
	read := func(bucket *bbolt.Bucket) error {
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			var entry protocol.ConnectionHistoryRow
			if id <= uint64(len(h.rows)) {
				entry = h.rows[id-1].entry()
			} else {
				if bucket == nil {
					return errors.New("connection history database unavailable")
				}
				if err := json.Unmarshal(bucket.Get(binary.BigEndian.AppendUint64(nil, id)), &entry); err != nil {
					return err
				}
			}
			size := 256 + 6*(len(entry.Process)+len(entry.Destination)+len(entry.Network)+len(entry.Inbound)+len(entry.Outbound)+len(entry.Rule)+len(entry.ProcessStatus)+len(entry.ProcessReason))
			if len(out.Rows) > 0 && bytes+size > pageBytes {
				break
			}
			bytes += size
			out.Rows = append(out.Rows, entry)
		}
		return nil
	}
	var err error
	if h.db != nil {
		err = h.db.View(func(tx *bbolt.Tx) error { return read(tx.Bucket(rowsBucket)) })
	} else {
		err = read(nil)
	}
	if err != nil {
		return protocol.ConnectionHistory{}, err
	}
	if args.Since != nil && len(out.Rows) < len(ids) {
		id := ids[len(out.Rows)]
		for revision := *args.Since + 1; revision <= out.Revision; revision++ {
			if h.changes[revision%changesSize] == id {
				out.Revision = revision - 1
				break
			}
		}
	}
	if args.Since == nil && len(out.Rows) > 0 {
		last := out.Rows[len(out.Rows)-1].ID
		if last > 1 {
			out.Next = last
		}
	}
	return out, nil
}

type flowKey struct{ process, outbound string }

func connectionFlow(key Key) flowKey {
	flow := flowKey{key.Process, key.Outbound}
	if len(flow.process) > maxFlowName {
		flow.process = ""
	}
	if len(flow.outbound) > maxFlowName {
		flow.outbound = ""
	}
	return flow
}

func summarizeFlows(closed, live, rejections map[flowKey]int64) []protocol.ConnectionFlow {
	const processLimit = 6
	processes, outbounds := make(map[string]int64), make(map[string]int64)
	for _, flows := range []map[flowKey]int64{closed, live} {
		for key, n := range flows {
			processes[key.process] += n
			outbounds[key.outbound] += n
		}
	}
	keepProcess, keepOutbound := topNames(processes, processLimit), topNames(outbounds, 4)
	available := processLimit - len(keepProcess)
	if available > 0 && len(keepProcess) == len(processes) && len(rejections) > 0 {
		rejected := make(map[string]int64)
		for key := range rejections {
			if !keepProcess[key.process] {
				rejected[key.process] = 0
			}
		}
		for name := range topNames(rejected, available) {
			keepProcess[name] = true
		}
	}
	folded := make(map[flowKey]int64)
	for _, flows := range []map[flowKey]int64{closed, live, rejections} {
		for key, n := range flows {
			if !keepProcess[key.process] {
				key.process = ""
			}
			if n > 0 && !keepOutbound[key.outbound] {
				key.outbound = ""
			}
			folded[key] += n
		}
	}
	out := make([]protocol.ConnectionFlow, 0, len(folded))
	for key, n := range folded {
		out = append(out, protocol.ConnectionFlow{Process: key.process, Outbound: key.outbound, Bytes: n})
	}
	slices.SortFunc(out, func(a, b protocol.ConnectionFlow) int {
		if order := cmp.Compare(a.Process, b.Process); order != 0 {
			return order
		}
		return cmp.Compare(a.Outbound, b.Outbound)
	})
	return out
}

func topNames(totals map[string]int64, limit int) map[string]bool {
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if order := cmp.Compare(totals[b], totals[a]); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
	if _, hasOther := totals[""]; len(names) > limit || hasOther {
		names = slices.DeleteFunc(names, func(name string) bool { return name == "" })
		names = names[:min(len(names), limit-1)]
	}
	keep := make(map[string]bool, len(names))
	for _, name := range names {
		keep[name] = true
	}
	return keep
}
