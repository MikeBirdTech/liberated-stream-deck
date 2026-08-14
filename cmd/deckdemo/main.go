package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"

	// Register the two decoders the bridge accepts for opaque raster frames
	// (key, touch-strip, region, and boot_image payloads).
	_ "image/jpeg"
	_ "image/png"

	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/render"
	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const (
	inputReadTimeout           = 50 * time.Millisecond
	reconnectInterval          = time.Second
	bridgeFallbackPollInterval = 5 * time.Second
)

var displayModes = [...]string{"INPUT", "KEY TEST", "TOUCH TEST"}

type demoState struct {
	keys        [streamdeck.KeyCount]bool
	counter     int
	brightness  int
	selectedKey int
	mode        int
	latestInput string
	latestTouch *streamdeck.TouchEvent

	remoteTheme      string
	remoteTitle      string
	remoteMessage    string
	remoteEventsSeen int
	remoteLast       string

	// revision-2 bridge mode: the controller owns all state and semantics. The deck is
	// a pure renderer - it paints exactly what the server sends (GET poll and
	// POST acks) and never interprets a key/dial/flick meaning locally.
	bridge       bool
	activeKey    *remoteKey
	activeStrip  *remoteStrip
	background   []remoteKey
	pollMS       int
	bootImageRev int

	// frames caches decoded key raster frames by the server's image revision.
	// A nil entry records a payload that failed to decode, so a broken frame
	// is logged once instead of on every poll repaint.
	frames map[string]image.Image

	// stripOutput caches decoded touch-strip frames across connections while
	// keeping desired and displayed output state serialized and connection-local.
	stripOutput stripOutputState
}

// frameCacheLimit bounds each decoded-frame cache. The key cache resets on
// overflow; the touch-strip cache evicts its oldest revision.
const frameCacheLimit = 64

// bootImageDeck is implemented by devices that can persist a power-on frame.
type bootImageDeck interface {
	UploadBootImage(image.Image) error
}

// pollResult carries one bridge-mode poll response from the background poller
// into the main event loop.
type pollResult struct {
	demo remoteDemo
	err  error
}

var (
	// bridgePaperBG is the quiet paper fill (#F6F5EE) for unlabeled keys and
	// the fallback background when the server's bg cannot be parsed.
	bridgePaperBG = color.NRGBA{R: 0xf6, G: 0xf5, B: 0xee, A: 255}
	// bridgePaperInk is the fallback foreground (#272C24) when the server's
	// fg cannot be parsed.
	bridgePaperInk = color.NRGBA{R: 0x27, G: 0x2c, B: 0x24, A: 255}
)

// parseHexColor parses "#RRGGBB" (the '#' is optional) into an opaque color.
func parseHexColor(s string) (color.Color, error) {
	hex := strings.TrimPrefix(s, "#")
	if len(hex) != 6 {
		return nil, fmt.Errorf("expected #RRGGBB, got %q", s)
	}
	var values [3]uint8
	for i := 0; i < 3; i++ {
		value, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", s, err)
		}
		values[i] = uint8(value)
	}
	return color.NRGBA{R: values[0], G: values[1], B: values[2], A: 255}, nil
}

// keyRenderColors resolves the wire bg/fg hex colors for a server key, falling
// back to quiet paper/ink on unparseable values.
func keyRenderColors(key *remoteKey) (bg, fg color.Color) {
	bg = bridgePaperBG
	fg = bridgePaperInk
	if key == nil {
		return
	}
	if key.BG != "" {
		if parsed, err := parseHexColor(key.BG); err == nil {
			bg = parsed
		} else {
			log.Printf("bridge key bg %q ignored: %v", key.BG, err)
		}
	}
	if key.FG != "" {
		if parsed, err := parseHexColor(key.FG); err == nil {
			fg = parsed
		} else {
			log.Printf("bridge key fg %q ignored: %v", key.FG, err)
		}
	}
	return
}

type demoDeck interface {
	Info() (streamdeck.DeviceInfo, error)
	ReadEvents(time.Duration) (streamdeck.InputRead, error)
	SetBrightness(int) error
	SetKeyImage(int, image.Image) error
	Close() error
}

type openDeckFunc func() (demoDeck, error)
type fetchDemoFunc func(context.Context) (remoteDemo, error)

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Printf("deckdemo: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGINT (terminal) and SIGTERM (launchd bootout / logout / reboot) both
	// count as a clean shutdown so the server background can be persisted to
	// the device before exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := demoState{brightness: 70}
	state.stripOutput.configure(touchStripMinimumInterval(), defaultTouchStripFailureDelay)
	var fetchDemo fetchDemoFunc
	if controllerBaseURL != "" {
		fetchDemo = func(ctx context.Context) (remoteDemo, error) {
			return fetchRemoteDemo(ctx, demoHTTPClient, controllerBaseURL)
		}
	}
	return runDemo(
		ctx,
		&state,
		func() (demoDeck, error) { return streamdeck.OpenAny() },
		fetchDemo,
		reconnectInterval,
	)
}

func runDemo(ctx context.Context, state *demoState, open openDeckFunc, fetchDemo fetchDemoFunc, retryInterval time.Duration) error {
	for {
		if ctx.Err() != nil {
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		deck, err := open()
		if err != nil {
			log.Printf("device unavailable retry=%s error=%q", retryInterval, err)
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		info, setupErr := deck.Info()
		var remote remoteDemo
		if setupErr == nil {
			if fetchDemo == nil {
				// No remote controller configured: revision-0 state keeps the
				// classic rev-1 local render without any network I/O.
				applyRemoteDemo(state, remoteDemo{})
			} else {
				var fetchErr error
				remote, fetchErr = fetchDemo(ctx)
				if fetchErr != nil {
					// A dead endpoint must never strand a newly started deck dark.
					// Render locally until the connection-local poller recovers the
					// controller. If bridge state already exists, retain it instead.
					if !state.bridge {
						applyRemoteDemo(state, remoteDemo{})
					}
					mode := "rev1-local"
					if state.bridge {
						mode = "bridge-retained"
					}
					log.Printf("demo endpoint unavailable mode=%s error=%q", mode, fetchErr)
				} else {
					applyRemoteDemo(state, remote)
				}
			}
		}
		if setupErr == nil {
			state.stripOutput.startConnection()
			setupErr = restoreDemo(deck, state, info.Model)
		}
		if setupErr == nil {
			// the controller-owned boot frame: upload on revision change (non-fatal).
			if bootErr := maybeUploadBootImage(deck, state, remote.BootImage); bootErr != nil {
				log.Printf("boot image error=%q", bootErr)
			}
		}
		if setupErr != nil {
			log.Printf("device setup failed retry=%s error=%q", retryInterval, setupErr)
			if closeErr := deck.Close(); closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			if !waitForReconnect(ctx, retryInterval) {
				log.Print("device shutdown reason=interrupt")
				return nil
			}
			continue
		}

		mode := "rev1"
		if state.bridge {
			mode = "bridge"
		}
		log.Printf("demo endpoint connected revision=%d mode=%s theme=%q events_seen=%d", remote.Revision, mode, remote.Presentation.Theme, remote.EventsSeen)
		log.Printf("device connected vid=%04x pid=%04x product=%q model=%q", info.VendorID, info.ProductID, info.Product, info.Model)
		if state.bridge && state.activeKey != nil && state.activeStrip != nil {
			log.Printf("bridge view key[%d]=%q state=%q strip=%q page=%d/%d poll_ms=%d background_keys=%d",
				state.activeKey.Index, state.activeKey.Label, state.activeKey.State,
				state.activeStrip.Title, state.activeStrip.Page, state.activeStrip.Pages, state.pollMS, len(state.background))
		}
		if info.Model.HasTouchStrip() {
			log.Printf("output restored keys=1-8 strip=800x100 brightness=%d", state.brightness)
		} else {
			width, height := info.Model.KeyImageSize()
			log.Printf("output restored keys=1-%d key-size=%dx%d brightness=%d", info.Model.KeyCount(), width, height, state.brightness)
		}

		connectionErr := runConnected(ctx, deck, state, info.Model, fetchDemo)
		if ctx.Err() != nil && state.bridge {
			// Best-effort: repaint every key from the server background so the
			// frames persisted in the device (shown at next power-on) are
			// the controller-owned rather than whatever was last flashed.
			if persistErr := renderBackgroundKeys(deck, state, info.Model); persistErr != nil {
				log.Printf("background persist error=%q", persistErr)
			}
		}
		closeErr := deck.Close()
		if ctx.Err() != nil {
			if closeErr != nil {
				log.Printf("device close error=%q", closeErr)
			}
			log.Print("device shutdown reason=interrupt")
			return nil
		}

		log.Printf("device disconnected retry=%s error=%q", retryInterval, connectionErr)
		if closeErr != nil {
			log.Printf("device close error=%q", closeErr)
		}
		if !waitForReconnect(ctx, retryInterval) {
			log.Print("device shutdown reason=interrupt")
			return nil
		}
	}
}

func runConnected(ctx context.Context, deck demoDeck, state *demoState, model streamdeck.Model, fetchDemo fetchDemoFunc) error {
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	eventResults := make(chan eventPostResult, 64)

	var pollResults chan pollResult
	var stopPoll func()
	if fetchDemo != nil {
		pollResults = make(chan pollResult, 1)
		stopPoll = startBridgePoll(connectionCtx, state, fetchDemo, pollResults)
		defer stopPoll()
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := flushStrip(deck, state, model); err != nil {
			return err
		}
		select {
		case result := <-eventResults:
			if result.err != nil {
				log.Printf("demo event error=%q", result.err)
				continue
			}
			if err := handleAck(deck, state, model, result.ack); err != nil {
				return err
			}
			continue
		case poll := <-pollResults:
			if err := handlePollResult(deck, state, model, poll); err != nil {
				return err
			}
			continue
		default:
		}

		result, err := deck.ReadEvents(inputReadTimeout)
		if errors.Is(err, streamdeck.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		logInputDiagnostics(result)
		for _, event := range result.Events {
			if state.bridge {
				// Pure renderer: no local interpretation or rerender. Log the
				// raw event, POST it, and let the ack/poll bring the new
				// server-owned state.
				logBridgeInput(state, event)
			} else if err := handleEvent(deck, state, model, event); err != nil {
				return err
			}
			if payload, ok := remoteEvent(event); ok {
				postEventAsync(connectionCtx, payload, eventResults)
			}
		}
	}
}

// startBridgePoll launches the controller poller and returns a stop function.
// Bridge responses choose the steady-state cadence; local fallback retries on
// a modest fixed cadence until a valid bridge response arrives.
func startBridgePoll(ctx context.Context, state *demoState, fetchDemo fetchDemoFunc, results chan<- pollResult) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := time.Duration(state.pollMS) * time.Millisecond
		if interval <= 0 {
			interval = bridgeFallbackPollInterval
		}
		for {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
			demo, err := fetchDemo(ctx)
			if err == nil && isBridgeDemo(demo) {
				interval = time.Duration(demo.PollMS) * time.Millisecond
			}
			select {
			case results <- pollResult{demo: demo, err: err}:
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func handlePollResult(deck demoDeck, state *demoState, model streamdeck.Model, poll pollResult) error {
	if poll.err != nil {
		log.Printf("demo poll error=%q", poll.err)
		return nil
	}
	return handlePoll(deck, state, model, poll.demo)
}

// handleAck repaints from an event POST ack. revision-2 acks carry a state
// object with the server's resulting key/strip, allowing an immediate
// render without waiting for the next poll.
func handleAck(deck demoDeck, state *demoState, model streamdeck.Model, ack eventAck) error {
	state.remoteEventsSeen = ack.EventsSeen
	state.remoteLast = lastEventMessage(ack.Message)
	if ack.State != nil {
		if ack.State.Key != nil {
			state.activeKey = ack.State.Key
		}
		if ack.State.Strip != nil {
			state.activeStrip = ack.State.Strip
		}
	}
	log.Printf("demo ack events_seen=%d message=%q", ack.EventsSeen, ack.Message)
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	if state.bridge && state.activeKey != nil {
		if err := renderKey(deck, state, model, state.activeKey.Index); err != nil {
			return err
		}
	}
	return nil
}

// handlePoll applies one bridge-mode GET and re-renders from the server state.
// Invalid responses leave either the local fallback or the last-known bridge
// render untouched; the poller will keep trying at its current cadence.
func handlePoll(deck demoDeck, state *demoState, model streamdeck.Model, demo remoteDemo) error {
	if !isBridgeDemo(demo) {
		return nil
	}
	wasBridge := state.bridge
	previousBrightness := state.brightness
	if err := applyRemoteDemo(state, demo); err != nil {
		return err
	}
	if err := maybeUploadBootImage(deck, state, demo.BootImage); err != nil {
		log.Printf("boot image error=%q", err)
	}
	if !wasBridge {
		log.Printf("demo endpoint recovered revision=%d mode=bridge poll_ms=%d", demo.Revision, demo.PollMS)
		return restoreDemo(deck, state, model)
	}
	if state.brightness != previousBrightness {
		if err := deck.SetBrightness(state.brightness); err != nil {
			return fmt.Errorf("poll brightness: %w", err)
		}
	}
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	if state.activeKey != nil {
		if err := renderKey(deck, state, model, state.activeKey.Index); err != nil {
			return err
		}
	}
	return nil
}

func logBridgeInput(state *demoState, event streamdeck.Event) {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		state.latestInput = fmt.Sprintf("KEY %d %s", event.Key+1, pressedLabel(event.Pressed))
	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
		state.latestInput = fmt.Sprintf("D%d %+03d", event.Dial+1, event.Delta)
	case streamdeck.DialPressEvent:
		log.Printf("input dial dial=%d pressed=%t", event.Dial+1, event.Pressed)
		state.latestInput = fmt.Sprintf("D%d %s", event.Dial+1, pressedLabel(event.Pressed))
	case streamdeck.TouchEvent:
		logTouch(event)
		state.latestInput = touchInputSummary(event)
		state.latestTouch = &event
	}
}

func waitForReconnect(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func restoreDemo(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("restore brightness: %w", err)
	}
	for index := 0; index < model.KeyCount(); index++ {
		if err := renderKey(deck, state, model, index); err != nil {
			return err
		}
	}
	if err := renderStrip(deck, state, model); err != nil {
		return err
	}
	return nil
}

func applyRemoteDemo(state *demoState, demo remoteDemo) error {
	state.remoteEventsSeen = demo.EventsSeen
	state.remoteLast = ""
	if demo.LastEvent != nil {
		state.remoteLast = lastEventMessage(demo.LastEvent.Summary)
	}
	// Mode is chosen from revision, never from the command string: the
	// command field stays "run_hardware_demo" forever as deliberate backward
	// compatibility, so it is ignored here.
	if isBridgeDemo(demo) {
		state.bridge = true
		state.activeKey = demo.Key
		state.activeStrip = demo.Strip
		state.pollMS = demo.PollMS
		if demo.Background != nil {
			state.background = demo.Background.Keys
		} else {
			// A response without a background means quiet paper; never render
			// stale frames from an earlier response.
			state.background = nil
		}
		if demo.Presentation.Brightness > 0 {
			state.brightness = demo.Presentation.Brightness
		}
		return nil
	}
	// revision < 2 or missing bridge fields: keep the classic rev-1 demo.
	state.bridge = false
	state.activeKey = nil
	state.activeStrip = nil
	state.background = nil
	state.pollMS = 0
	if demo.Presentation.Brightness > 0 {
		state.brightness = demo.Presentation.Brightness
	}
	state.remoteTheme = demo.Presentation.Theme
	state.remoteTitle = demo.Presentation.Title
	state.remoteMessage = demo.Presentation.Message
	return nil
}

func isBridgeDemo(demo remoteDemo) bool {
	return demo.Revision >= 2 && demo.Key != nil && demo.Strip != nil && demo.PollMS > 0
}

func handleEvent(deck demoDeck, state *demoState, model streamdeck.Model, event streamdeck.Event) error {
	switch event := event.(type) {
	case streamdeck.KeyEvent:
		if event.Key < 0 || event.Key >= model.KeyCount() {
			return fmt.Errorf("key event index %d out of range for %s", event.Key, model)
		}
		log.Printf("input key key=%d pressed=%t", event.Key+1, event.Pressed)
		state.latestInput = fmt.Sprintf("KEY %d %s", event.Key+1, pressedLabel(event.Pressed))
		if event.Pressed {
			state.keys[event.Key] = !state.keys[event.Key]
			if err := renderKey(deck, state, model, event.Key); err != nil {
				return err
			}
			if model == streamdeck.ModelMini && (event.Key == 4 || event.Key == 5) {
				delta := -10
				if event.Key == 5 {
					delta = 10
				}
				state.brightness = clamp(state.brightness+delta, 0, 100)
				if err := deck.SetBrightness(state.brightness); err != nil {
					return err
				}
				log.Printf("output brightness=%d", state.brightness)
			}
		}
		return renderStrip(deck, state, model)

	case streamdeck.DialRotateEvent:
		log.Printf("input dial dial=%d delta=%+d", event.Dial+1, event.Delta)
		state.latestInput = fmt.Sprintf("D%d %+03d", event.Dial+1, event.Delta)
		switch event.Dial {
		case 0:
			state.counter += event.Delta
		case 1:
			next := clamp(state.brightness+event.Delta*5, 0, 100)
			if next != state.brightness {
				state.brightness = next
				if err := deck.SetBrightness(state.brightness); err != nil {
					return err
				}
			}
		case 2:
			previous := state.selectedKey
			state.selectedKey = wrap(state.selectedKey+event.Delta, streamdeck.KeyCount)
			if previous != state.selectedKey {
				if err := renderKey(deck, state, model, previous); err != nil {
					return err
				}
				if err := renderKey(deck, state, model, state.selectedKey); err != nil {
					return err
				}
			}
		case 3:
			state.mode = wrap(state.mode+event.Delta, len(displayModes))
		}
		return renderStrip(deck, state, model)

	case streamdeck.DialPressEvent:
		log.Printf("input dial dial=%d pressed=%t", event.Dial+1, event.Pressed)
		state.latestInput = fmt.Sprintf("D%d %s", event.Dial+1, pressedLabel(event.Pressed))
		if !event.Pressed {
			return renderStrip(deck, state, model)
		}
		switch event.Dial {
		case 0:
			state.counter = 0
		case 1:
			if state.brightness == 15 {
				state.brightness = 70
			} else {
				state.brightness = 15
			}
			if err := deck.SetBrightness(state.brightness); err != nil {
				return err
			}
		case 2:
			state.keys[state.selectedKey] = !state.keys[state.selectedKey]
			if err := renderKey(deck, state, model, state.selectedKey); err != nil {
				return err
			}
		case 3:
			state.mode = 0
		}
		return renderStrip(deck, state, model)

	case streamdeck.TouchEvent:
		logTouch(event)
		state.latestInput = touchInputSummary(event)
		state.latestTouch = &event
		return renderStrip(deck, state, model)

	default:
		return fmt.Errorf("unsupported normalized event %T", event)
	}
}

func renderKey(deck demoDeck, state *demoState, model streamdeck.Model, index int) error {
	if state.bridge {
		// Pure renderer: paint exactly what the server sent. The active key
		// carries its wire frame; every other key paints the server
		// background frame for its index, or quiet paper when the server
		// provides none.
		key := state.activeKey
		if key == nil || key.Index != index {
			key = backgroundFrame(state.background, index)
		}
		return renderWireKey(deck, state, model, index, key)
	}
	width, height := model.KeyImageSize()
	view := render.KeyView{Index: index, On: state.keys[index], Selected: index == state.selectedKey}
	img := render.KeySize(view, width, height)
	if err := deck.SetKeyImage(index, img); err != nil {
		return fmt.Errorf("render key %d: %w", index+1, err)
	}
	return nil
}

// renderWireKey paints one server-owned key. A decodable raster frame is
// scaled to the native key size and painted verbatim; otherwise the semantic
// label/bg/fg fallback is drawn (quiet paper when key is nil).
func renderWireKey(deck demoDeck, state *demoState, model streamdeck.Model, index int, key *remoteKey) error {
	width, height := model.KeyImageSize()
	var img image.Image
	if frame := state.imageFrame(key); frame != nil {
		img = streamdeck.ScaleImage(frame, width, height)
	} else {
		view := render.KeyView{Index: index, BG: bridgePaperBG}
		if key != nil {
			view.Label = key.Label
			view.BG, view.FG = keyRenderColors(key)
		}
		img = render.KeySize(view, width, height)
	}
	if err := deck.SetKeyImage(index, img); err != nil {
		return fmt.Errorf("render key %d: %w", index+1, err)
	}
	return nil
}

// imageFrame returns the decoded raster frame carried by a wire key, or nil
// when the key has no image or its payload cannot be decoded — the caller
// then falls back to the semantic label/bg/fg rendering. Decoded frames are
// cached by the server's content revision (including failures); a payload
// without a revision is decoded every time rather than cached.
func (s *demoState) imageFrame(key *remoteKey) image.Image {
	if key == nil || key.Image == nil || key.Image.Data == "" {
		return nil
	}
	revision := key.Image.Revision
	if revision != "" {
		if frame, cached := s.frames[revision]; cached {
			return frame
		}
	}
	frame, err := decodeImageFrame(key.Image)
	if err != nil {
		log.Printf("bridge key %d image ignored: %v", key.Index+1, err)
	}
	if revision != "" {
		if s.frames == nil || len(s.frames) >= frameCacheLimit {
			s.frames = make(map[string]image.Image)
		}
		s.frames[revision] = frame
	}
	return frame
}

// decodeImageFrame decodes a wire image payload (base64-encoded PNG or JPEG).
func decodeImageFrame(img *remoteImage) (image.Image, error) {
	raw, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return nil, fmt.Errorf("decode image base64: %w", err)
	}
	frame, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return frame, nil
}

// backgroundFrame returns the server background frame for a key index, if any.
func backgroundFrame(frames []remoteKey, index int) *remoteKey {
	for i := range frames {
		if frames[i].Index == index {
			return &frames[i]
		}
	}
	return nil
}

// renderBackgroundKeys repaints every key from the server background (or quiet
// paper when absent) and restores brightness. Used at clean shutdown so the
// frames persisted in the device are the controller-owned at the next power-on.
func renderBackgroundKeys(deck demoDeck, state *demoState, model streamdeck.Model) error {
	if err := deck.SetBrightness(state.brightness); err != nil {
		return fmt.Errorf("persist background brightness: %w", err)
	}
	for index := 0; index < model.KeyCount(); index++ {
		if err := renderWireKey(deck, state, model, index, backgroundFrame(state.background, index)); err != nil {
			return fmt.Errorf("persist background: %w", err)
		}
	}
	log.Printf("persisted %d background key frames for next boot", model.KeyCount())
	return nil
}

// maybeUploadBootImage persists a server-provided power-on frame when its
// revision is new. Non-fatal: failures are logged and the previous frame
// remains.
func maybeUploadBootImage(deck demoDeck, state *demoState, boot *remoteBootImage) error {
	if boot == nil || boot.Revision == state.bootImageRev || boot.Data == "" {
		return nil
	}
	uploader, ok := deck.(bootImageDeck)
	if !ok {
		return fmt.Errorf("device does not support boot image upload")
	}
	raw, err := base64.StdEncoding.DecodeString(boot.Data)
	if err != nil {
		return fmt.Errorf("decode boot image: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode boot image: %w", err)
	}
	if err := uploader.UploadBootImage(img); err != nil {
		return fmt.Errorf("upload boot image: %w", err)
	}
	state.bootImageRev = boot.Revision
	log.Printf("boot image persisted revision=%d source=%dx%d", boot.Revision, img.Bounds().Dx(), img.Bounds().Dy())
	return nil
}

func logInputDiagnostics(result streamdeck.InputRead) {
	for _, diagnostic := range result.Diagnostics {
		log.Printf("input diagnostic=%q", diagnostic)
	}
}

func logTouch(event streamdeck.TouchEvent) {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		log.Printf("input touch kind=%s x=%d y=%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		log.Printf("input touch kind=FLICK start=%d,%d end=%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	}
}

func touchInputSummary(event streamdeck.TouchEvent) string {
	switch event.Kind {
	case streamdeck.TouchTap, streamdeck.TouchPress:
		return fmt.Sprintf("%s %d,%d", event.Kind, event.X, event.Y)
	case streamdeck.TouchFlick:
		return fmt.Sprintf("FLICK %d,%d>%d,%d", event.StartX, event.StartY, event.EndX, event.EndY)
	default:
		return "TOUCH"
	}
}

func pressedLabel(pressed bool) string {
	if pressed {
		return "DOWN"
	}
	return "UP"
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func wrap(value, size int) int {
	return ((value % size) + size) % size
}
