package db

import (
	"context"
	"testing"
	"time"
)

// The audit of Agents' transfers is the manager's: a row per call, read back newest first, all of them or a machine's
// (as either end); a row without its machines or state is refused.
func TestFileTransferAudit(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, freshDatabase(t), ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	rows := []FileTransferAudit{
		{ID: "t1", At: at, CallerMachine: "mac", CallerSession: "s1", CallerKind: "agent", Direction: "send", FromMachine: "mac",
			FromPath: "~/out/report.pdf", ToMachine: "box", ResultPath: "/home/u/Downloads/Persona/report.pdf", Kind: "file",
			Bytes: 2048, SHA256: "ab", State: "done", ElapsedMS: 120},
		{ID: "t2", At: at.Add(time.Minute), CallerMachine: "mac", CallerKind: "assistant", Direction: "fetch", FromMachine: "lab",
			FromPath: "~/proj", ToMachine: "mac", ToPath: "~/in/", State: "failed", Code: "offline", Error: "Lab is offline"},
	}
	for _, r := range rows {
		if err := d.AddFileTransferAudit(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.AddFileTransferAudit(ctx, FileTransferAudit{ID: "t3"}); err == nil {
		t.Fatal("a row without machines and state was written")
	}
	all, err := d.FileTransferAudits(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "t2" || all[1].ID != "t1" {
		t.Fatalf("all %+v", all)
	}
	got := all[1]
	got.At = got.At.UTC()
	if got != rows[0] {
		t.Fatalf("read back %+v, want %+v", got, rows[0])
	}
	box, err := d.FileTransferAudits(ctx, "box", 10)
	if err != nil || len(box) != 1 || box[0].ID != "t1" {
		t.Fatalf("box %+v %v", box, err)
	}
	if lab, _ := d.FileTransferAudits(ctx, "lab", 10); len(lab) != 1 || lab[0].Code != "offline" {
		t.Fatalf("lab %+v", lab)
	}
}
