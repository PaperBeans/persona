package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// BenchmarkPublishListWithArchive is one session of a machine's 200 changing and its list told the hub
// (publishList), with the machine's archive keeping 0, 2,000 or 8,000 rows its node no longer lists (#6836): told
// whole (an older hub) and told apart (Hello.ArchivedApart).
func BenchmarkPublishListWithArchive(b *testing.B) {
	for _, records := range []int{0, 2000, 8000} {
		for _, apart := range []bool{false, true} {
			b.Run(fmt.Sprintf("records=%d/apart=%v", records, apart), func(b *testing.B) {
				f := newFakeHub()
				f.hello.Assistant.MachineID = "box"
				f.hello.Assistant.Machines = []string{"box"}
				f.hello.ArchivedApart = apart
				f.devices = []Device{{ID: "box", Home: "/home/u"}}
				d := newFleetDevs()
				d.add("box", "Computer")
				st := newFleetMem()
				s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})
				archive := map[string]agentArchive{}
				for i := range records {
					at := time.Unix(1600000000+int64(i), 0)
					sess := archiveSnapshot(proto.Session{MachineID: "box", SessionID: fmt.Sprintf("a%05d", i), Title: "An archived task",
						Cwd: "/code/example", Agent: "claude", UpdatedAt: at, LastText: strings.Repeat("x", 180)})
					archive[runtimeListID("box", sess.SessionID)] = agentArchive{Session: sess, Archived: true, ChangedAt: at}
				}
				if err := st.PutSetting(context.Background(), agentArchivesKey, archive); err != nil {
					b.Fatal(err)
				}
				list := make([]proto.Session, 200)
				for i := range list {
					list[i] = proto.Session{SessionID: fmt.Sprintf("s%03d", i), Cwd: "/code/example", Agent: "claude",
						Status: proto.StatusIdle, Title: "Example task", LastText: strings.Repeat("Example result. ", 32)}
				}
				d.setSessions("box", list)
				s.publishList("box")
				f.mu.Lock()
				f.told = nil
				f.mu.Unlock()
				b.ReportAllocs()
				n := 0
				for b.Loop() {
					n++
					list[n%len(list)].LastText = fmt.Sprintf("%512d", n)
					d.setSessions("box", list)
					s.publishList("box")
				}
				f.mu.Lock()
				told := 0
				for _, e := range f.told {
					told += len(e.params)
				}
				b.ReportMetric(float64(told)/float64(n), "told-B/op")
				f.mu.Unlock()
			})
		}
	}
}

func BenchmarkApplyArchives_500Sessions(b *testing.B) {
	ctx := context.Background()
	f := newFakeHub()
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})

	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	archive := map[string]agentArchive{}
	list := make([]proto.Session, 500)
	for i := range list {
		sid := fmt.Sprintf("claude-11111111-2222-4333-8444-%012d", i)
		sess := proto.Session{
			MachineID: "box",
			SessionID: sid,
			Cwd:       "/code/example",
			Agent:     "claude",
			Status:    proto.StatusIdle,
			Title:     "Example session",
			UpdatedAt: at,
		}
		if i < 20 {
			sess.SetAliases([]string{fmt.Sprintf("pane-w1-p%02d", i)})
		}
		list[i] = sess
		if i < 50 {
			archive[runtimeListID("box", sid)] = agentArchive{
				Session:   archiveSnapshot(sess),
				Archived:  i < 25,
				ChangedAt: at,
			}
		}
	}
	for i := range 200 {
		sid := fmt.Sprintf("archived-tail-%04d", i)
		sess := archiveSnapshot(proto.Session{
			MachineID: "box",
			SessionID: sid,
			Cwd:       "/code/example",
			Agent:     "claude",
			UpdatedAt: at,
		})
		archive[runtimeListID("box", sid)] = agentArchive{
			Session:   sess,
			Archived:  true,
			ChangedAt: at,
		}
	}
	if err := st.PutSetting(ctx, agentArchivesKey, archive); err != nil {
		b.Fatal(err)
	}

	mark := s.archiveMark()
	if _, _, err := s.applyArchives(ctx, "box", list, mark, true); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := s.applyArchives(ctx, "box", list, mark, true); err != nil {
			b.Fatal(err)
		}
	}
}
