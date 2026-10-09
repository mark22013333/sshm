package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/tunnelstate"
)

func seedRunning(t *testing.T, statePath string, fo *fakeOrca, handle string) {
	t.Helper()
	fo.live[handle] = true
	writeState(t, statePath, map[string]tunnelstate.Entry{
		"t_db": {Handle: handle, StartedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano)},
	})
}

// R1：第一次查詢回來前，有紀錄的 tunnel 顯示 ◌，Enter 不會再啟動一次。
func TestTunnelUnknownBeforeFirstRefresh(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	seedRunning(t, sp, fo, "term_old")
	send(t, m, "tab") // 不執行查詢：模擬 list 尚未回來
	if m.tunnelStatusOf("t_db") != tunnelUnknown || !strings.Contains(m.View(), "◌ db") {
		t.Fatalf("status=%v\n%s", m.tunnelStatusOf("t_db"), m.View())
	}
	if m.tunnelStatusOf("t_orphan") != tunnelStopped {
		t.Fatal("沒有紀錄的 tunnel 應為已停止")
	}
	drain(t, m, send(t, m, "enter"))
	if n := len(fo.callsOf("create")); n != 0 {
		t.Fatalf("◌ 時不應啟動，create=%d", n)
	}
	if st, _ := tunnelstate.Load(sp); st.Tunnels["t_db"].Handle != "term_old" {
		t.Fatalf("handle 被改動：%+v", st.Tunnels)
	}
}

// R2：查詢失敗（◌）時不可刪除。
func TestTunnelDeleteBlockedWhenUnknown(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	seedRunning(t, sp, fo, "term_old")
	m.opts.Runner = func(ctx context.Context, argv []string) ([]byte, []byte, error) {
		if argv[2] == "list" {
			return nil, []byte("boom"), errors.New("exit 1")
		}
		return fo.run(ctx, argv)
	}
	drain(t, m, send(t, m, "tab"))
	if m.tunnelStatusOf("t_db") != tunnelUnknown {
		t.Fatalf("前提：status=%v", m.tunnelStatusOf("t_db"))
	}
	send(t, m, "ctrl+x")
	if m.mode == modeConfirmTunnelDelete || !strings.Contains(m.status, "仍有執行紀錄") {
		t.Fatalf("◌ 時不應進入刪除確認；status=%q", m.status)
	}
	send(t, m, "y")
	if f, _ := store.Open(m.opts.Path); f.TunnelIndex("t_db") < 0 {
		t.Fatal("◌ 時刪除了 tunnel")
	}
}

// R5：不在 Orca 中、state 有紀錄時也不可刪除。
func TestTunnelDeleteBlockedOutsideOrca(t *testing.T) {
	m, fo, sp := setupTunnels(t, false)
	seedRunning(t, sp, fo, "term_old")
	drain(t, m, send(t, m, "tab"))
	send(t, m, "ctrl+x", "y")
	if f, _ := store.Open(m.opts.Path); f.TunnelIndex("t_db") < 0 {
		t.Fatal("不在 Orca 中仍刪除了有執行紀錄的 tunnel")
	}
}

// R3：較早發出的查詢晚到時不得覆寫剛啟動的狀態，也不能因此重複啟動。
func TestTunnelStaleRefreshIgnored(t *testing.T) {
	m, fo, _ := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	early := m.refreshTunnelsCmd()() // 啟動前發出、晚到
	drain(t, m, send(t, m, "enter"))
	if m.tunnelStatusOf("t_db") != tunnelRunning {
		t.Fatalf("前提：status=%v", m.tunnelStatusOf("t_db"))
	}
	m.Update(early)
	if m.tunnelStatusOf("t_db") != tunnelRunning {
		t.Fatalf("過時的查詢結果覆寫了狀態：%v", m.tunnelStatusOf("t_db"))
	}
	fo.next = "term_dup"
	drain(t, m, send(t, m, "enter")) // 應為停止，不是再啟動
	if len(fo.callsOf("create")) != 1 || len(fo.callsOf("close")) != 1 {
		t.Fatalf("create=%d close=%d", len(fo.callsOf("create")), len(fo.callsOf("close")))
	}
}

// R4：兩個 sshm 實例，B 在刷新前啟動同一條：在 state 鎖內被拒絕，不覆寫 A 的 handle。
func TestTunnelTwoInstancesNoDoubleStart(t *testing.T) {
	a, fo, sp := setupTunnels(t, true)
	b := New(Options{Path: a.opts.Path, File: a.file, ScriptPath: "/cfg/sshm-login.exp", StatePath: sp,
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: true, Runner: fo.run})
	drain(t, a, send(t, a, "tab"))
	drain(t, b, send(t, b, "tab"))
	fo.next = "term_A"
	drain(t, a, send(t, a, "enter"))
	fo.next = "term_B"
	// B 的畫面仍是舊狀態（○），直接執行啟動指令，不先刷新
	_, cmd := b.toggleTunnel()
	msg := cmd()
	if am, ok := msg.(tunnelActionMsg); !ok || am.err == nil || !strings.Contains(am.err.Error(), "已有執行紀錄") {
		t.Fatalf("B 應被拒絕：%+v", msg)
	}
	b.Update(msg)
	if n := len(fo.callsOf("create")); n != 1 {
		t.Fatalf("create=%d", n)
	}
	if st, _ := tunnelstate.Load(sp); st.Tunnels["t_db"].Handle != "term_A" {
		t.Fatalf("handle 被覆寫：%+v", st.Tunnels)
	}
}

// 啟動失敗時清掉保留紀錄，之後可以再試。
func TestTunnelStartFailureClearsReservation(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	m.opts.Runner = func(ctx context.Context, argv []string) ([]byte, []byte, error) {
		if argv[2] == "create" {
			return []byte(`{"ok":false,"error":{"message":"no worktree"}}`), nil, errors.New("exit 1")
		}
		return fo.run(ctx, argv)
	}
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "enter"))
	if st, _ := tunnelstate.Load(sp); len(st.Tunnels) != 0 {
		t.Fatalf("失敗後應清掉保留：%+v", st.Tunnels)
	}
	if !strings.Contains(m.status, "no worktree") || m.tunnelStatusOf("t_db") != tunnelStopped {
		t.Fatalf("status=%q st=%v", m.status, m.tunnelStatusOf("t_db"))
	}
}

// 上一次查詢還沒回來時，tick 只排下一次、不再發查詢。
func TestTunnelTickSkipsWhileInFlight(t *testing.T) {
	m, fo, _ := setupTunnels(t, true)
	send(t, m, "tab") // 第一次查詢已發出、尚未回來
	if m.tInFlight != 1 {
		t.Fatalf("inFlight=%d", m.tInFlight)
	}
	_, cmd := m.Update(tunnelTickMsg{})
	if _, ok := cmd().(tunnelTickMsg); !ok {
		t.Fatal("查詢進行中時 tick 應只排下一次")
	}
	if len(fo.callsOf("list")) != 0 || m.tInFlight != 1 {
		t.Fatalf("不應再發查詢：list=%d inFlight=%d", len(fo.callsOf("list")), m.tInFlight)
	}
}

// PTY 重連中的 handle 顯示 ◌，不清紀錄。
func TestTunnelReconnectingIsUnknown(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	seedRunning(t, sp, fo, "term_old")
	m.opts.Runner = func(ctx context.Context, argv []string) ([]byte, []byte, error) {
		if argv[2] == "list" {
			return []byte(`{"ok":true,"result":{"terminals":[{"handle":"term_old","connected":false}],"truncated":false,
				"hostScope":{"hostIds":["local"],"omittedHostIds":[]}}}`), nil, nil
		}
		return fo.run(ctx, argv)
	}
	drain(t, m, send(t, m, "tab"))
	if m.tunnelStatusOf("t_db") != tunnelUnknown {
		t.Fatalf("status=%v", m.tunnelStatusOf("t_db"))
	}
	if st, _ := tunnelstate.Load(sp); st.Tunnels["t_db"].Handle != "term_old" {
		t.Fatal("重連中不可清紀錄")
	}
	_ = tea.Quit
}

// Ctrl+G：有 handle 時呼叫 orca terminal switch，sshm 保持開啟。
func TestTunnelGotoTab(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	seedRunning(t, sp, fo, "term_old")
	drain(t, m, send(t, m, "tab"))
	cmd := send(t, m, "ctrl+g")
	drain(t, m, cmd)
	sw := fo.callsOf("switch")
	if len(sw) != 1 || strings.Join(sw[0], " ") != "/orca/orca terminal switch --terminal=term_old --json" {
		t.Fatalf("switch = %q", sw)
	}
	if !strings.Contains(m.status, "已切到「db」分頁") || m.statusErr {
		t.Fatalf("status = %q", m.status)
	}
	if !strings.Contains(m.View(), "Ctrl+G 前往分頁") {
		t.Fatal("說明列缺 Ctrl+G")
	}
}

func TestTunnelGotoTabWithoutRecord(t *testing.T) {
	m, fo, _ := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "ctrl+g"))
	if len(fo.callsOf("switch")) != 0 || !strings.Contains(m.status, "這條 tunnel 沒有在執行") {
		t.Fatalf("status=%q calls=%v", m.status, fo.calls)
	}
}

func TestTunnelGotoTabOrcaError(t *testing.T) {
	m, fo, sp := setupTunnels(t, true)
	seedRunning(t, sp, fo, "term_old")
	m.opts.Runner = func(ctx context.Context, argv []string) ([]byte, []byte, error) {
		if argv[2] == "switch" {
			return []byte(`{"ok":false,"error":{"message":"terminal not found"}}`), nil, errors.New("exit 1")
		}
		return fo.run(ctx, argv)
	}
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "ctrl+g"))
	if !m.statusErr || !strings.Contains(m.status, "terminal not found") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestTunnelGotoTabOutsideOrca(t *testing.T) {
	m, fo, sp := setupTunnels(t, false)
	seedRunning(t, sp, fo, "term_old")
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "ctrl+g"))
	if len(fo.calls) != 0 || !strings.Contains(m.status, "需要在 Orca 中執行") {
		t.Fatalf("status=%q calls=%v", m.status, fo.calls)
	}
}
