package connlog_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/readeem/omihomo/internal/core/connlog"
	"github.com/readeem/omihomo/internal/protocol"
)

func TestHistoryGroupsRejectedRoutesUnderReject(t *testing.T) {
	h := connlog.New(t.TempDir(), 1)
	t.Cleanup(h.Discard)
	for i, outbound := range []string{"DIRECT", "proxy-a", "proxy-b", "proxy-c"} {
		h.Start(outbound, connlog.Key{Process: "browser", Outbound: outbound}, func() (int64, int64) {
			return int64(i + 1), 0
		}, nil)()
	}
	for _, outbound := range []string{"REJECT", "REJECT-DROP"} {
		h.Start(outbound, connlog.Key{Process: "browser", Outbound: outbound, Blocked: true}, nil, nil)
	}
	summary, open := h.Summary()
	if len(summary.Flows) != 5 || !slices.Contains(summary.Flows, protocol.ConnectionFlow{Process: "browser", Outbound: "REJECT"}) {
		t.Errorf("session flows = %+v, want four traffic routes and one REJECT route", summary.Flows)
	}
	if open != 0 || summary.Blocked != 2 || summary.Upload != 10 || summary.Download != 0 {
		t.Fatalf("summary = %+v, open = %d", summary, open)
	}
	page, err := h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 5 || page.Rows[0].Outbound != "REJECT" || page.Rows[0].Count != 2 {
		t.Fatalf("connection history = %+v, %v", page.Rows, err)
	}
}

func TestHistorySummaryKeepsNamedProcessesWhenAnotherProcessIsRejected(t *testing.T) {
	h := connlog.New(t.TempDir(), 1)
	t.Cleanup(h.Discard)
	for i := range 6 {
		name := fmt.Sprintf("client-%d", i)
		h.Start(name, connlog.Key{Process: name, Outbound: "DIRECT"}, func() (int64, int64) { return 1, 0 }, nil)()
	}
	h.Start("blocked", connlog.Key{Process: "blocked", Outbound: "REJECT", Blocked: true}, nil, nil)
	summary, _ := h.Summary()
	if !slices.Contains(summary.Flows, protocol.ConnectionFlow{Process: "client-5", Outbound: "DIRECT", Bytes: 1}) ||
		!slices.Contains(summary.Flows, protocol.ConnectionFlow{Outbound: "REJECT", Bytes: 0}) {
		t.Fatalf("session flows = %+v", summary.Flows)
	}
}

func TestHistoryPagesAndUpdatesRowsPastTenThousand(t *testing.T) {
	h := connlog.New(t.TempDir(), 1)
	t.Cleanup(h.Discard)
	const distinct = 10030
	key := connlog.Key{Process: "unknown", Network: "tcp", Inbound: "redir", Outbound: "DIRECT",
		ProcessStatus: protocol.ProcessLookupFailed, ProcessReason: protocol.ProcessSocketNotFound}
	for i := range distinct {
		key.Destination = fmt.Sprintf("host-%d.example:443", i)
		h.Start("flow", key, func() (int64, int64) { return 7, 11 }, nil)()
	}
	page, err := h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 100 || page.Rows[0].ID != distinct {
		t.Fatalf("first history page has %d rows, error %v", len(page.Rows), err)
	}
	since := page.Revision
	done := h.Start("flow", key, func() (int64, int64) { return 5, 9 }, nil)
	done()
	done()
	delta, err := h.Page(t.Context(), protocol.ConnectionHistoryArgs{Since: &since, Session: page.Session})
	if err != nil || len(delta.Rows) != 1 || delta.Rows[0].ID != distinct || delta.Rows[0].Count != 2 || delta.Rows[0].Upload != 12 || delta.Rows[0].Download != 20 {
		t.Fatalf("changed row = %+v, %v", delta, err)
	}
	if row := delta.Rows[0]; row.Inbound != key.Inbound || row.ProcessStatus != key.ProcessStatus || row.ProcessReason != key.ProcessReason {
		t.Fatalf("spilled attribution = %+v", row)
	}
	total := len(page.Rows)
	for page.Next != 0 {
		page, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{Before: page.Next, Session: page.Session, ClearedAt: page.ClearedAt})
		if err != nil {
			t.Fatal(err)
		}
		total += len(page.Rows)
	}
	if total != distinct {
		t.Fatalf("history has %d rows, want %d", total, distinct)
	}
	if summary, _ := h.Summary(); summary.Incomplete || summary.Closed != distinct+1 || summary.Upload != 70215 || summary.Download != 110339 {
		t.Fatalf("summary = %+v", summary)
	}
	key.ProcessReason = protocol.ProcessOwnerUnavailable
	h.Start("flow", key, func() (int64, int64) { return 1, 1 }, nil)()
	page, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 100 || page.Rows[0].ProcessReason != key.ProcessReason || page.Rows[0].Count != 1 || page.Rows[1].Count != 2 {
		t.Fatalf("history did not distinguish lookup reasons: %+v, %v", page, err)
	}

	done = h.Start("live", connlog.Key{Process: "live", Outbound: "DIRECT"}, func() (int64, int64) { return 2, 3 }, nil)
	h.Start("blocked", connlog.Key{Process: "blocked", Blocked: true}, nil, nil)
	h.Clear()
	summary, open := h.Summary()
	if open != 1 || summary.Closed != 0 || summary.Blocked != 0 || summary.Upload != 2 || summary.Download != 3 ||
		!slices.Equal(summary.Flows, []protocol.ConnectionFlow{{Process: "live", Outbound: "DIRECT", Bytes: 5}}) {
		t.Fatalf("summary after clearing spilled history = %+v, open = %d", summary, open)
	}
	page, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) != 0 {
		t.Fatalf("history after clearing = %+v, %v", page, err)
	}
	done()
	delta, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{Since: &page.Revision, Session: page.Session})
	if err != nil || len(delta.Rows) != 1 || delta.Rows[0].Process != "live" || delta.Rows[0].Count != 1 {
		t.Fatalf("connection closed after clearing = %+v, %v", delta, err)
	}
}

func TestHistoryRecoversWhenSpillBecomesAvailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	h := connlog.New(dir, 1)
	t.Cleanup(h.Discard)
	key := connlog.Key{Network: "tcp", Outbound: "REJECT", Blocked: true}
	for i := range 10001 {
		key.Destination = fmt.Sprintf("host-%d.example:443", i)
		h.Start("", key, nil, nil)
	}
	page, err := h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) == 0 || page.Rows[0].ID != 10000 {
		t.Fatalf("page during spill failure has %d rows, error %v", len(page.Rows), err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h.Start("", key, nil, nil)
	page, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(page.Rows) == 0 || page.Rows[0].ID != 10001 || page.Rows[0].Destination != key.Destination {
		t.Fatalf("page after recovery has %d rows, error %v", len(page.Rows), err)
	}
	if summary, _ := h.Summary(); summary.Blocked != 10002 || !summary.Incomplete {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestHistoryPaginationResetsAcrossClear(t *testing.T) {
	h := connlog.New(t.TempDir(), 1)
	t.Cleanup(h.Discard)
	for i := range 150 {
		h.Start("", connlog.Key{Destination: fmt.Sprintf("old-%d", i), Blocked: true}, nil, nil)
	}
	first, err := h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || len(first.Rows) != 100 || first.Next == 0 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	oldCursor := protocol.ConnectionHistoryArgs{Session: first.Session, Before: first.Next, ClearedAt: first.ClearedAt}
	h.Start("", connlog.Key{Destination: "another-old", Blocked: true}, nil, nil)
	page, err := h.Page(t.Context(), oldCursor)
	if err != nil || page.Reset || len(page.Rows) != 50 {
		t.Fatalf("continuation after new activity = %+v, %v", page, err)
	}
	h.Clear()
	for i := range 10 {
		h.Start("", connlog.Key{Destination: fmt.Sprintf("new-%d", i), Blocked: true}, nil, nil)
	}
	page, err = h.Page(t.Context(), oldCursor)
	if err != nil || !page.Reset || len(page.Rows) != 0 || page.Next != 0 || page.Session != first.Session || page.ClearedAt <= first.Revision {
		t.Fatalf("continuation after clearing = %+v, %v", page, err)
	}
	for i := 10; i < 150; i++ {
		h.Start("", connlog.Key{Destination: fmt.Sprintf("new-%d", i), Blocked: true}, nil, nil)
	}
	first, err = h.Page(t.Context(), protocol.ConnectionHistoryArgs{})
	if err != nil || first.Reset || len(first.Rows) != 100 || first.Next == 0 {
		t.Fatalf("first page after clearing = %+v, %v", first, err)
	}
	newCursor := protocol.ConnectionHistoryArgs{Session: first.Session, Before: first.Next, ClearedAt: first.ClearedAt}
	page, err = h.Page(t.Context(), newCursor)
	if err != nil || page.Reset || len(page.Rows) != 50 || page.Rows[0].Destination != "new-49" {
		t.Fatalf("continuation with current clear revision = %+v, %v", page, err)
	}
	h.Clear()
	h.Start("", connlog.Key{Destination: "after-second-clear", Blocked: true}, nil, nil)
	page, err = h.Page(t.Context(), newCursor)
	if err != nil || !page.Reset || len(page.Rows) != 0 {
		t.Fatalf("continuation after another clear = %+v, %v", page, err)
	}
}

func BenchmarkHistory(b *testing.B) {
	h := connlog.New(b.TempDir(), 1)
	b.Cleanup(h.Discard)
	key := connlog.Key{Process: "browser", Destination: "example.com:443", Network: "tcp", Outbound: "REJECT", Rule: "Domain(example.com)", Blocked: true}
	b.ReportAllocs()
	for b.Loop() {
		h.Start("", key, nil, nil)
	}
}
