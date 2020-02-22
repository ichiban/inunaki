package main

import (
	"flag"
	"io/ioutil"
	"path/filepath"
	"sync"

	"github.com/ichiban/assets"
	"github.com/ichiban/inunaki"
	"golang.org/x/crypto/ssh"
)

func main() {
	var httpPort int
	var sshPort int
	flag.IntVar(&httpPort, "http", 8080, ``)
	flag.IntVar(&sshPort, "ssh", 2022, ``)
	flag.Parse()

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
		remote.Inbound(sshPort)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		remote.Outbound(httpPort)
	}()
	wg.Wait()
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
