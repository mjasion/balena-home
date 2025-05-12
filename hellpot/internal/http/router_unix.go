//go:build linux || darwin || freebsd

package http

import (
	"net"
	"os"
	"syscall"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	"github.com/yunginnanet/HellPot/internal/config"
)

func listenOnUnixSocket(addr string, r *router.Router) error {
	var err error
	var unixAddr *net.UnixAddr
	var unixListener *net.UnixListener
	unixAddr, err = net.ResolveUnixAddr("unix", addr)
	if err != nil {
		return err
	}
	// Always unlink sockets before listening on them
	_ = syscall.Unlink(addr)
	// Before we set socket permissions, we want to make sure only the user HellPot is running under
	// has permission to the socket.
	oldmask := syscall.Umask(0o077)
	unixListener, err = net.ListenUnix("unix", unixAddr)
	syscall.Umask(oldmask)
	if err != nil {
		return err
	}
	if err = os.Chmod(
		unixAddr.Name,
		os.FileMode(config.UnixSocketPermissions),
	); err != nil {
		return err
	}

	return fasthttp.Serve(unixListener, r.Handler)
}
