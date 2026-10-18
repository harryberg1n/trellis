package run

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/harryberg1n/trellis/internal/clock"
	"github.com/harryberg1n/trellis/internal/testiso"
)

// Roles for the self-spawn pattern: the test binary re-executed with these
// set is a child that starts a sleeping grandchild, or that grandchild.
const (
	roleEnv = "TRELLIS_RUN_TEST_ROLE"
	addrEnv = "TRELLIS_RUN_TEST_ADDR"
)

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "child":
		os.Exit(childMain())
	case "sleeper":
		blockForever()
	case "stubborn":
		os.Exit(stubbornMain())
	case "exiter":
		os.Exit(exiterMain())
	case "parent":
		os.Exit(parentMain())
	}
	testiso.Main(m)
}

func blockForever() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}

// stubbornMain ignores SIGTERM, tells the test it is ready, and blocks until it
// is killed.
func stubbornMain() int {
	signal.Ignore(syscall.SIGTERM)
	conn, err := net.Dial("tcp", os.Getenv(addrEnv))
	if err != nil {
		return 3
	}
	if _, err := fmt.Fprintf(conn, "%d\n", os.Getpid()); err != nil {
		return 4
	}
	_ = conn.Close() // the pid is sent; nothing more to report
	blockForever()
	return 0
}

// exiterMain starts a grandchild that holds this child's output pipes open,
// reports the grandchild's pid, and exits 0 at once.
func exiterMain() int {
	gc := exec.Command(os.Args[0])
	gc.Env = append(os.Environ(), roleEnv+"=sleeper")
	gc.Stdout, gc.Stderr = os.Stdout, os.Stderr
	if err := gc.Start(); err != nil {
		return 2
	}
	conn, err := net.Dial("tcp", os.Getenv(addrEnv))
	if err != nil {
		return 3
	}
	if _, err := fmt.Fprintf(conn, "%d\n", gc.Process.Pid); err != nil {
		return 4
	}
	_ = conn.Close() // the pid is sent; nothing more to report
	return 0
}

// childMain starts a sleeping grandchild, reports its pid to the test over
// TCP so the test knows the tree exists, then blocks.
func childMain() int {
	gc := exec.Command(os.Args[0])
	gc.Env = append(os.Environ(), roleEnv+"=sleeper")
	if err := gc.Start(); err != nil {
		return 2
	}
	conn, err := net.Dial("tcp", os.Getenv(addrEnv))
	if err != nil {
		return 3
	}
	if _, err := fmt.Fprintf(conn, "%d\n", gc.Process.Pid); err != nil {
		return 4
	}
	_ = conn.Close() // the pid is sent; nothing more to report
	blockForever()
	return 0
}

// parentMain runs a stubborn child through Light and waits for it: the child
// reports its pid to the test, and the test then kills this process.
func parentMain() int {
	if err := os.Setenv(roleEnv, "stubborn"); err != nil {
		return 2
	}
	if _, err := Light(clock.Real(), Spec{Name: os.Args[0], Timeout: time.Hour}); err != nil {
		return 3
	}
	return 0
}
