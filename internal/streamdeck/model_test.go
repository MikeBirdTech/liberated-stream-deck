package streamdeck

import (
	"errors"
	"image"
	"reflect"
	"testing"

	"github.com/sstallion/go-hid"
)

func TestListWithUsesWildcardAndReportsSupportedModels(t *testing.T) {
	var gotVendorID, gotProductID uint16
	enumerate := func(vendorID, productID uint16, visit hid.EnumFunc) error {
		gotVendorID = vendorID
		gotProductID = productID
		for _, info := range []*hid.DeviceInfo{
			{Path: "plus", VendorID: VendorID, ProductID: ProductID, ProductStr: "Stream Deck +"},
			{Path: "other", VendorID: VendorID, ProductID: 0xffff, ProductStr: "Unsupported"},
			{Path: "mini", VendorID: VendorID, ProductID: MiniProductID, ProductStr: "Stream Deck Mini"},
		} {
			if err := visit(info); err != nil {
				return err
			}
		}
		return nil
	}

	devices, err := listWith(enumerate)
	if err != nil {
		t.Fatalf("listWith: %v", err)
	}
	if gotVendorID != VendorID || gotProductID != hid.ProductIDAny {
		t.Fatalf("Enumerate IDs = %04x:%04x, want %04x:%04x", gotVendorID, gotProductID, VendorID, hid.ProductIDAny)
	}
	want := []DeviceInfo{
		{Path: "plus", VendorID: VendorID, ProductID: ProductID, Model: ModelPlus, Product: "Stream Deck +"},
		{Path: "mini", VendorID: VendorID, ProductID: MiniProductID, Model: ModelMini, Product: "Stream Deck Mini"},
	}
	if !reflect.DeepEqual(devices, want) {
		t.Fatalf("devices = %#v, want %#v", devices, want)
	}
}

func TestOpenModelRoutesByProductID(t *testing.T) {
	for _, test := range []struct {
		model Model
		pid   uint16
		kind  any
	}{
		{model: ModelPlus, pid: ProductID, kind: (*Deck)(nil)},
		{model: ModelMini, pid: MiniProductID, kind: (*Mini)(nil)},
	} {
		t.Run(test.model.String(), func(t *testing.T) {
			var gotVendorID, gotProductID uint16
			device, err := openModelWith(test.model, func(vendorID, productID uint16) (hidDevice, error) {
				gotVendorID = vendorID
				gotProductID = productID
				return &fakeHIDDevice{}, nil
			})
			if err != nil {
				t.Fatalf("openModelWith: %v", err)
			}
			if gotVendorID != VendorID || gotProductID != test.pid {
				t.Fatalf("open IDs = %04x:%04x, want %04x:%04x", gotVendorID, gotProductID, VendorID, test.pid)
			}
			switch test.kind.(type) {
			case *Deck:
				if _, ok := device.(*Deck); !ok {
					t.Fatalf("device type = %T, want *Deck", device)
				}
			case *Mini:
				if _, ok := device.(*Mini); !ok {
					t.Fatalf("device type = %T, want *Mini", device)
				}
			}
		})
	}
}

func TestOpenModelRejectsUnknownModelWithoutOpening(t *testing.T) {
	called := false
	_, err := openModelWith(ModelUnknown, func(uint16, uint16) (hidDevice, error) {
		called = true
		return &fakeHIDDevice{}, nil
	})
	if err == nil || called {
		t.Fatalf("error = %v, opener called = %t", err, called)
	}
}

func TestOpenAnyPrefersPlusThenFallsBackToMini(t *testing.T) {
	var productIDs []uint16
	device, err := openAnyWith(func(_ uint16, productID uint16) (hidDevice, error) {
		productIDs = append(productIDs, productID)
		if productID == ProductID {
			return nil, errors.New("Plus absent")
		}
		return &fakeHIDDevice{}, nil
	})
	if err != nil {
		t.Fatalf("openAnyWith: %v", err)
	}
	if _, ok := device.(*Mini); !ok {
		t.Fatalf("device type = %T, want *Mini", device)
	}
	want := []uint16{ProductID, MiniProductID}
	if !reflect.DeepEqual(productIDs, want) {
		t.Fatalf("open product IDs = %04x, want %04x", productIDs, want)
	}
}

func TestOpenAnyStopsAfterPlusSuccess(t *testing.T) {
	var productIDs []uint16
	device, err := openAnyWith(func(_ uint16, productID uint16) (hidDevice, error) {
		productIDs = append(productIDs, productID)
		return &fakeHIDDevice{}, nil
	})
	if err != nil {
		t.Fatalf("openAnyWith: %v", err)
	}
	if _, ok := device.(*Deck); !ok {
		t.Fatalf("device type = %T, want *Deck", device)
	}
	if !reflect.DeepEqual(productIDs, []uint16{ProductID}) {
		t.Fatalf("open product IDs = %04x, want [%04x]", productIDs, ProductID)
	}
}

func TestMiniInfoAndCloseUseSharedHandle(t *testing.T) {
	fake := &fakeHIDDevice{info: &hid.DeviceInfo{
		VendorID: VendorID, ProductID: MiniProductID, ProductStr: "Stream Deck Mini",
	}}
	mini := newMini(fake)
	info, err := mini.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Model != ModelMini {
		t.Fatalf("model = %v, want %v", info.Model, ModelMini)
	}
	if err := mini.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mini.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("underlying close calls = %d, want 1", fake.closeCalls)
	}
	if _, err := mini.Info(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Info error = %v, want ErrClosed", err)
	}
	if _, err := mini.ReadEvents(0); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadEvents error = %v, want ErrClosed", err)
	}
	if err := mini.SetBrightness(70); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetBrightness error = %v, want ErrClosed", err)
	}
	key := image.NewNRGBA(image.Rect(0, 0, MiniKeyImageWidth, MiniKeyImageHeight))
	if err := mini.SetKeyImage(0, key); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetKeyImage error = %v, want ErrClosed", err)
	}
}
