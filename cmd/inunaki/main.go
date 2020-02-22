package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/subcommands"
	"github.com/ichiban/assets"
	"github.com/ichiban/inunaki"
	"golang.org/x/crypto/ssh"
)

func main() {
	subcommands.Register(&remoteCmd{}, "")
	subcommands.Register(&localCmd{}, "")
	flag.Parse()
	os.Exit(int(subcommands.Execute(context.Background())))
}

type remoteCmd struct {
	httpPort int
	sshPort  int
}

func (r *remoteCmd) Name() string {
	return "remote"
}

func (r *remoteCmd) Synopsis() string {
	return ``
}

func (r *remoteCmd) Usage() string {
	return ``
}

func (r *remoteCmd) SetFlags(f *flag.FlagSet) {
	f.IntVar(&r.httpPort, "http", 8080, ``)
	f.IntVar(&r.sshPort, "ssh", 2022, ``)
}

func (r *remoteCmd) Execute(ctx context.Context, f *flag.FlagSet, args ...interface{}) subcommands.ExitStatus {
	private, err := privateKey()
	if err != nil {
		panic(err)
	}
	public := private.PublicKey()

	remote := inunaki.NewRemote(public, private)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		remote.Inbound(r.sshPort)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		remote.Outbound(r.httpPort)
	}()
	wg.Wait()

	return 0
}

type localCmd struct {
	host    string
	tunnels tunnels
}

func (l *localCmd) Name() string {
	return "local"
}

func (l *localCmd) Synopsis() string {
	return ``
}

func (l *localCmd) Usage() string {
	return ``
}

func (l *localCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&l.host, "host", "ichiban.dev:22", ``)
	f.Var(&l.tunnels, "tunnel", ``)
}

func (l *localCmd) Execute(ctx context.Context, f *flag.FlagSet, args ...interface{}) subcommands.ExitStatus {
	private, err := privateKey()
	if err != nil {
		panic(err)
	}
	public := private.PublicKey()

	local, err := inunaki.Open(l.host, public, private)
	if err != nil {
		return 1
	}
	defer local.Wait()

	for _, t := range l.tunnels {
		if err := local.Bind(t.name, t.port); err != nil {
			return 2
		}
	}

	return 0
}

func privateKey() (ssh.Signer, error) {
	a, err := assets.New()
	defer func() {
		if err := a.Close(); err != nil {
			panic(err)
		}
	}()

	privateBytes, err := ioutil.ReadFile(filepath.Join(a.Path, "inunaki_rsa"))
	if err != nil {
		return nil, err
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		return nil, err
	}
	return private, nil
}

type tunnels []tunnel

func (t *tunnels) String() string {
	return ""
}

func (t *tunnels) Set(s string) error {
	ss := strings.Split(s, ":")
	switch len(ss) {
	case 2: // "foo:8080"
		p, err := strconv.Atoi(ss[1])
		if err != nil {
			return err
		}
		*t = append(*t, tunnel{
			name: ss[0],
			port: p,
		})
	default:
		return fmt.Errorf("unknown tunnel: %s", s)
	}
	return nil
}

type tunnel struct {
	name string
	port int
}
