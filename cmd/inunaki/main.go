package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ichiban/assets"
	"github.com/ichiban/inunaki"
	"golang.org/x/crypto/ssh"
)

func main() {
	var host string
	var tunnels tunnels
	flag.StringVar(&host, "host", "ichiban.dev:22", ``)
	flag.Var(&tunnels, "tunnel", ``)
	flag.Parse()

	private, err := privateKey()
	if err != nil {
		panic(err)
	}
	public := private.PublicKey()

	local, err := inunaki.Open(host, public, private)
	if err != nil {
		os.Exit(1)
	}
	defer local.Wait()

	for _, t := range tunnels {
		if err := local.Bind(t.name, t.port); err != nil {
			os.Exit(2)
		}
	}
}

func privateKey() (ssh.Signer, error) {
	a, err := assets.New()
	defer a.Close()

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
