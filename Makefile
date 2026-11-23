# Build Matty Plugin, the Mattermost plugin that is the Matty app's companion.
#
#   make dist          build everything and produce the installable .tar.gz
#   make lab           build dist/matty-lab.html, the panel in a browser
#   make check         both halves below
#   make check-server  go vet, the Go tests under -race (the stand-ins' own,
#                      and the plugin's against them, too, and the release
#                      checker's), gofmt, and that plugin.json and the server
#                      agree on the version
#   make check-webapp  the React rule, TypeScript, the stylesheet and its
#                      build-time squeeze (build/check-squeeze.mjs), the
#                      side-panel rules (build/check-away.mjs), Twitch's
#                      pure rules (build/check-twitch.mjs), the awards and
#                      the plan card (build/check-awards.mjs), the
#                      soundboard (build/check-sounds.mjs), @names in
#                      titles (build/check-mentions.mjs), the clip
#                      player (build/check-player.mjs), the panel's
#                      reads of the server (build/check-reads.mjs), the
#                      In voice row (build/check-voice.mjs) and the
#                      Updates card (build/check-update.mjs)
#   make e2e           the real-time WebRTC tests, which make check leaves out;
#                      make e2e E2EFLAGS=-race runs them under the race detector
#   make fakebb        build build/fakebb/fakebb, a stand-in for Broadcast Box
#                      with real WebRTC playback, and beside it for Steam and
#                      for Mumble, for developing the plugin and its clients
#                      (the web panel, the Matty app) on one machine
#   make fakebb-media  make the clips it plays, VP8 and H.264 (needs ffmpeg
#                      with libvpx and libx264); only those missing are made
#   make standin-linux the bundle for that machine and no other: built to ask
#                      fakebb's Steam (dist/standin/, never a release)
#   make clean         remove build output

GO ?= go
NPM ?= npm

PLUGIN_ID := $(shell python3 -c "import json;print(json.load(open('plugin.json'))['id'])")
PLUGIN_VERSION := $(shell python3 -c "import json;print(json.load(open('plugin.json'))['version'])")
BUNDLE := dist/$(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz

GOBUILD := CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w"

.PHONY: all dist dist-linux server server-linux webapp lab check check-server check-webapp e2e clean fakebb fakebb-media standin-linux

# No binary that is released may be the stand-in build (server/standin.go),
# which takes a stand-in's word for whose Steam account is whose. Its tag is
# never passed here; this holds it for a tag that came in some other way (the
# environment's GOFLAGS, say), by looking for that build's own sentence
# (standInSiteRefusal; TestStandInRefusalIsWhatTheReleaseGuardLooksFor holds
# the two together).
NO_STANDIN = @! grep -q -a -r "development build of Matty Plugin" server/dist || { rm -rf server/dist; echo "That was a stand-in build, which is never released, so it was deleted: build again without the villanet_standin tag"; exit 1; }

all: dist

server:
	@mkdir -p server/dist
	GOOS=linux   GOARCH=amd64 $(GOBUILD) -o server/dist/plugin-linux-amd64       ./server
	GOOS=linux   GOARCH=arm64 $(GOBUILD) -o server/dist/plugin-linux-arm64       ./server
	GOOS=darwin  GOARCH=amd64 $(GOBUILD) -o server/dist/plugin-darwin-amd64      ./server
	GOOS=darwin  GOARCH=arm64 $(GOBUILD) -o server/dist/plugin-darwin-arm64      ./server
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o server/dist/plugin-windows-amd64.exe ./server
	$(NO_STANDIN)

server-linux:
	rm -rf server/dist
	@mkdir -p server/dist
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o server/dist/plugin-linux-amd64 ./server
	$(NO_STANDIN)

webapp:
	cd webapp && $(NPM) install --no-audit --no-fund
	cd webapp && $(NPM) run build

dist: server webapp
	rm -rf dist/$(PLUGIN_ID)
	mkdir -p dist/$(PLUGIN_ID)
	cp plugin.json dist/$(PLUGIN_ID)/
	cp -r assets dist/$(PLUGIN_ID)/
	mkdir -p dist/$(PLUGIN_ID)/server dist/$(PLUGIN_ID)/webapp
	cp -r server/dist dist/$(PLUGIN_ID)/server/dist
	cp -r webapp/dist dist/$(PLUGIN_ID)/webapp/dist
	cd dist && tar -czf $(notdir $(BUNDLE)) $(PLUGIN_ID)
	rm -rf dist/$(PLUGIN_ID)
	@echo
	@echo "Built $(BUNDLE)"

# A much smaller bundle for a Linux amd64 server, which is the usual case.
dist-linux: server-linux webapp
	rm -rf dist/$(PLUGIN_ID)
	mkdir -p dist/$(PLUGIN_ID)/server dist/$(PLUGIN_ID)/webapp
	python3 -c "import json; m=json.load(open('plugin.json')); m['server']['executables']={'linux-amd64':'server/dist/plugin-linux-amd64'}; json.dump(m, open('dist/$(PLUGIN_ID)/plugin.json','w'), indent=4)"
	cp -r assets dist/$(PLUGIN_ID)/
	cp -r server/dist dist/$(PLUGIN_ID)/server/dist
	cp -r webapp/dist dist/$(PLUGIN_ID)/webapp/dist
	cd dist && tar -czf $(PLUGIN_ID)-$(PLUGIN_VERSION)-linux-amd64.tar.gz $(PLUGIN_ID)
	rm -rf dist/$(PLUGIN_ID)
	@echo
	@echo "Built dist/$(PLUGIN_ID)-$(PLUGIN_VERSION)-linux-amd64.tar.gz"

# The plugin for a developer's machine, and never a release: built with the
# villanet_standin tag, so that Steam is the stand-in beside it (build/fakebb;
# server/standin.go says what changes, which is Steam's two hosts and nothing
# else). It is kept apart from the bundles that are released: under
# dist/standin, with "standin" in its file name and in the version Mattermost
# shows for it, and built straight into its own folder, never through
# server/dist. It only starts where the Site URL is this machine.
standin-linux: webapp
	rm -rf dist/standin
	mkdir -p dist/standin/$(PLUGIN_ID)/server/dist dist/standin/$(PLUGIN_ID)/webapp
	GOOS=linux GOARCH=amd64 $(GOBUILD) -tags villanet_standin -o dist/standin/$(PLUGIN_ID)/server/dist/plugin-linux-amd64 ./server
	python3 -c "import json; m=json.load(open('plugin.json')); m['version']+='+standin'; m['server']['executables']={'linux-amd64':'server/dist/plugin-linux-amd64'}; json.dump(m, open('dist/standin/$(PLUGIN_ID)/plugin.json','w'), indent=4)"
	cp -r assets dist/standin/$(PLUGIN_ID)/
	cp -r webapp/dist dist/standin/$(PLUGIN_ID)/webapp/dist
	cd dist/standin && tar -czf $(PLUGIN_ID)-$(PLUGIN_VERSION)-standin-linux-amd64.tar.gz $(PLUGIN_ID)
	rm -rf dist/standin/$(PLUGIN_ID)
	@echo
	@echo "Built dist/standin/$(PLUGIN_ID)-$(PLUGIN_VERSION)-standin-linux-amd64.tar.gz (for this machine only: never for a server people use)"

# The panel, in a browser, with a fake server and no Mattermost. One HTML file
# that opens from the filesystem; see build/build-lab.py. It runs on the React
# Mattermost ships at min_server_version, as the plugin does; see
# build/check-react.py.
lab:
	cd webapp && $(NPM) install --no-audit --no-fund
	cd webapp && npx webpack --config lab/webpack.config.js
	python3 build/build-lab.py

# Split in two so that whoever is working on one half can check it without
# the other half's work in progress failing them. check-version.py reads only
# plugin.json and server/plugin.go, so it goes with the server.
check: check-server check-webapp

# The stand-ins (build/fakebb) are checked with the server they stand beside:
# their own tests, and, under the tag the stand-in build is made with, the
# plugin's own code against them (server/standin_test.go).
check-server:
	$(GO) vet ./server/... ./build/fakebb/... ./build/checkrelease/...
	$(GO) vet -tags villanet_standin ./server/...
	$(GO) test -race ./server/... ./build/fakebb/... ./build/checkrelease/...
	$(GO) test -race -tags villanet_standin -run '^TestStandIn' ./server/...
	@test -z "$$(gofmt -l server/ build/fakebb/ build/checkrelease/)" || (echo "gofmt needed:"; gofmt -l server/ build/fakebb/ build/checkrelease/; exit 1)
	python3 build/check-version.py

check-webapp:
	python3 build/check-react.py
	cd webapp && npx tsc --noEmit -p tsconfig.json
	python3 build/check-styles.py
	node build/check-squeeze.mjs
	node build/check-away.mjs
	node build/check-twitch.mjs
	node build/check-accounts.mjs
	node build/check-awards.mjs
	node build/check-sounds.mjs
	node build/check-mentions.mjs
	node build/check-player.mjs
	node build/check-reads.mjs
	node build/check-voice.mjs
	node build/check-update.mjs

# Real WebRTC in real time, which under the race detector is flaky rather than
# wrong, so it is not part of check. Each such test skips itself unless
# VILLANET_E2E=1, and -count=1 stops a cached pass from standing in for a run.
# E2EFLAGS passes more flags to go test, -race or -v; under -race the tests
# allow themselves longer.
E2EFLAGS ?=

e2e:
	VILLANET_E2E=1 $(GO) test $(E2EFLAGS) -count=1 -run 'E2E' ./server/...

# The stand-in for Broadcast Box, and beside it for Steam and for Mumble
# (build/fakebb/main.go and build/fakebb/standin say what each answers).
# Never part of the bundle: dist copies nothing from build/.
fakebb:
	$(GO) build -o build/fakebb/fakebb ./build/fakebb

# The clip every viewer of the stand-in is sent: a test card and a tone. The
# picture is made twice, the same 12 seconds each time so that the one sound
# loop fits under both: as VP8, so that an emulator with no hardware decoder
# can play it too, and as H.264, the only video the plugin's clip recorder
# records, in what OBS sends Broadcast Box by WHIP (constrained baseline,
# level 3.1, no B-frames, a keyframe every second with its SPS and PPS before
# it). Only a file that is missing is made, so a checkout with the older two
# gains the third; each is written beside itself first, so a run that stops
# half way leaves nothing that looks made.
FAKEBB_MEDIA := build/fakebb/media/stream.ivf build/fakebb/media/stream.h264 build/fakebb/media/stream.ogg

fakebb-media: $(FAKEBB_MEDIA)

build/fakebb/media/stream.ivf:
	@mkdir -p build/fakebb/media
	ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=30" -t 12 -c:v libvpx -b:v 1200k -g 30 -keyint_min 30 -deadline realtime -cpu-used 8 -f ivf $@.part
	mv $@.part $@

build/fakebb/media/stream.h264:
	@mkdir -p build/fakebb/media
	ffmpeg -loglevel error -y -f lavfi -i "testsrc2=size=1280x720:rate=30" -t 12 -c:v libx264 -profile:v baseline -level:v 3.1 -pix_fmt yuv420p -preset veryfast -b:v 1200k -maxrate 1500k -bufsize 1500k -g 30 -keyint_min 30 -sc_threshold 0 -bf 0 -x264-params repeat-headers=1 -f h264 $@.part
	mv $@.part $@

build/fakebb/media/stream.ogg:
	@mkdir -p build/fakebb/media
	ffmpeg -loglevel error -y -f lavfi -i "sine=frequency=440:sample_rate=48000" -t 12 -ac 2 -c:a libopus -b:a 64k -page_duration 20000 -f ogg $@.part
	mv $@.part $@

# server/server is what a bare `go build` inside server/ leaves behind.
clean:
	rm -rf dist server/dist server/server webapp/dist webapp/lab/dist build/fakebb/fakebb build/fakebb/media
