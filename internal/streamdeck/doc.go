// Package streamdeck provides direct USB HID discovery, input decoding, and
// image and brightness output for supported Elgato Stream Deck models.
//
// Key and dial indexes in this package are zero-based physical indexes. The
// Plus devices additionally emit dial and touch events. The package
// intentionally exposes the relevant report-level behavior instead of acting
// as a generic device or UI framework.
package streamdeck
