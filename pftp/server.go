package pftp

import (
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lestrrat/go-server-starter/listener"
	"github.com/sirupsen/logrus"
)

type middlewareFunc func(*Context, string) error
type middleware map[string]middlewareFunc

type FtpServer struct {
	listener      net.Listener
	clientCounter int
	config        *config
	middleware    middleware
	shutdown      bool
}

func NewFtpServer(confFile string) (*FtpServer, error) {
	c, err := loadConfig(confFile)
	if err != nil {
		return nil, err
	}
	m := middleware{}
	return &FtpServer{
		config:     c,
		middleware: m,
	}, nil
}

func (server *FtpServer) Use(command string, m middlewareFunc) {
	server.middleware[strings.ToUpper(command)] = m
}

func (server *FtpServer) listen() (err error) {
	if os.Getenv("SERVER_STARTER_PORT") != "" {
		listeners, err := listener.ListenAll()
		if listeners == nil || err != nil {
			return err
		}
		server.listener = listeners[0]
	} else {
		l, err := net.Listen("tcp", server.config.ListenAddr)
		if err != nil {
			return err
		}
		server.listener = l
	}

	logrus.Info("Listening address ", server.listener.Addr())

	return err
}

func (server *FtpServer) serve() error {
	var currentConnection int32
	currentConnection = 0
	eg := errgroup.Group{}

	for {
		conn, err := server.listener.Accept()
		if err != nil {
			// if use server starter, break for while all childs end
			if os.Getenv("SERVER_STARTER_PORT") != "" {
				logrus.Info("Close listener")
				break
			}

			if server.listener != nil {
				return err
			}
		}

		if server.config.IdleTimeout > 0 {
			conn.SetDeadline(time.Now().Add(time.Duration(server.config.IdleTimeout) * time.Second))
		}

		server.clientCounter++
		c := newClientHandler(conn, server.config, server.middleware, server.clientCounter, &currentConnection)
		logrus.Debug("FTP Client connected ", "clientIp ", conn.RemoteAddr())
		eg.Go(func() error {
			return c.handleCommands()
		})
	}

	return eg.Wait()
}

func (server *FtpServer) Start() error {
	var lastError error
	done := make(chan struct{})

	if err := server.listen(); err != nil {
		return err
	}

	logrus.Info("Starting...")

	go func() {
		if err := server.serve(); err != nil {
			if !server.shutdown {
				lastError = err
			}
		}
		done <- struct{}{}
	}()

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM)
L:
	for {
		switch <-ch {
		case syscall.SIGHUP, syscall.SIGTERM:
			if err := server.stop(); err != nil {
				lastError = err
			}
			break L
		}
	}

	<-done
	return lastError
}

func (server *FtpServer) stop() error {
	server.shutdown = true
	if server.listener != nil {
		if err := server.listener.Close(); err != nil {
			return err
		}
	}
	return nil
}
