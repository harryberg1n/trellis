package run

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"testing"

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
	}
	testiso.Main(m)
}

func blockForever() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
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
	fmt.Fprintf(conn, "%d\n", gc.Process.Pid)
	conn.Close()
	blockForever()
	return 0
}
