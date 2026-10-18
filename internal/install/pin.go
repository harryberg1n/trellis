package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// Pin places the binary at src under the state root as the pinned copy of
// version, and returns its path. A pinned file already there is kept whatever
// it holds, as Install keeps it; copied says whether Pin wrote one.
func Pin(root, version, src string) (bin string, copied bool, err error) {
	if version == "" || version == "." || version == ".." || filepath.Base(version) != version || filepath.ToSlash(version) != version {
		return "", false, fmt.Errorf("version %q cannot name a directory", version)
	}
	bin = BinPath(root, version, filepath.Ext(src))
	if _, err := os.Stat(bin); err == nil {
		return bin, false, nil
	}
	if err := pin(bin, src); err != nil {
		return "", false, err
	}
	return bin, true, nil
}
