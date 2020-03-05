package inunaki

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

type Local struct {
	conn    ssh.Conn
	tunnels localTunnels
}

func Open(host string, public ssh.PublicKey, private ssh.Signer) (*Local, error) {
	conn, err := ssh.Dial("tcp", host, &ssh.ClientConfig{
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(private),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if !bytes.Equal(key.Marshal(), public.Marshal()) {
				return errors.New("public key is different")
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	log := logrus.WithField("addr", conn.RemoteAddr())
	log.Info("opened connection")

	l := Local{
		conn: conn,
		tunnels: localTunnels{
			tunnels: map[string]int{},
		},
	}

	go func() {
		for nc := range conn.HandleChannelOpen("tunnel") {
			go l.handleNewChannel(nc)
		}
	}()

	return &l, nil
}

func (l *Local) handleNewChannel(nc ssh.NewChannel) {
	name := string(nc.ExtraData())

	log := logrus.WithField("name", name)

	port, ok := l.tunnels.get(name)
	if !ok {
		if err := nc.Reject(ssh.Prohibited, ""); err != nil {
			log.WithField("err", err).Info("failed to reject")
		}
		return
	}

	log = log.WithField("port", port)

	ch, reqs, err := nc.Accept()
	if err != nil {
		log.WithField("err", err).Info("failed to accept")
		return
	}
	defer ch.Close()

	go ssh.DiscardRequests(reqs)

	conn, err := net.Dial("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.WithField("err", err).Info("failed to dial")
		return
	}
	defer conn.Close()

	log = log.WithField("addr", conn.RemoteAddr())

	log.Info("start tunneling")
	defer log.Info("end tunneling")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ch.Close()
		io.Copy(ch, conn)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer conn.Close()
		io.Copy(conn, ch)
	}()
	wg.Wait()
}

func (l *Local) Bind(name string, port int) error {
	log := logrus.WithFields(logrus.Fields{
		"name": name,
		"port": port,
	})

	ok, _, err := l.conn.SendRequest("bind", true, []byte(name))
	if err != nil {
		log.Error("failed to bind")
		return err
	}

	if !ok {
		log.Warn("refused to bind")
		return fmt.Errorf("couldn't bind: %s", name)
	}

	log.Info("succeeded to bind")

	l.tunnels.set(name, port)
	return nil
}

func (l *Local) Close() error {
	return l.conn.Close()
}

func (l *Local) Wait() error {
	return l.conn.Wait()
}

type localTunnels struct {
	sync.RWMutex
	tunnels map[string]int
}

func (l *localTunnels) get(name string) (int, bool) {
	l.RLock()
	defer l.RUnlock()

	c, ok := l.tunnels[name]
	return c, ok
}

func (l *localTunnels) set(name string, port int) {
	l.Lock()
	defer l.Unlock()

	l.tunnels[name] = port
}

func (l *localTunnels) delete(name string) {
	l.Lock()
	defer l.Unlock()

	delete(l.tunnels, name)
}
