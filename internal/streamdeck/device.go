package streamdeck

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/sstallion/go-hid"
)

var (
	// ErrTimeout means no input report arrived during a timed read.
	ErrTimeout = errors.New("streamdeck input read timeout")
	// ErrClosed means an operation was attempted on a closed Device.
	ErrClosed = errors.New("streamdeck device is closed")
)

// Device is the common interface implemented by every supported Stream Deck.
type Device interface {
	Info() (DeviceInfo, error)
	ReadEvents(time.Duration) (InputRead, error)
	SetBrightness(int) error
	SetKeyImage(int, image.Image) error
	Close() error
}

type hidDevice interface {
	ReadWithTimeout([]byte, time.Duration) (int, error)
	Write([]byte) (int, error)
	SendFeatureReport([]byte) (int, error)
	GetFeatureReport([]byte) (int, error)
	GetDeviceInfo() (*hid.DeviceInfo, error)
	Close() error
}

type hidEnumerator func(uint16, uint16, hid.EnumFunc) error
type hidOpener func(uint16, uint16) (hidDevice, error)

// DeviceInfo contains enumeration information for a supported Stream Deck.
type DeviceInfo struct {
	Path         string
	VendorID     uint16
	ProductID    uint16
	Model        Model
	Serial       string
	Manufacturer string
	Product      string
	Interface    int
	UsagePage    uint16
	Usage        uint16
}

type hidHandle struct {
	device   hidDevice
	model    Model
	readMu   sync.Mutex
	writeMu  sync.Mutex
	deviceMu sync.RWMutex
	closed   bool
}

// Deck is an open Stream Deck Plus HID handle. It retains the original Plus
// API, including SetTouchStripImage, while also implementing Device.
type Deck struct {
	handle  *hidHandle
	decoder inputDecoder
}

// Mini is an open original Stream Deck Mini HID handle.
type Mini struct {
	handle  *hidHandle
	decoder miniInputDecoder
}

// List enumerates all supported Stream Deck models. ProductIDAny is used so a
// single HID enumeration covers every supported Elgato PID.
func List() ([]DeviceInfo, error) {
	return listWith(hid.Enumerate)
}

func listWith(enumerate hidEnumerator) ([]DeviceInfo, error) {
	devices := make([]DeviceInfo, 0, 2)
	err := enumerate(VendorID, hid.ProductIDAny, func(info *hid.DeviceInfo) error {
		if _, supported := modelForProductID(info.ProductID); supported {
			devices = append(devices, deviceInfo(info))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate supported Stream Deck devices: %w", err)
	}
	return devices, nil
}

// Open opens the first Stream Deck Plus. It intentionally retains the
// original Plus-only behavior; use OpenAny to auto-detect any supported model.
func Open() (*Deck, error) {
	device, err := openFirstHID(VendorID, ProductID)
	if err != nil {
		return nil, fmt.Errorf("open Stream Deck Plus %04x:%04x: %w", VendorID, ProductID, err)
	}
	return newPlus(device), nil
}

// OpenModel opens the first connected device of an explicit supported model.
func OpenModel(model Model) (Device, error) {
	return openModelWith(model, openFirstHID)
}

// OpenAny opens the first Stream Deck Plus, falling back to Stream Deck Mini.
func OpenAny() (Device, error) {
	return openAnyWith(openFirstHID)
}

func openFirstHID(vendorID, productID uint16) (hidDevice, error) {
	return hid.OpenFirst(vendorID, productID)
}

func openModelWith(model Model, open hidOpener) (Device, error) {
	if err := validateModel(model); err != nil {
		return nil, err
	}
	device, err := open(VendorID, model.ProductID())
	if err != nil {
		return nil, fmt.Errorf("open %s %04x:%04x: %w", model, VendorID, model.ProductID(), err)
	}
	return newModelDevice(model, device), nil
}

func openAnyWith(open hidOpener) (Device, error) {
	var openErrors []error
	for _, model := range []Model{ModelPlus, ModelMini} {
		device, err := openModelWith(model, open)
		if err == nil {
			return device, nil
		}
		openErrors = append(openErrors, err)
	}
	return nil, fmt.Errorf("open any supported Stream Deck: %w", errors.Join(openErrors...))
}

func newModelDevice(model Model, device hidDevice) Device {
	switch model {
	case ModelMini:
		return newMini(device)
	default:
		return newPlus(device)
	}
}

func newPlus(device hidDevice) *Deck {
	return &Deck{handle: &hidHandle{device: device, model: ModelPlus}}
}

func newMini(device hidDevice) *Mini {
	return &Mini{handle: &hidHandle{device: device, model: ModelMini}}
}

// Info returns HID enumeration information for the open Plus.
func (d *Deck) Info() (DeviceInfo, error) {
	return d.handle.info()
}

// Info returns HID enumeration information for the open Mini.
func (d *Mini) Info() (DeviceInfo, error) {
	return d.handle.info()
}

func (h *hidHandle) info() (DeviceInfo, error) {
	h.deviceMu.RLock()
	defer h.deviceMu.RUnlock()
	if h.closed {
		return DeviceInfo{}, ErrClosed
	}
	info, err := h.device.GetDeviceInfo()
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("get %s device info: %w", h.model, err)
	}
	return deviceInfo(info), nil
}

// ReadEvents performs one timed HID read and normalizes one Plus input report.
func (d *Deck) ReadEvents(timeout time.Duration) (InputRead, error) {
	d.handle.readMu.Lock()
	defer d.handle.readMu.Unlock()
	report, err := d.handle.readReport(inputReportSize, timeout)
	if err != nil {
		return InputRead{}, err
	}
	result, err := d.decoder.Decode(report)
	if err != nil {
		return InputRead{}, fmt.Errorf("decode Stream Deck Plus input report=% x: %w", meaningfulInputBytes(report), err)
	}
	return result, nil
}

// ReadEvents performs one timed HID read and normalizes one Mini key snapshot.
func (d *Mini) ReadEvents(timeout time.Duration) (InputRead, error) {
	d.handle.readMu.Lock()
	defer d.handle.readMu.Unlock()
	report, err := d.handle.readReport(miniInputReportSize, timeout)
	if err != nil {
		return InputRead{}, err
	}
	result, err := d.decoder.Decode(report)
	if err != nil {
		return InputRead{}, fmt.Errorf("decode Stream Deck Mini input report=% x: %w", meaningfulMiniInputBytes(report), err)
	}
	return result, nil
}

func (h *hidHandle) readReport(reportSize int, timeout time.Duration) ([]byte, error) {
	if timeout < 0 {
		return nil, fmt.Errorf("input read timeout must not be negative: %s", timeout)
	}

	report := make([]byte, reportSize)
	h.deviceMu.RLock()
	if h.closed {
		h.deviceMu.RUnlock()
		return nil, ErrClosed
	}
	n, err := h.device.ReadWithTimeout(report, timeout)
	h.deviceMu.RUnlock()
	if err != nil {
		if errors.Is(err, hid.ErrTimeout) {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("read %s input: %w", h.model, err)
	}
	if n <= 0 || n > len(report) {
		return nil, fmt.Errorf("invalid HID read length %d", n)
	}
	return report[:n], nil
}

func (h *hidHandle) writeReports(name string, target int, reports [][]byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.deviceMu.RLock()
	defer h.deviceMu.RUnlock()
	if h.closed {
		return ErrClosed
	}
	for chunkIndex, report := range reports {
		n, err := h.device.Write(report)
		if err != nil {
			return fmt.Errorf("write %s %d image chunk %d: %w", name, target, chunkIndex, err)
		}
		if n != len(report) {
			return fmt.Errorf(
				"write %s %d image chunk %d: wrote %d bytes, want %d",
				name,
				target,
				chunkIndex,
				n,
				len(report),
			)
		}
	}
	return nil
}

func (h *hidHandle) sendFeatureReport(name string, report []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.deviceMu.RLock()
	defer h.deviceMu.RUnlock()
	if h.closed {
		return ErrClosed
	}
	n, err := h.device.SendFeatureReport(report)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if n != len(report) {
		return fmt.Errorf("%s: wrote %d bytes, want %d", name, n, len(report))
	}
	return nil
}

// getFeatureReport performs one getter control transfer on the control pipe.
// The caller seeds the buffer's report ID; the response arrives in the same
// buffer with the device-populated bytes.
func (h *hidHandle) getFeatureReport(name string, report []byte) ([]byte, error) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	h.deviceMu.RLock()
	defer h.deviceMu.RUnlock()
	if h.closed {
		return nil, ErrClosed
	}
	n, err := h.device.GetFeatureReport(report)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if n <= 0 || n > len(report) {
		return nil, fmt.Errorf("%s: invalid feature read length %d", name, n)
	}
	return report[:n], nil
}

// Close closes the Plus HID handle. It is safe to call more than once.
func (d *Deck) Close() error {
	return d.handle.close()
}

// Close closes the Mini HID handle. It is safe to call more than once.
func (d *Mini) Close() error {
	return d.handle.close()
}

func (h *hidHandle) close() error {
	h.deviceMu.Lock()
	defer h.deviceMu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if err := h.device.Close(); err != nil {
		return fmt.Errorf("close %s: %w", h.model, err)
	}
	return nil
}

func deviceInfo(info *hid.DeviceInfo) DeviceInfo {
	model, _ := modelForProductID(info.ProductID)
	return DeviceInfo{
		Path:         info.Path,
		VendorID:     info.VendorID,
		ProductID:    info.ProductID,
		Model:        model,
		Serial:       info.SerialNbr,
		Manufacturer: info.MfrStr,
		Product:      info.ProductStr,
		Interface:    info.InterfaceNbr,
		UsagePage:    info.UsagePage,
		Usage:        info.Usage,
	}
}

var _ Device = (*Deck)(nil)
var _ Device = (*Mini)(nil)
