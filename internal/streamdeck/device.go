package streamdeck

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sstallion/go-hid"
)

// ErrTimeout means no input report arrived during a timed read.
var ErrTimeout = errors.New("streamdeck input read timeout")

type hidDevice interface {
	ReadWithTimeout([]byte, time.Duration) (int, error)
	Write([]byte) (int, error)
	SendFeatureReport([]byte) (int, error)
	GetDeviceInfo() (*hid.DeviceInfo, error)
	Close() error
}

// DeviceInfo contains enumeration information for a Stream Deck Plus.
type DeviceInfo struct {
	Path         string
	VendorID     uint16
	ProductID    uint16
	Serial       string
	Manufacturer string
	Product      string
	Interface    int
	UsagePage    uint16
	Usage        uint16
}

// Deck is an open Stream Deck Plus HID handle.
type Deck struct {
	device  hidDevice
	decoder inputDecoder
	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

// List enumerates Stream Deck Plus HID devices by the official VID/PID.
func List() ([]DeviceInfo, error) {
	devices := make([]DeviceInfo, 0, 1)
	err := hid.Enumerate(VendorID, ProductID, func(info *hid.DeviceInfo) error {
		devices = append(devices, deviceInfo(info))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate Stream Deck Plus: %w", err)
	}
	return devices, nil
}

// Open opens the first Stream Deck Plus using go-hid's ordinary public API.
func Open() (*Deck, error) {
	device, err := hid.OpenFirst(VendorID, ProductID)
	if err != nil {
		return nil, fmt.Errorf("open Stream Deck Plus %04x:%04x: %w", VendorID, ProductID, err)
	}
	return &Deck{device: device}, nil
}

// Info returns HID enumeration information for the open device.
func (d *Deck) Info() (DeviceInfo, error) {
	info, err := d.device.GetDeviceInfo()
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("get Stream Deck Plus device info: %w", err)
	}
	return deviceInfo(info), nil
}

// ReadEvents performs one timed HID read and normalizes one input report.
func (d *Deck) ReadEvents(timeout time.Duration) (InputRead, error) {
	report := make([]byte, inputReportSize)
	n, err := d.device.ReadWithTimeout(report, timeout)
	if err != nil {
		if errors.Is(err, hid.ErrTimeout) {
			return InputRead{}, ErrTimeout
		}
		return InputRead{}, fmt.Errorf("read Stream Deck Plus input: %w", err)
	}
	if n <= 0 || n > len(report) {
		return InputRead{}, fmt.Errorf("invalid HID read length %d", n)
	}

	result, err := d.decoder.Decode(report[:n])
	if err != nil {
		return InputRead{}, fmt.Errorf("decode Stream Deck Plus input report=% x: %w", meaningfulInputBytes(report[:n]), err)
	}
	return result, nil
}

// Close closes the HID handle. It is safe to call more than once.
func (d *Deck) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if err := d.device.Close(); err != nil {
		return fmt.Errorf("close Stream Deck Plus: %w", err)
	}
	return nil
}

func deviceInfo(info *hid.DeviceInfo) DeviceInfo {
	return DeviceInfo{
		Path:         info.Path,
		VendorID:     info.VendorID,
		ProductID:    info.ProductID,
		Serial:       info.SerialNbr,
		Manufacturer: info.MfrStr,
		Product:      info.ProductStr,
		Interface:    info.InterfaceNbr,
		UsagePage:    info.UsagePage,
		Usage:        info.Usage,
	}
}
