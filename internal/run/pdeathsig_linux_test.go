//go:build linux

package run

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The parent of a light child dies the one way no handler sees, a SIGKILL: the
// kernel must end the child with it. The parent is this test binary in role
// "parent", which starts a stubborn child through Light and holds.
func TestLight_ChildDoesNotOutliveAParentThatIsKilled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	parent := exec.CommandContext(ctx, os.Args[0])
	parent.Env = append(os.Environ(), roleEnv+"=parent", addrEnv+"="+ln.Addr().String())
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })

	conn, err := ln.Accept()
	if err != nil {
		t.Fatalf("the parent's child never reported: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.Close() // read-only use; the read error below is the one that matters
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(pid) {
		t.Fatalf("child %d is not running before its parent is killed, so the test proves nothing", pid)
	}

	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()

	for i := 0; alive(pid); i++ {
		if i == 5_000_000 {
			t.Fatalf("child %d survived the SIGKILL of its parent", pid)
		}
	}
}
