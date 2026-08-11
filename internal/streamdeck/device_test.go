package streamdeck

import (
	"errors"
	"image"
	"testing"
	"time"
)

func TestCloseIsIdempotentAndOperationsReturnErrClosed(t *testing.T) {
	fake := &fakeHIDDevice{}
	deck := newPlus(fake)
	if err := deck.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := deck.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("underlying close calls = %d, want 1", fake.closeCalls)
	}

	if _, err := deck.Info(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Info error = %v, want ErrClosed", err)
	}
	if _, err := deck.ReadEvents(time.Millisecond); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadEvents error = %v, want ErrClosed", err)
	}
	if err := deck.SetBrightness(70); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetBrightness error = %v, want ErrClosed", err)
	}
	key := image.NewNRGBA(image.Rect(0, 0, KeyImageWidth, KeyImageHeight))
	if err := deck.SetKeyImage(0, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetKeyImage error = %v, want ErrClosed", err)
	}
	lcd := image.NewNRGBA(image.Rect(0, 0, LCDImageWidth, LCDImageHeight))
	if err := deck.SetLCDImage(lcd); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetLCDImage error = %v, want ErrClosed", err)
	}
	region := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	if err := deck.SetPartialWindowImage(0, 0, region); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetPartialWindowImage error = %v, want ErrClosed", err)
	}
	if err := deck.ShowLogo(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ShowLogo error = %v, want ErrClosed", err)
	}
	if err := deck.FillLCD(1, 2, 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("FillLCD error = %v, want ErrClosed", err)
	}
}

func TestReadEventsRejectsNegativeTimeout(t *testing.T) {
	deck := newPlus(&fakeHIDDevice{})
	if _, err := deck.ReadEvents(-time.Millisecond); err == nil {
		t.Fatal("negative timeout returned nil error")
	}
}
