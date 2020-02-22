package inunaki

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
)

type Remote struct {
	publicKey  ssh.PublicKey
	privateKey ssh.Signer

	tunnels remoteTunnels
}

func NewRemote(public ssh.PublicKey, private ssh.Signer) *Remote {
	return &Remote{
		publicKey:  public,
		privateKey: private,

		tunnels: remoteTunnels{
			tunnels: map[string]ssh.Conn{},
		},
	}
}

func (r *Remote) Outbound(port int) {
	log := logrus.WithField("port", port)
	log.Info("start outbound")
	defer log.Info("end outbound")

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.WithField("err", err).Info("failed to listen")
		return
	}
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			log.WithField("err", err).Info("failed to accept")
			continue
		}

		go r.handleOutboundConn(c)
	}
}

func (r *Remote) handleOutboundConn(c net.Conn) {
	defer c.Close()

	log := logrus.WithField("addr", c.RemoteAddr())
	log.Info("start outbound connection")
	defer log.Info("end outbound connection")

	h, pr, err := peekHost(c)
	if err != nil {
		return
	}

	name := strings.Split(h, ".")[0]
	log = log.WithField("name", name)

	t, ok := r.tunnels.get(name)
	if !ok {
		log.Info("no remote tunnel")
		return
	}

	ch, reqs, nil := t.OpenChannel("tunnel", []byte(name))
	if err != nil {
		log.Info("failed to open tunnel")
		return
	}
	defer ch.Close()

	go ssh.DiscardRequests(reqs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ch.Close()

		log.Info("start copying from outbound to inbound")
		defer log.Info("end copying from outbound to inbound")

		io.Copy(ch, pr)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer c.Close()

		log.Info("start copying from inbound to outbound")
		defer log.Info("end copying from inbound to outbound")

		io.Copy(c, ch)
	}()
	wg.Wait()
}

func peekHost(c io.Reader) (string, io.Reader, error) {
	// rfc7230 says:
	//   Various ad hoc limitations on request-line length are found in
	//   practice.  It is RECOMMENDED that all HTTP senders and recipients
	//   support, at a minimum, request-line lengths of 8000 octets.
	r := bufio.NewReaderSize(c, 8000)

	var peeked bytes.Buffer
	for {
		l, _, err := r.ReadLine()
		if err != nil {
			return "", nil, err
		}

		if _, err := peeked.Write(append(l, []byte("\r\n")...)); err != nil {
			return "", nil, err
		}

		s := strings.Split(string(l), ":")
		if len(s) == 1 { // e.g. `GET / HTTP/1.1`
			continue
		}

		if strings.EqualFold(s[0], "Host") {
			return strings.TrimSpace(s[1]), io.MultiReader(&peeked, r), nil
		}
	}
}

func (r *Remote) Inbound(port int) {
	log := logrus.WithField("port", port)
	log.Info("start inbound")
	defer log.Info("end inbound")

	config := ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), r.publicKey.Marshal()) {
				return nil, fmt.Errorf("unknown public key for %q", conn.User())
			}
			return &ssh.Permissions{
				Extensions: map[string]string{
					"pubkey-fp": ssh.FingerprintSHA256(key),
				},
			}, nil
		},
	}
	config.AddHostKey(r.privateKey)

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		log.WithField("err", err).Info("failed to listen")
		return
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.WithField("err", err).Info("failed to accept")
			return
		}

		go r.handleInboundConn(conn, config)
	}
}

func (r *Remote) handleInboundConn(nConn net.Conn, config ssh.ServerConfig) {
	defer nConn.Close()

	log := logrus.WithField("addr", nConn.RemoteAddr())
	log.Infof("start inbound connection")
	defer log.Infof("end inbound connection")

	conn, chans, reqs, err := ssh.NewServerConn(nConn, &config)
	if err != nil {
		log.WithField("err", err).Info("failed to handshake")
		return
	}
	log = log.WithField("fingerprint", conn.Permissions.Extensions["pubkey-fp"])

	go func() {
		var names []string
		defer func() {
			for _, n := range names {
				r.tunnels.delete(n)
			}
		}()

		for req := range reqs {
			switch req.Type {
			case "bind":
				name := string(req.Payload)
				log = log.WithField("name", name)

				if _, ok := r.tunnels.get(name); ok {
					if !req.WantReply {
						continue
					}
					if err := req.Reply(false, nil); err != nil {
						log.WithField("err", err).Info("failed to reply")
						continue
					}
				}

				names = append(names, name)
				r.tunnels.set(name, conn)
				if !req.WantReply {
					continue
				}
				if err := req.Reply(true, nil); err != nil {
					log.WithField("err", err).Info("failed to reply")
					continue
				}
			default:
				if !req.WantReply {
					continue
				}
				if err := req.Reply(false, nil); err != nil {
					log.WithField("err", err).Info("failed to reply")
					continue
				}
			}
		}
	}()

	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			go r.handleSession(ch)
		default:
			if err := ch.Reject(ssh.UnknownChannelType, "unknown channel type"); err != nil {
				log.WithField("err", err).Info("failed to reject")
				continue
			}
		}
	}
}

func (r *Remote) handleSession(ch ssh.NewChannel) {
	log := logrus.WithField("type", ch.ChannelType())
	log.Info("start session")
	defer log.Info("end session")

	channel, requests, err := ch.Accept()
	if err != nil {
		log.WithField("err", err).Info("failed to accept")
	}
	defer channel.Close()

	go func(in <-chan *ssh.Request) {
		for req := range in {
			if err := req.Reply(req.Type == "shell", nil); err != nil {
				log.WithField("err", err).Info("failed to reply")
				continue
			}
		}
	}(requests)

	term := terminal.NewTerminal(channel, "> ")

	for {
		line, err := term.ReadLine()
		if err != nil {
			break
		}
		fmt.Fprintf(term, line+"\r\n")
	}
}

type remoteTunnels struct {
	sync.RWMutex
	tunnels map[string]ssh.Conn
}

func (r *remoteTunnels) get(name string) (ssh.Conn, bool) {
	r.RLock()
	defer r.RUnlock()

	c, ok := r.tunnels[name]
	return c, ok
}

func (r *remoteTunnels) set(name string, conn ssh.Conn) {
	r.Lock()
	defer r.Unlock()

	logrus.WithFields(logrus.Fields{
		"name": name,
		"addr": conn.RemoteAddr(),
	}).Info("start tunneling")

	r.tunnels[name] = conn
}

func (r *remoteTunnels) delete(name string) {
	r.Lock()
	defer r.Unlock()

	t, ok := r.tunnels[name]
	if !ok {
		return
	}

	logrus.WithFields(logrus.Fields{
		"name": name,
		"addr": t.RemoteAddr(),
	}).Info("end tunneling")

	delete(r.tunnels, name)
}
