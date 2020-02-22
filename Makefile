bin/inunaki: bin/inunaki.raw bin/assets.zip
	cat $^ > $@
	zip -A $@
	chmod +x $@

bin/inunaki.raw: $(wildcard *.go) $(wildcard cmd/inunaki/*.go) go.sum bin
	go build -o $@ cmd/inunaki/*.go

bin/assets.zip: assets bin
	cd assets; zip -r ../bin/assets.zip .

go.sum: go.mod
	go mod tidy

assets:
	mkdir -p assets
	ssh-keygen -b 4096 -t rsa -N '' -f assets/inunaki.rsa

bin:
	mkdir -p bin

.PHONY: clean
clean:
	rm -rf assets bin
