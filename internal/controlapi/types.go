package controlapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"

	"github.com/MikeBirdTech/liberated-stream-deck/internal/streamdeck"
)

const (
	// APIVersion is the version of the application-facing wire contract.
	APIVersion = "v1"

	maxImageBytes       = 16 << 20
	maxCommandsPerBatch = 64
)

type CommandType string

const (
	CommandSetBrightness  CommandType = "set_brightness"
	CommandSetKeyImage    CommandType = "set_key_image"
	CommandSetLCDImage    CommandType = "set_lcd_image"
	CommandSetTouchStrip  CommandType = "set_touch_strip_image"
	CommandSetTouchRegion CommandType = "set_touch_strip_region"
)

// ImagePayload is an opaque client-rendered PNG or JPEG. The service validates
// native dimensions but never adds labels, layouts, or other presentation.
type ImagePayload struct {
	Revision string `json:"revision,omitempty"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data_base64"`
}

// Command describes one hardware operation. Fields are interpreted solely by
// Type; unused fields must be omitted by clients for a stable, explicit wire
// shape.
type Command struct {
	Type       CommandType   `json:"type"`
	Index      *int          `json:"index,omitempty"`
	Brightness *int          `json:"brightness,omitempty"`
	X          *int          `json:"x,omitempty"`
	Y          *int          `json:"y,omitempty"`
	Image      *ImagePayload `json:"image,omitempty"`
}

type CommandBatch struct {
	RequestID string    `json:"request_id,omitempty"`
	Commands  []Command `json:"commands"`
}

type CommandAck struct {
	APIVersion string `json:"api_version"`
	RequestID  string `json:"request_id,omitempty"`
	Status     string `json:"status"`
	Generation uint64 `json:"generation"`
	Connected  bool   `json:"connected"`
	Message    string `json:"message,omitempty"`
}

type SurfaceCapabilities struct {
	Width          int  `json:"width"`
	Height         int  `json:"height"`
	PartialUpdates bool `json:"partial_updates,omitempty"`
}

type DeviceCapabilities struct {
	Model             string               `json:"model"`
	KeyCount          int                  `json:"key_count"`
	Key               SurfaceCapabilities  `json:"key"`
	DialCount         int                  `json:"dial_count"`
	LCD               *SurfaceCapabilities `json:"lcd,omitempty"`
	TouchStrip        *SurfaceCapabilities `json:"touch_strip,omitempty"`
	InputEvents       []string             `json:"input_events"`
	SupportedCommands []CommandType        `json:"supported_commands"`
}

type ConnectedDevice struct {
	VendorID     uint16 `json:"vendor_id"`
	ProductID    uint16 `json:"product_id"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
}

type DesiredState struct {
	Brightness         *int           `json:"brightness,omitempty"`
	Keys               map[int]string `json:"keys,omitempty"`
	LCDRevision        string         `json:"lcd_revision,omitempty"`
	TouchStripRevision string         `json:"touch_strip_revision,omitempty"`
}

type Snapshot struct {
	APIVersion   string             `json:"api_version"`
	Connected    bool               `json:"connected"`
	Capabilities DeviceCapabilities `json:"capabilities"`
	Device       *ConnectedDevice   `json:"device,omitempty"`
	Generation   uint64             `json:"generation"`
	Desired      DesiredState       `json:"desired"`
	LastError    string             `json:"last_error,omitempty"`
}

// Event is a normalized device-to-application message. Pointer scalar fields
// preserve meaningful zero values, including key/dial index zero and x=0.
type Event struct {
	Sequence  uint64           `json:"sequence"`
	Timestamp string           `json:"timestamp"`
	Kind      string           `json:"kind"`
	Index     *int             `json:"index,omitempty"`
	Pressed   *bool            `json:"pressed,omitempty"`
	Delta     *int             `json:"delta,omitempty"`
	X         *int             `json:"x,omitempty"`
	Y         *int             `json:"y,omitempty"`
	StartX    *int             `json:"start_x,omitempty"`
	StartY    *int             `json:"start_y,omitempty"`
	EndX      *int             `json:"end_x,omitempty"`
	EndY      *int             `json:"end_y,omitempty"`
	Device    *ConnectedDevice `json:"device,omitempty"`
	Message   string           `json:"message,omitempty"`
}

type operation struct {
	typeName   CommandType
	index      int
	brightness int
	x          int
	y          int
	image      *image.NRGBA
	revision   string
}

func decodeOperations(model streamdeck.Model, commands []Command) ([]operation, error) {
	if len(commands) == 0 {
		return nil, fmt.Errorf("commands must contain at least one command")
	}
	if len(commands) > maxCommandsPerBatch {
		return nil, fmt.Errorf("commands contains %d entries, maximum is %d", len(commands), maxCommandsPerBatch)
	}

	operations := make([]operation, 0, len(commands))
	for index, command := range commands {
		operation, err := decodeOperation(model, command)
		if err != nil {
			return nil, fmt.Errorf("commands[%d]: %w", index, err)
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func decodeOperation(model streamdeck.Model, command Command) (operation, error) {
	op := operation{typeName: command.Type}
	switch command.Type {
	case CommandSetBrightness:
		if command.Brightness == nil {
			return operation{}, fmt.Errorf("brightness is required")
		}
		if command.Index != nil || command.X != nil || command.Y != nil || command.Image != nil {
			return operation{}, fmt.Errorf("set_brightness accepts only brightness")
		}
		if *command.Brightness < 0 || *command.Brightness > 100 {
			return operation{}, fmt.Errorf("brightness %d is outside 0..100", *command.Brightness)
		}
		op.brightness = *command.Brightness
		return op, nil

	case CommandSetKeyImage:
		if command.Index == nil {
			return operation{}, fmt.Errorf("index is required")
		}
		if command.Brightness != nil || command.X != nil || command.Y != nil {
			return operation{}, fmt.Errorf("set_key_image accepts only index and image")
		}
		if *command.Index < 0 || *command.Index >= model.KeyCount() {
			return operation{}, fmt.Errorf("key index %d is outside 0..%d", *command.Index, model.KeyCount()-1)
		}
		width, height := model.KeyImageSize()
		img, revision, err := decodeImage(command.Image, width, height)
		if err != nil {
			return operation{}, fmt.Errorf("key image: %w", err)
		}
		op.index, op.image, op.revision = *command.Index, img, revision
		return op, nil

	case CommandSetLCDImage:
		if command.Index != nil || command.Brightness != nil || command.X != nil || command.Y != nil {
			return operation{}, fmt.Errorf("set_lcd_image accepts only image")
		}
		if model != streamdeck.ModelPlus {
			return operation{}, fmt.Errorf("full LCD output is not supported by %s", model)
		}
		img, revision, err := decodeImage(command.Image, streamdeck.LCDImageWidth, streamdeck.LCDImageHeight)
		if err != nil {
			return operation{}, fmt.Errorf("LCD image: %w", err)
		}
		op.image, op.revision = img, revision
		return op, nil

	case CommandSetTouchStrip:
		if command.Index != nil || command.Brightness != nil || command.X != nil || command.Y != nil {
			return operation{}, fmt.Errorf("set_touch_strip_image accepts only image")
		}
		if !model.HasTouchStrip() {
			return operation{}, fmt.Errorf("touch-strip output is not supported by %s", model)
		}
		img, revision, err := decodeImage(command.Image, streamdeck.TouchStripWidth, streamdeck.TouchStripHeight)
		if err != nil {
			return operation{}, fmt.Errorf("touch-strip image: %w", err)
		}
		op.image, op.revision = img, revision
		return op, nil

	case CommandSetTouchRegion:
		if command.X == nil || command.Y == nil {
			return operation{}, fmt.Errorf("x and y are required")
		}
		if command.Index != nil || command.Brightness != nil {
			return operation{}, fmt.Errorf("set_touch_strip_region accepts only x, y, and image")
		}
		if !model.HasTouchStrip() {
			return operation{}, fmt.Errorf("touch-strip output is not supported by %s", model)
		}
		if *command.X < 0 || *command.Y < 0 {
			return operation{}, fmt.Errorf("touch-strip region origin (%d,%d) must not be negative", *command.X, *command.Y)
		}
		img, revision, err := decodeImage(command.Image, 0, 0)
		if err != nil {
			return operation{}, fmt.Errorf("touch-strip region image: %w", err)
		}
		if *command.X+img.Bounds().Dx() > streamdeck.TouchStripWidth || *command.Y+img.Bounds().Dy() > streamdeck.TouchStripHeight {
			return operation{}, fmt.Errorf(
				"touch-strip region (%d,%d)+%dx%d exceeds %dx%d",
				*command.X, *command.Y, img.Bounds().Dx(), img.Bounds().Dy(),
				streamdeck.TouchStripWidth, streamdeck.TouchStripHeight,
			)
		}
		op.x, op.y, op.image, op.revision = *command.X, *command.Y, img, revision
		return op, nil

	default:
		return operation{}, fmt.Errorf("unsupported command type %q", command.Type)
	}
}

func decodeImage(payload *ImagePayload, width, height int) (*image.NRGBA, string, error) {
	if payload == nil {
		return nil, "", fmt.Errorf("image is required")
	}
	if payload.MimeType != "image/png" && payload.MimeType != "image/jpeg" {
		return nil, "", fmt.Errorf("mime_type %q is not image/png or image/jpeg", payload.MimeType)
	}
	if len(payload.Revision) > 256 {
		return nil, "", fmt.Errorf("revision is longer than 256 bytes")
	}
	raw, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decode data_base64: %w", err)
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("decoded image is empty")
	}
	if len(raw) > maxImageBytes {
		return nil, "", fmt.Errorf("decoded image is %d bytes, maximum is %d", len(raw), maxImageBytes)
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	wantFormat := strings.TrimPrefix(payload.MimeType, "image/")
	if format != wantFormat {
		return nil, "", fmt.Errorf("mime_type says %s but payload is image/%s", payload.MimeType, format)
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, "", fmt.Errorf("image has empty bounds %v", bounds)
	}
	if width > 0 && height > 0 && (bounds.Dx() != width || bounds.Dy() != height) {
		return nil, "", fmt.Errorf("image is %dx%d, want exactly %dx%d", bounds.Dx(), bounds.Dy(), width, height)
	}

	native := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(native, native.Bounds(), img, bounds.Min, draw.Src)
	revision := payload.Revision
	if revision == "" {
		digest := sha256.Sum256(raw)
		revision = "sha256:" + hex.EncodeToString(digest[:])
	}
	return native, revision, nil
}

func decodeCommandBatch(decoder *json.Decoder, model streamdeck.Model) (CommandBatch, []operation, error) {
	decoder.DisallowUnknownFields()
	var batch CommandBatch
	if err := decoder.Decode(&batch); err != nil {
		return CommandBatch{}, nil, fmt.Errorf("decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return CommandBatch{}, nil, fmt.Errorf("decode request: multiple JSON values are not allowed")
		}
		return CommandBatch{}, nil, fmt.Errorf("decode request trailing data: %w", err)
	}
	if len(batch.RequestID) > 256 {
		return CommandBatch{}, nil, fmt.Errorf("request_id is longer than 256 bytes")
	}
	operations, err := decodeOperations(model, batch.Commands)
	if err != nil {
		return CommandBatch{}, nil, err
	}
	return batch, operations, nil
}

func capabilitiesFor(model streamdeck.Model) DeviceCapabilities {
	width, height := model.KeyImageSize()
	result := DeviceCapabilities{
		Model:             modelName(model),
		KeyCount:          model.KeyCount(),
		Key:               SurfaceCapabilities{Width: width, Height: height},
		InputEvents:       []string{"key"},
		SupportedCommands: []CommandType{CommandSetBrightness, CommandSetKeyImage},
	}
	if model == streamdeck.ModelPlus {
		result.DialCount = streamdeck.DialCount
		result.LCD = &SurfaceCapabilities{Width: streamdeck.LCDImageWidth, Height: streamdeck.LCDImageHeight}
		result.TouchStrip = &SurfaceCapabilities{Width: streamdeck.TouchStripWidth, Height: streamdeck.TouchStripHeight, PartialUpdates: true}
		result.InputEvents = []string{"key", "dial_press", "dial_rotate", "touch_tap", "touch_press", "touch_flick"}
		result.SupportedCommands = append(result.SupportedCommands, CommandSetLCDImage, CommandSetTouchStrip, CommandSetTouchRegion)
	}
	return result
}

func modelName(model streamdeck.Model) string {
	switch model {
	case streamdeck.ModelPlus:
		return "stream_deck_plus"
	case streamdeck.ModelMini:
		return "stream_deck_mini"
	default:
		return "unknown"
	}
}
