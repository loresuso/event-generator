package syscall

import (
	"os"
	"syscall"

	"github.com/falcosecurity/event-generator/events"
	"golang.org/x/sys/unix"
)

var _ = events.Register(OpenSSLFileReadOrWrite)

func OpenSSLFileReadOrWrite(h events.Helper) error {
	if h.Spawned() {
		fd, err := syscall.Openat(unix.AT_FDCWD, "/etc/passwd", os.O_RDONLY, 0)
		defer syscall.Close(fd)
		return err
	} else {
		h.SpawnAs("openssl", "helper.DoNothing", true, "-encrypt", "-in", "")
	}

	return nil
}
