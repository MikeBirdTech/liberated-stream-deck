// Package streamdeck provides direct USB HID discovery, input decoding, and
// image and brightness output for the Elgato Stream Deck Plus.
//
// Key and dial indexes in this package are zero-based physical indexes. The
// package intentionally exposes the relevant report-level behavior instead of
// acting as a generic device or UI framework.
package streamdeck
